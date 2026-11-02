package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentMetadataSymlinksExcludedInBothModes(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	outside := t.TempDir()
	if e := os.WriteFile(filepath.Join(outside, "secret.go"), []byte("never include this"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{".agents", ".codex", ".gigacode"} {
		if e := os.Symlink(outside, filepath.Join(s.Root, name)); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.Symlink("GIGACODE.md", filepath.Join(s.Root, "AGENTS.md")); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(s.Root, "GIGACODE.md"), []byte("metadata only"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "metadata links"}} {
		if _, e := Git(ctx, s.Root, args...); e != nil {
			t.Fatal(e)
		}
	}
	var previous string
	for _, ref := range []string{"", "HEAD"} {
		m, id, e := s.Capture(ctx, ref)
		if e != nil {
			t.Fatal(e)
		}
		if len(m.Files) != 2 {
			t.Fatalf("metadata copied into source: %+v", m.Files)
		}
		if previous != "" && previous != id {
			t.Fatal("working tree/ref differ for identical source")
		}
		previous = id
	}
	for _, name := range []string{".agents", ".codex", ".gigacode", "AGENTS.md"} {
		if info, e := os.Lstat(filepath.Join(s.Root, name)); e != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatal("metadata symlink modified", name, e)
		}
		if ValidSourcePath(name + "/new.go") {
			t.Fatal("candidate can reintroduce excluded metadata", name)
		}
	}
}
func TestUntrackedIrrelevantLinkIsNotInspectedButSourceLinkRejected(t *testing.T) {
	s := setup(t)
	if e := os.Symlink("/nonexistent/outside", filepath.Join(s.Root, "notes.txt")); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.Observe(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("/nonexistent/outside", filepath.Join(s.Root, "source.go")); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.Observe(context.Background()); e == nil {
		t.Fatal("source symlink was silently ignored")
	}
}
func TestObserveIsReadOnlyAndDetectsSameSizePreservedMtime(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	_, before, e := s.Observe(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(s.Objects.Root, "objects")); !os.IsNotExist(e) {
		t.Fatal("Observe wrote CAS", e)
	}
	path := filepath.Join(s.Root, "rule.go")
	info, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	original, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	changed := strings.Replace(string(original), "return 1", "return 2", 1)
	if len(changed) != len(original) {
		t.Fatal("fixture size changed")
	}
	if e = os.WriteFile(path, []byte(changed), info.Mode()); e != nil {
		t.Fatal(e)
	}
	if e = os.Chtimes(path, info.ModTime(), info.ModTime()); e != nil {
		t.Fatal(e)
	}
	_, after, e := s.Observe(ctx)
	if e != nil || after == before {
		t.Fatal("freshness relied on timestamps", e)
	}
	_, captured, e := s.Capture(ctx, "")
	if e != nil || captured != after {
		t.Fatal("Observe identity differs from Capture", e)
	}
}
func TestCaptureLargeRepositoryUsesConstantGitProcesses(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	dir := filepath.Join(s.Root, "tracked")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	const count = 2627
	for i := 0; i < count; i++ {
		if e := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%04d.txt", i)), []byte(fmt.Sprintf("tracked fixture %d\n", i)), 0600); e != nil {
			t.Fatal(e)
		}
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "large fixture"}} {
		if _, e := Git(ctx, s.Root, args...); e != nil {
			t.Fatal(e)
		}
	}
	trace := filepath.Join(t.TempDir(), "git-trace")
	t.Setenv("GIT_TRACE", trace)
	started := time.Now()
	m, observed, e := s.Observe(ctx)
	observeTime := time.Since(started)
	if e != nil || len(m.Files) != count+2 {
		t.Fatal(len(m.Files), e)
	}
	raw, e := os.ReadFile(trace)
	if e != nil {
		t.Fatal(e)
	}
	commands := strings.Count(string(raw), "built-in: git ")
	if commands != 6 {
		t.Fatalf("expected 6 Git processes for two complete passes, got %d: %s", commands, raw)
	}
	if e = os.WriteFile(trace, nil, 0600); e != nil {
		t.Fatal(e)
	}
	started = time.Now()
	_, working, e := s.Capture(ctx, "")
	captureTime := time.Since(started)
	if e != nil || working != observed {
		t.Fatal(e)
	}
	raw, e = os.ReadFile(trace)
	if e != nil {
		t.Fatal(e)
	}
	if n := strings.Count(string(raw), "built-in: git "); n != 6 {
		t.Fatalf("Capture spawned %d Git processes", n)
	}
	if e = os.WriteFile(trace, nil, 0600); e != nil {
		t.Fatal(e)
	}
	started = time.Now()
	_, ref, e := s.Capture(ctx, "HEAD")
	refTime := time.Since(started)
	if e != nil || ref != observed {
		t.Fatal("batched ref/working bytes differ", e)
	}
	raw, e = os.ReadFile(trace)
	if e != nil {
		t.Fatal(e)
	}
	if n := strings.Count(string(raw), "built-in: git "); n != 3 {
		t.Fatalf("ref capture spawned %d Git processes", n)
	}
	t.Logf("%d files: Observe=%s (6 Git processes), durable Capture=%s (6), ref Capture=%s (3)", len(m.Files), observeTime, captureTime, refTime)
}
