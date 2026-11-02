// Package workspace captures source bytes without changing the user's index or HEAD.
package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/storage"
	"sort"
	"strings"
	"unicode/utf8"
)

type Service struct {
	Root, Project string
	Objects       storage.CAS
	Build         domain.Build
}

func Git(ctx context.Context, root string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, "git", append([]string{"-c", "core.fsmonitor=false", "-C", root}, args...)...)
	b, e := c.Output()
	if e != nil {
		return nil, fmt.Errorf("git %v: %w", args, e)
	}
	return b, nil
}
func ValidPath(p string) bool {
	return utf8.ValidString(p) && !strings.ContainsRune(p, 0) && p != "" && !strings.Contains(p, "\\") && !filepath.IsAbs(p) && filepath.ToSlash(filepath.Clean(p)) == p && p != ".." && !strings.HasPrefix(p, "../")
}

const SourcePolicy = "seai.source-policy/v2"

func MetadataExclusions() []string {
	return []string{"**/.agents/**", "**/.codex/**", "**/.gigacode/**", "**/AGENTS.md", "**/GIGACODE.md"}
}
func excluded(p string) bool {
	for _, part := range strings.Split(p, "/") {
		if part == ".agents" || part == ".codex" || part == ".gigacode" || part == "AGENTS.md" || part == "GIGACODE.md" {
			return true
		}
	}
	return legacyExcluded(p)
}
func legacyExcluded(p string) bool {
	return p == ".git" || strings.HasPrefix(p, ".git/") || p == ".seai" || strings.HasPrefix(p, ".seai/") || strings.HasSuffix(p, ".key") || filepath.Base(p) == ".env" || strings.HasPrefix(p, "vendor/")
}
func (s Service) capture(ctx context.Context, ref string, persist bool) (domain.Manifest, error) {
	m := domain.Manifest{Schema: "seai.source/v1", Repository: s.Project, Build: s.Build, Files: []domain.File{}}
	target := "HEAD"
	if ref != "" {
		target = ref
	}
	if strings.HasPrefix(target, "-") {
		return m, domain.Err("INVALID_REF", "ref cannot start with '-'")
	}
	commit, e := Git(ctx, s.Root, "rev-parse", "--verify", target+"^{commit}")
	if e != nil {
		return m, e
	}
	m.Commit = strings.TrimSpace(string(commit))
	sourceRoot, e := os.OpenRoot(s.Root)
	if e != nil {
		return m, e
	}
	defer sourceRoot.Close()
	var entries []string
	tracked := map[string]bool{}
	var batch *blobBatch
	if ref != "" {
		b, e := Git(ctx, s.Root, "ls-tree", "-rz", "--full-tree", m.Commit)
		if e != nil {
			return m, e
		}
		entries = strings.Split(string(b), "\x00")
		batch, e = newBlobBatch(ctx, s.Root)
		if e != nil {
			return m, e
		}
		defer batch.abort()
	} else {
		b, e := Git(ctx, s.Root, "ls-files", "--cached", "-z")
		if e != nil {
			return m, e
		}
		for _, path := range strings.Split(string(b), "\x00") {
			if path != "" && !tracked[path] {
				tracked[path] = true
				entries = append(entries, path)
			}
		}
		b, e = Git(ctx, s.Root, "ls-files", "--others", "--exclude-standard", "-z")
		if e != nil {
			return m, e
		}
		for _, path := range strings.Split(string(b), "\x00") {
			if path != "" && !tracked[path] && relevantUntracked(path) {
				entries = append(entries, path)
			}
		}
	}
	seen := map[string]bool{}
	folded := map[string]string{}
	for _, entry := range entries {
		if e = ctx.Err(); e != nil {
			return m, e
		}
		if entry == "" {
			continue
		}
		p := entry
		mode := "100644"
		var data []byte
		if ref != "" {
			parts := strings.SplitN(entry, "\t", 2)
			if len(parts) != 2 {
				return m, fmt.Errorf("invalid tree entry")
			}
			p = parts[1]
			fields := strings.Fields(parts[0])
			if len(fields) != 3 {
				return m, fmt.Errorf("invalid tree metadata")
			}
			mode = fields[0]
			if excluded(p) {
				continue
			}
			if mode != "100644" && mode != "100755" {
				return m, domain.Err("UNSUPPORTED_SOURCE", "symlink/submodule: %s", p)
			}
			data, e = batch.read(fields[2])
		} else {
			if excluded(p) {
				continue
			}
			if seen[p] {
				continue
			}
			if !ValidPath(p) {
				return m, domain.Err("INVALID_PATH", "%q", p)
			}
			// Reject every symlink ancestor rather than following it outside the root.
			parts := strings.Split(p, "/")
			current := ""
			var info os.FileInfo
			for _, part := range parts {
				current = filepath.Join(current, part)
				info, e = sourceRoot.Lstat(current)
				if e != nil {
					break
				}
				if info.Mode()&os.ModeSymlink != 0 {
					return m, domain.Err("UNSUPPORTED_SOURCE", "symlink: %s", p)
				}
			}
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return m, e
			}
			if !info.Mode().IsRegular() {
				return m, domain.Err("UNSUPPORTED_SOURCE", "nonregular file: %s", p)
			}
			if info.Mode()&0111 != 0 {
				mode = "100755"
			}
			data, e = sourceRoot.ReadFile(p)
		}
		if e != nil {
			return m, e
		}
		if !ValidPath(p) {
			return m, domain.Err("INVALID_PATH", "%q", p)
		}
		lower := strings.ToLower(p)
		if old, ok := folded[lower]; ok && old != p {
			return m, domain.Err("PATH_COLLISION", "%s and %s", old, p)
		}
		folded[lower] = p
		seen[p] = true
		id := domain.Digest(data)
		if persist {
			var err error
			id, err = s.Objects.Put(data)
			if err != nil {
				return m, err
			}
		}
		m.Files = append(m.Files, domain.File{Path: p, Mode: mode, Digest: id})
	}
	if batch != nil {
		if e = batch.finish(); e != nil {
			return m, e
		}
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	return m, nil
}
func relevantUntracked(p string) bool {
	return !excluded(p) && (strings.HasSuffix(p, ".go") || strings.HasPrefix(p, "testdata/") || p == "go.mod" || p == "go.sum")
}

