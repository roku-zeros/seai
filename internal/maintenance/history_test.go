package maintenance

import (
	"context"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/storage"
	"seai.local/seai/internal/workspace"
	"testing"
)

func TestNewCapturePolicyPreservesHistoricalMetadataBackups(t *testing.T) {
	data := t.TempDir()
	store, e := storage.Open(data)
	if e != nil {
		t.Fatal(e)
	}
	defer store.DB.Close()
	if e = store.Register("legacy", "/legacy/repo"); e != nil {
		t.Fatal(e)
	}
	cas := storage.CAS{Root: data}
	blob, e := cas.Put([]byte("historical regular metadata"))
	if e != nil {
		t.Fatal(e)
	}
	manifest := domain.Manifest{Schema: "seai.source/v1", Repository: "legacy", Build: domain.Build{Tags: []string{}}, Files: []domain.File{{Path: "AGENTS.md", Mode: "100644", Digest: blob}}}
	raw, e := domain.Canonical(manifest)
	if e != nil {
		t.Fatal(e)
	}
	id, e := cas.Put(raw)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.DB.Exec("INSERT INTO source_snapshots VALUES(?,?)", id, id); e != nil {
		t.Fatal(e)
	}
	if workspace.ValidSourcePath("AGENTS.md") || !workspace.ValidStoredSourcePath("AGENTS.md") {
		t.Fatal("new and historical capture policies not separated")
	}
	if e = CheckFiles(context.Background(), data, store.DB); e != nil {
		t.Fatal("old history became corrupt after policy update", e)
	}
	for _, path := range []string{"../outside", ".git/hooks/x", "nested/.seai/key", ".env"} {
		if workspace.ValidStoredSourcePath(path) {
			t.Fatal("historical validator weakened", path)
		}
	}
}
