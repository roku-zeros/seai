// Package reconcile merges immutable snapshots without modifying the repository.
package reconcile

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/storage"
	"sort"
)

func Equal(a, b *domain.File) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Digest == b.Digest && a.Mode == b.Mode
}
func Map(m domain.Manifest) map[string]*domain.File {
	out := map[string]*domain.File{}
	for _, f := range m.Files {
		f := f
		out[f.Path] = &f
	}
	return out
}
func Paths(a, b domain.Manifest) []string {
	x, y := Map(a), Map(b)
	set := map[string]bool{}
	for p := range x {
		set[p] = true
	}
	for p := range y {
		set[p] = true
	}
	out := []string{}
	for p := range set {
		if !Equal(x[p], y[p]) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
func Merge(ctx context.Context, base, ours, theirs domain.Manifest, cas storage.CAS, parent string) (domain.Manifest, []domain.Conflict, error) {
	b, o, n := Map(base), Map(ours), Map(theirs)
	set := map[string]bool{}
	for p := range b {
		set[p] = true
	}
	for p := range o {
		set[p] = true
	}
	for p := range n {
		set[p] = true
	}
	paths := []string{}
	for p := range set {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	result := theirs
	result.Files = []domain.File{}
	conflicts := []domain.Conflict{}
	for _, p := range paths {
		var chosen *domain.File
		switch {
		case Equal(o[p], b[p]):
			chosen = n[p]
		case Equal(n[p], b[p]) || Equal(o[p], n[p]):
			chosen = o[p]
		default:
			conflict := domain.Conflict{Path: p, Kind: "content", Base: b[p], Ours: o[p], Theirs: n[p]}
			chosen = n[p]
			if b[p] == nil || o[p] == nil || n[p] == nil {
				conflict.Kind = "add-delete-modify"
			} else if o[p].Mode != b[p].Mode || n[p].Mode != b[p].Mode {
				conflict.Kind = "mode"
			} else {
				blobs := [][]byte{}
				for _, f := range []*domain.File{o[p], b[p], n[p]} {
					data, e := cas.Get(f.Digest)
					if e != nil {
						return result, conflicts, e
					}
					blobs = append(blobs, data)
				}
				if bytes.IndexByte(bytes.Join(blobs, nil), 0) >= 0 {
					conflict.Kind = "binary"
				} else {
					temp, e := os.MkdirTemp(parent, "merge-")
					if e != nil {
						return result, conflicts, e
					}
					names := []string{}
					for i, label := range []string{"ours", "base", "theirs"} {
						path := filepath.Join(temp, label)
						if e = os.WriteFile(path, blobs[i], 0600); e != nil {
							os.RemoveAll(temp)
							return result, conflicts, e
						}
						names = append(names, path)
					}
					cmd := exec.CommandContext(ctx, "git", "merge-file", "-p", "-L", "candidate", "-L", "baseline", "-L", "release", names[0], names[1], names[2])
					data, mergeErr := cmd.Output()
					os.RemoveAll(temp)
					if ctx.Err() != nil {
						return result, conflicts, ctx.Err()
					}
					d, e := cas.Put(data)
					if e != nil {
						return result, conflicts, e
					}
					if mergeErr == nil {
						f := *o[p]
						f.Digest = d
						chosen = &f
						conflict.Path = ""
					} else {
						conflict.MarkerDigest = d
					}
				}
			}
			if conflict.Path != "" {
				conflicts = append(conflicts, conflict)
			}
		}
		if chosen != nil {
			result.Files = append(result.Files, *chosen)
		}
	}
	return result, conflicts, nil
}