// Observe computes the same stable working-tree identity without writing CAS.
func (s Service) Observe(ctx context.Context) (domain.Manifest, string, error) {
	return s.stable(ctx, "", false)
}
func (s Service) Capture(ctx context.Context, ref string) (domain.Manifest, string, error) {
	return s.stable(ctx, ref, true)
}
func (s Service) stable(ctx context.Context, ref string, persist bool) (domain.Manifest, string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		a, e := s.capture(ctx, ref, persist)
		if e != nil {
			return a, "", e
		}
		if ref == "" {
			b, e := s.capture(ctx, ref, persist)
			if e != nil {
				return a, "", e
			}
			x, _ := domain.Canonical(a)
			y, _ := domain.Canonical(b)
			if !bytes.Equal(x, y) {
				continue
			}
		}
		b, e := domain.Canonical(a)
		if e != nil {
			return a, "", e
		}
		if !persist {
			return a, domain.Digest(b), nil
		}
		id, e := s.Objects.Put(b)
		return a, id, e
	}
	return domain.Manifest{}, "", domain.Err("WORKSPACE_BUSY", "source changed during three capture attempts")
}
func Materialize(m domain.Manifest, c storage.CAS, dest string) error {
	for _, f := range m.Files {
		if !ValidPath(f.Path) {
			return domain.Err("INVALID_PATH", "%q", f.Path)
		}
		b, e := c.Get(f.Digest)
		if e != nil {
			return e
		}
		p := filepath.Join(dest, filepath.FromSlash(f.Path))
		if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			return e
		}
		mode := os.FileMode(0400)
		if f.Mode == "100755" {
			mode = 0500
		}
		if e = os.WriteFile(p, b, mode); e != nil {
			return e
		}
	}
	return nil
}

// ValidSourcePath applies snapshot exclusions to new candidate files too.
func ValidSourcePath(p string) bool { return ValidStoredSourcePath(p) && !excluded(p) }

// ValidStoredSourcePath preserves safety validation for historical manifests,
// including regular agent metadata files captured before policy v2.
func ValidStoredSourcePath(p string) bool {
	if !ValidPath(p) || legacyExcluded(p) {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".git" || part == ".seai" {
			return false
		}
	}
	return true
}
