package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/indexer"
	"seai.local/seai/internal/storage"
	"seai.local/seai/internal/workspace"
	"testing"
)

func TestBuildSearchRestartAndIdempotency(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	data := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.invalid"}} {
		if _, e := workspace.Git(ctx, repo, args...); e != nil {
			t.Fatal(e)
		}
	}
	for p, b := range map[string]string{"go.mod": "module fixture\n\ngo 1.26.6\n", "rule.go": "package fixture\ntype Values[T any] = []T\nfunc Rule() int { return 1 }\nfunc RuleExtra() {}\n"} {
		if e := os.WriteFile(filepath.Join(repo, p), []byte(b), 0600); e != nil {
			t.Fatal(e)
		}
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "base"}} {
		if _, e := workspace.Git(ctx, repo, args...); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.MkdirAll(filepath.Join(data, "indexes"), 0700); e != nil {
		t.Fatal(e)
	}
	db, e := storage.Open(data)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { db.DB.Close() }()
	if e = db.Register("fixture", repo); e != nil {
		t.Fatal(e)
	}
	profile, e := indexer.BuildProfile(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if profile.Toolchain != runtime.Version() {
		t.Fatalf("indexer Go %s differs from test toolchain %s", profile.Toolchain, runtime.Version())
	}
	s := &Service{Store: db, Data: data, Workspace: workspace.Service{Root: repo, Project: "fixture", Objects: storage.CAS{Root: data}, Build: profile}}
	zero := 0
	p := Params{Schema: "seai.api/v1", Epoch: &zero, Operation: "build-one"}
	if _, e = s.Handle(ctx, "index.build", p); e != nil {
		t.Fatal(e)
	}
	s.Wait()
	j, e := s.Handle(ctx, "jobs.get", Params{Schema: p.Schema, Job: p.Operation})
	if e != nil || j.(Job).State != "SUCCEEDED" {
		t.Fatalf("job: %+v %v", j, e)
	}
	if _, e = s.Handle(ctx, "index.build", p); e != nil {
		t.Fatal("idempotent retry failed", e)
	}
	p.Ref = "HEAD"
	if _, e = s.Handle(ctx, "index.build", p); e == nil {
		t.Fatal("operation payload conflict accepted")
	}
	p.Operation = "old-epoch"
	if _, e = s.Handle(ctx, "index.build", p); e == nil {
		t.Fatal("old epoch accepted")
	}
	// Generic aliases exercise the upgraded AST/types backend against a Go 1.26 module.
	aliasResult, e := s.Handle(ctx, "search.entities", Params{Schema: p.Schema, Query: "Values"})
	if e != nil {
		t.Fatal(e)
	}
	alias := aliasResult.(domain.SearchResult)
	if len(alias.Hits) != 1 || alias.Hits[0].Kind != "type" || len(alias.Limitations) != len(indexer.Limitations) {
		t.Fatalf("generic alias analysis failed: %+v", alias)
	}
	// Live edits cannot alter the evidence returned by a published generation.
	if e = os.WriteFile(filepath.Join(repo, "rule.go"), []byte("invalid live source"), 0600); e != nil {
		t.Fatal(e)
	}
	result, e := s.Handle(ctx, "search.entities", Params{Schema: p.Schema, Query: "Rule", Limit: 1, Generation: alias.Generation})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(result)
	var search struct {
		Hits      []struct{ Name, Snippet string }
		Truncated bool
	}
	if e = json.Unmarshal(b, &search); e != nil {
		t.Fatal(e)
	}
	if len(search.Hits) != 1 || search.Hits[0].Name != "Rule" || search.Hits[0].Snippet != "func Rule() int { return 1 }" || !search.Truncated {
		t.Fatal(string(b))
	}
	db.DB.Close()
	db, e = storage.Open(data)
	if e != nil {
		t.Fatal(e)
	}
	s.Store = db
	if _, e = s.Handle(ctx, "search.entities", Params{Schema: p.Schema, Query: "Rule", Generation: alias.Generation}); e != nil {
		t.Fatal("search after restart", e)
	}
}
func TestRuntimeOutsideSource(t *testing.T) {
	repo := t.TempDir()
	if _, e := workspace.Git(context.Background(), repo, "init", "-q"); e != nil {
		t.Fatal(e)
	}
	if _, _, e := ValidateLocations(repo, filepath.Join(repo, "runtime")); e == nil {
		t.Fatal("runtime inside source accepted")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if e := os.Symlink(repo, alias); e != nil {
		t.Fatal(e)
	}
	if _, _, e := ValidateLocations(repo, filepath.Join(alias, "runtime")); e == nil {
		t.Fatal("symlink alias accepted")
	}
}
