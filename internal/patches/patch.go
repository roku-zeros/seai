// Package patches integrates bounded file operations into a new immutable CAS manifest.
package patches

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/storage"
	"seai.local/seai/internal/workspace"
)

func Apply(base domain.Manifest, baseID, specDigest string, p domain.Patch, scope []string, cas storage.CAS) (domain.Manifest, string, error) {
	if p.Schema != "seai.patch/v1" || p.Base != baseID || p.SpecDigest != specDigest {
		return base, "", domain.Err("PRECONDITION_FAILED", "patch must bind the current candidate and spec digest")
	}
	if len(p.Files) == 0 || len(p.Files) > 32 {
		return base, "", domain.Err("SCHEMA_INVALID", "1..32 operations required")
	}
	allowed := map[string]bool{}
	for _, f := range scope {
		allowed[f] = true
	}
	files := map[string]domain.File{}
	for _, f := range base.Files {
		if _, e := cas.Get(f.Digest); e != nil {
			return base, "", e
		}
		files[f.Path] = f
	}
	seen := map[string]bool{}
	total := 0
	// Validate every precondition before publishing any new candidate.
	for _, op := range p.Files {
		if !workspace.ValidSourcePath(op.Path) || !allowed[op.Path] || op.Path == ".git" || strings.HasPrefix(op.Path, ".git/") || op.Path == ".seai" || strings.HasPrefix(op.Path, ".seai/") {
			return base, "", domain.Err("SCOPE_VIOLATION", "invalid/out-of-scope path %s", op.Path)
		}
		if seen[op.Path] {
			return base, "", domain.Err("SCHEMA_INVALID", "duplicate operation path")
		}
		seen[op.Path] = true
		before, exists := files[op.Path]
		if op.Before == nil {
			if exists || op.BeforeMode != "" || op.Delete {
				return base, "", domain.Err("PRECONDITION_FAILED", "expected absent path %s", op.Path)
			}
		} else if !exists || *op.Before != before.Digest || op.BeforeMode != before.Mode {
			return base, "", domain.Err("PRECONDITION_FAILED", "before hash/mode differs: %s", op.Path)
		}
		if op.Delete {
			if op.Content != nil || op.Base64 != nil || op.Mode != "" || op.AfterDigest != nil {
				return base, "", domain.Err("SCHEMA_INVALID", "deletion has no after content/mode")
			}
			continue
		}
		if (op.Content == nil) == (op.Base64 == nil) || op.Mode != "100644" && op.Mode != "100755" {
			return base, "", domain.Err("SCHEMA_INVALID", "exactly one content encoding and regular/executable mode required")
		}
		b, e := decode(op)
		if e != nil {
			return base, "", e
		}
		if op.AfterDigest == nil {
			return base, "", domain.Err("SCHEMA_INVALID", "after_digest required for after content")
		}
		if domain.Digest(b) != *op.AfterDigest {
			return base, "", domain.Err("POSTCONDITION_FAILED", "after content hash differs: %s", op.Path)
		}
		total += len(b)
		if len(b) > 65536 || total > 524288 {
			return base, "", domain.Err("LIMIT_EXCEEDED", "patch after content max 64 KiB/file, 512 KiB total")
		}
	}
	for _, op := range p.Files {
		if op.Delete {
			delete(files, op.Path)
			continue
		}
		b, _ := decode(op)
		id, e := cas.Put(b)
		if e != nil {
			return base, "", e
		}
		files[op.Path] = domain.File{Path: op.Path, Mode: op.Mode, Digest: id}
	}
	result := base
	result.Files = []domain.File{}
	folded := map[string]string{}
	for path, f := range files {
		lower := strings.ToLower(path)
		if old, ok := folded[lower]; ok && old != path {
			return base, "", domain.Err("PATH_COLLISION", "%s and %s", old, path)
		}
		folded[lower] = path
		for parent := filepath.ToSlash(filepath.Dir(path)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			if _, ok := files[parent]; ok {
				return base, "", domain.Err("PATH_COLLISION", "file is also a parent: %s", parent)
			}
		}
		result.Files = append(result.Files, f)
	}
	sort.Slice(result.Files, func(i, j int) bool { return result.Files[i].Path < result.Files[j].Path })
	data, e := domain.Canonical(result)
	if e != nil {
		return base, "", e
	}
	id, e := cas.Put(data)
	if e == nil && id == baseID {
		return base, "", domain.Err("EMPTY_PATCH", "patch has no effect")
	}
	return result, id, e
}
func decode(op domain.FileOperation) ([]byte, error) {
	if op.Content != nil {
		return []byte(*op.Content), nil
	}
	b, e := base64.StdEncoding.DecodeString(*op.Base64)
	if e != nil {
		return nil, domain.Err("SCHEMA_INVALID", "invalid base64 content")
	}
	return b, nil
}
func Changed(a, b domain.Manifest) []string {
	old := map[string]domain.File{}
	for _, f := range a.Files {
		old[f.Path] = f
	}
	changed := map[string]bool{}
	for _, f := range b.Files {
		if prev, ok := old[f.Path]; !ok || prev != f {
			changed[f.Path] = true
		}
		delete(old, f.Path)
	}
	for p := range old {
		changed[p] = true
	}
	result := []string{}
	for p := range changed {
		result = append(result, p)
	}
	sort.Strings(result)
	return result
}
func Diff(ctx context.Context, a, b domain.Manifest, cas storage.CAS) (string, []string, error) {
	paths := Changed(a, b)
	if len(paths) == 0 {
		return "", paths, nil
	}
	changed := map[string]bool{}
	for _, p := range paths {
		changed[p] = true
	}
	selectFiles := func(m domain.Manifest) domain.Manifest {
		copy := m
		copy.Files = []domain.File{}
		for _, f := range m.Files {
			if changed[f.Path] {
				copy.Files = append(copy.Files, f)
			}
		}
		return copy
	}
	a = selectFiles(a)
	b = selectFiles(b)
	root, e := os.MkdirTemp("", "seai-diff-")
	if e != nil {
		return "", nil, e
	}
	defer os.RemoveAll(root)
	before, after, gitdir := filepath.Join(root, "before"), filepath.Join(root, "after"), filepath.Join(root, "git")
	if e = os.MkdirAll(before, 0700); e != nil {
		return "", nil, e
	}
	if e = os.MkdirAll(after, 0700); e != nil {
		return "", nil, e
	}
	for _, m := range []domain.Manifest{a, b} {
		for _, f := range m.Files {
			blob, e := cas.Get(f.Digest)
			if e != nil {
				return "", nil, e
			}
			if len(blob) > 1048576 {
				return "", nil, domain.Err("LIMIT_EXCEEDED", "diff file <=1 MiB")
			}
		}
	}
	if e = workspace.Materialize(a, cas, before); e != nil {
		return "", nil, e
	}
	if e = workspace.Materialize(b, cas, after); e != nil {
		return "", nil, e
	}
	run := func(tree string, args ...string) ([]byte, error) {
		argv := []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "core.autocrlf=false", "--git-dir=" + gitdir, "--work-tree=" + tree}
		argv = append(argv, args...)
		cmd := exec.CommandContext(ctx, "git", argv...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ATTR_NOSYSTEM=1", "LC_ALL=C"}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, e := cmd.Output()
		if e != nil {
			return nil, fmt.Errorf("isolated git diff: %w: %.1024s", e, stderr.Bytes())
		}
		return out, nil
	}
	init := exec.CommandContext(ctx, "git", "init", "--bare", gitdir)
	init.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C"}
	if out, e := init.CombinedOutput(); e != nil {
		return "", nil, fmt.Errorf("git init: %w: %s", e, out)
	}
	if e = os.WriteFile(filepath.Join(gitdir, "info", "attributes"), []byte("* -text -filter -ident -working-tree-encoding\n"), 0600); e != nil {
		return "", nil, e
	}
	if _, e = run(before, "add", "--force", "--all", "--", "."); e != nil {
		return "", nil, e
	}
	tree, e := run(before, "write-tree")
	if e != nil {
		return "", nil, e
	}
	if _, e = run(after, "add", "--force", "--all", "--", "."); e != nil {
		return "", nil, e
	}
	diff, e := run(after, "diff", "--cached", "--binary", "--no-ext-diff", "--no-textconv", strings.TrimSpace(string(tree)), "--")
	if e != nil {
		return "", nil, e
	}
	if len(diff) > 1048576 {
		return "", nil, domain.Err("LIMIT_EXCEEDED", "export patch <=1 MiB")
	}
	return string(diff), paths, nil
}
