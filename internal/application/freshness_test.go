package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"seai.local/seai/internal/domain"
	"testing"
	"time"
)

func TestFreshnessTimeoutIsUnknownAndAsyncCheckDoesNotPublish(t *testing.T) {
	s := workflowFixture(t)
	task := plannedTask(t, s)
	ctx := context.Background()
	s.FreshnessBudget = time.Nanosecond
	_, e := s.Handle(ctx, "workspace.status", Params{Schema: domain.API})
	var de *domain.Error
	if !errors.As(e, &de) || de.Code != "FRESHNESS_TIMEOUT" || de.SuggestedAction == "" {
		t.Fatal("missing actionable freshness timeout", e)
	}
	current := call(t, s, "task.get", Params{Task: task.ID}).(domain.Task)
	if current.Freshness != "UNKNOWN" || current.FreshnessError == nil || current.FreshnessError.Code != "FRESHNESS_TIMEOUT" || current.Readiness == "REVIEW_READY" {
		t.Fatalf("timeout was treated as proof: %+v", current)
	}
	before, e := s.resolve(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	epoch := before.Epoch
	run := waitRun(t, s, call(t, s, "workspace.check", Params{Operation: "async-freshness", Epoch: &epoch}))
	if run["state"] != "SUCCEEDED" {
		t.Fatal(run)
	}
	after, e := s.resolve(ctx, "")
	if e != nil || before != after {
		t.Fatal("async check moved the published snapshot", e)
	}
	// A new check must not reuse stale proof; actual bytes are re-read.
	if e = os.WriteFile(filepath.Join(s.Workspace.Root, "rule.go"), []byte("package fixture\nconst Changed=true\n"), 0600); e != nil {
		t.Fatal(e)
	}
	run = waitRun(t, s, call(t, s, "workspace.check", Params{Operation: "async-changed", Epoch: &epoch}))
	if run["state"] != "SUCCEEDED" {
		t.Fatal(run)
	}
	b, e := json.Marshal(run["result"])
	if e != nil {
		t.Fatal(e)
	}
	var observed struct {
		Freshness string `json:"freshness"`
	}
	if json.Unmarshal(b, &observed) != nil || observed.Freshness != "STALE" {
		t.Fatal("changed workspace marked current", string(b))
	}
}
