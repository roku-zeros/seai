package workspace

import (
	"context"
	"os"
	"path/filepath"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/storage"
	"testing"
)

func setup(t *testing.T) Service {
	t.Helper()
	root := t.TempDir()
	ctx := context.Background()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "test@example.invalid"}, {"config", "user.name", "Test"}} {
		if _, e := Git(ctx, root, args...); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.26.6\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "rule.go"), []byte("package fixture\nfunc Rule() int {return 1}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "baseline"}} {
		if _, e := Git(ctx, root, args...); e != nil {
			t.Fatal(e)
		}
	}
	return Service{Root: root, Project: "fixture", Objects: storage.CAS{Root: t.TempDir()}, Build: domain.Build{Toolchain: "test", Tags: []string{}}}
}
func TestImmutableDirtyCapture(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	baseline, id, e := s.Capture(ctx, "HEAD")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(s.Root, "rule.go"), []byte("package fixture\nfunc Rule() int {return 2}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(s.Root, "scratch.go"), []byte("package fixture\n"), 0600); e != nil {
		t.Fatal(e)
	}
	dirty, newID, e := s.Capture(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	if newID == id || len(dirty.Files) != 3 {
		t.Fatal("dirty/untracked source lost")
	}
	again, refID, e := s.Capture(ctx, "HEAD")
	if e != nil || refID != id {
		t.Fatal(refID, e)
	}
	if again.Commit != baseline.Commit {
		t.Fatal("HEAD changed")
	}
	dir := t.TempDir()
	if e = Materialize(baseline, s.Objects, dir); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(dir, "rule.go"))
	if e != nil || string(b) != "package fixture\nfunc Rule() int {return 1}\n" {
		t.Fatal(string(b), e)
	}
}
func TestRejectSymlink(t *testing.T) {
	s := setup(t)
	if e := os.Symlink("/etc/passwd", filepath.Join(s.Root, "escape.go")); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.Capture(context.Background(), ""); e == nil {
		t.Fatal("symlink followed")
	}
}
func TestPaths(t *testing.T) {
	for _, p := range []string{"../x", "/tmp/x", "a/../b", "a\\b", ""} {
		if ValidPath(p) {
			t.Fatal(p)
		}
	}
}
