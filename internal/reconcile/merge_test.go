package reconcile

import (
	"context"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/storage"
	"testing"
)

func TestThreeWayTextModeAndDeleteConflicts(t *testing.T) {
	cas := storage.CAS{Root: t.TempDir()}
	manifest := func(text, mode string) domain.Manifest {
		d, e := cas.Put([]byte(text))
		if e != nil {
			t.Fatal(e)
		}
		return domain.Manifest{Files: []domain.File{{Path: "a.go", Mode: mode, Digest: d}}}
	}
	b := manifest("a\nb\nc\nd\ne\n", "100644")
	o := manifest("ours\nb\nc\nd\ne\n", "100644")
	n := manifest("a\nb\nc\nd\ntheirs\n", "100644")
	m, c, e := Merge(context.Background(), b, o, n, cas, t.TempDir())
	if e != nil || len(c) != 0 {
		t.Fatal(c, e)
	}
	data, _ := cas.Get(m.Files[0].Digest)
	if string(data) != "ours\nb\nc\nd\ntheirs\n" {
		t.Fatal(string(data))
	}
	n = manifest("other\nb\nc\nd\ne\n", "100644")
	_, c, e = Merge(context.Background(), b, o, n, cas, t.TempDir())
	if e != nil || len(c) != 1 || c[0].MarkerDigest == "" {
		t.Fatal(c, e)
	}
	n = manifest("new\n", "100755")
	_, c, e = Merge(context.Background(), b, o, n, cas, t.TempDir())
	if e != nil || len(c) != 1 || c[0].Kind != "mode" {
		t.Fatal(c, e)
	}
	n.Files = nil
	_, c, e = Merge(context.Background(), b, o, n, cas, t.TempDir())
	if e != nil || len(c) != 1 || c[0].Kind != "add-delete-modify" {
		t.Fatal(c, e)
	}
}
