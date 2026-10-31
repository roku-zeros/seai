package patches

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/storage"
	"seai.local/seai/internal/workspace"
	"testing"
)

func TestBinaryModesAndGitPatchRoundtrip(t *testing.T) {
	cas := storage.CAS{Root: t.TempDir()}
	old := []byte{'a', 0, 'b', '\r', '\n'}
	id, e := cas.Put(old)
	if e != nil {
		t.Fatal(e)
	}
	base := domain.Manifest{Schema: "seai.source/v1", Repository: "test", Files: []domain.File{{Path: "binary.dat", Mode: "100644", Digest: id}}}
	raw, _ := domain.Canonical(base)
	baseID, _ := cas.Put(raw)
	binary := "eAB5AA=="
	text := "#!/bin/sh\nexit 0\n"
	rawBinary, _ := base64.StdEncoding.DecodeString(binary)
	binaryDigest := domain.Digest(rawBinary)
	textDigest := domain.Digest([]byte(text))
	patch := domain.Patch{Schema: "seai.patch/v1", Base: baseID, SpecDigest: "spec", Files: []domain.FileOperation{{Path: "binary.dat", Before: &id, BeforeMode: "100644", Base64: &binary, AfterDigest: &binaryDigest, Mode: "100644"}, {Path: "dir/file with spaces.sh", Content: &text, AfterDigest: &textDigest, Mode: "100755"}}}
	candidate, _, e := Apply(base, baseID, "spec", patch, []string{"binary.dat", "dir/file with spaces.sh"}, cas)
	if e != nil {
		t.Fatal(e)
	}
	diff, _, e := Diff(context.Background(), base, candidate, cas)
	if e != nil {
		t.Fatal(e)
	}
	repo := t.TempDir()
	if e = workspace.Materialize(base, cas, repo); e != nil {
		t.Fatal(e)
	}
	f := filepath.Join(t.TempDir(), "change.patch")
	if e = os.WriteFile(f, []byte(diff), 0600); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command("git", "apply", "--binary", f)
	cmd.Dir = repo
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("git apply: %s %v\n%s", out, e, diff)
	}
	for _, file := range candidate.Files {
		b, e := os.ReadFile(filepath.Join(repo, file.Path))
		if e != nil || domain.Digest(b) != file.Digest {
			t.Fatal("roundtrip differs", file, e)
		}
		info, e := os.Stat(filepath.Join(repo, file.Path))
		if e != nil {
			t.Fatal(e)
		}
		if file.Mode == "100755" && info.Mode()&0111 == 0 {
			t.Fatal("executable mode lost")
		}
	}
}
func TestAtomicPreconditionsAndPathGuards(t *testing.T) {
	cas := storage.CAS{Root: t.TempDir()}
	id, _ := cas.Put([]byte("old"))
	base := domain.Manifest{Repository: "test", Files: []domain.File{{Path: "a", Mode: "100644", Digest: id}}}
	raw, _ := domain.Canonical(base)
	baseID, _ := cas.Put(raw)
	text := "new"
	wrong := "wrong"
	patch := domain.Patch{Schema: "seai.patch/v1", Base: baseID, SpecDigest: "spec", Files: []domain.FileOperation{{Path: "a", Before: &wrong, BeforeMode: "100644", Content: &text, Mode: "100644"}}}
	if _, _, e := Apply(base, baseID, "spec", patch, []string{"a"}, cas); e == nil {
		t.Fatal("wrong hash accepted")
	}
	for _, path := range []string{"../escape", "/absolute", "a/../b", ".git/config"} {
		patch.Files = []domain.FileOperation{{Path: path, Content: &text, Mode: "100644"}}
		if _, _, e := Apply(base, baseID, "spec", patch, []string{path}, cas); e == nil {
			t.Fatal("bad path", path)
		}
	}
	b, e := cas.Get(id)
	if e != nil || string(b) != "old" {
		t.Fatal("base corrupted")
	}
}
func TestDeleteAndAfterHashFailure(t *testing.T) {
	cas := storage.CAS{Root: t.TempDir()}
	old, _ := cas.Put([]byte("old"))
	base := domain.Manifest{Repository: "test", Files: []domain.File{{Path: "gone.go", Mode: "100644", Digest: old}}}
	raw, _ := domain.Canonical(base)
	baseID, _ := cas.Put(raw)
	p := domain.Patch{Schema: "seai.patch/v1", Base: baseID, SpecDigest: "s", Files: []domain.FileOperation{{Path: "gone.go", Before: &old, BeforeMode: "100644", Delete: true}}}
	after, _, e := Apply(base, baseID, "s", p, []string{"gone.go"}, cas)
	if e != nil || len(after.Files) != 0 {
		t.Fatal(after, e)
	}
	text := "new"
	bad := "incorrect"
	p.Files = []domain.FileOperation{{Path: "gone.go", Before: &old, BeforeMode: "100644", Content: &text, AfterDigest: &bad, Mode: "100644"}}
	if _, _, e = Apply(base, baseID, "s", p, []string{"gone.go"}, cas); e == nil {
		t.Fatal("after hash mismatch accepted")
	}
}
func TestDiffOnlyCreateAndDelete(t *testing.T) {
	cas := storage.CAS{Root: t.TempDir()}
	id, _ := cas.Put([]byte("line\r\n"))
	empty := domain.Manifest{Files: []domain.File{}}
	full := domain.Manifest{Files: []domain.File{{Path: "only.txt", Mode: "100644", Digest: id}}}
	for _, pair := range [][2]domain.Manifest{{empty, full}, {full, empty}} {
		diff, paths, e := Diff(context.Background(), pair[0], pair[1], cas)
		if e != nil || diff == "" || len(paths) != 1 {
			t.Fatal(diff, paths, e)
		}
	}
}
