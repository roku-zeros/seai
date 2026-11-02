package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"seai.local/seai/internal/domain"
	"strings"
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

func TestStaticRejectionAndStoredStatusDoNotRequireFreshness(t *testing.T) {
	s := workflowFixture(t)
	task := plannedTask(t, s)
	s.FreshnessBudget = time.Nanosecond
	spec := sampleSpec()
	spec.Unresolved = nil
	p := mutation("bad-static-spec", task)
	p.Spec = &spec
	_, e := s.Handle(context.Background(), "task.revise", p)
	var de *domain.Error
	if !errors.As(e, &de) || de.Code != "SCHEMA_INVALID" {
		t.Fatal("invalid spec scanned workspace", e)
	}
	p = mutation("bad-static-plan", task)
	plan := *task.Plan
	plan.Steps = append([]domain.PlanStep{}, task.Plan.Steps...)
	plan.Steps[0].Evidence = []string{strings.Repeat("0", 64)}
	p.Plan = &plan
	_, e = s.Handle(context.Background(), "task.plan", p)
	if !errors.As(e, &de) || de.Code != "EVIDENCE_NOT_FOUND" {
		t.Fatal("invalid evidence scanned workspace", e)
	}
	stored := call(t, s, "task.get", Params{Task: task.ID, Stored: true}).(domain.Task)
	if stored.Freshness != "UNKNOWN" || stored.FreshnessError.Code != "FRESHNESS_NOT_CHECKED" || stored.Readiness == "REVIEW_READY" {
		t.Fatal(stored)
	}
}

func TestAsyncWorkflowReceiptSuccessReplayAndFailure(t *testing.T) {
	s := workflowFixture(t)
	if e := s.Configure(""); e != nil {
		t.Fatal(e)
	}
	spec := sampleSpec()
	epoch, revision := 1, 0
	p := Params{Schema: domain.API, Operation: "async-create", Epoch: &epoch, Revision: &revision, Spec: &spec, WorkflowMethod: "task.create"}
	accepted := call(t, s, "workflow.submit", p)
	run := waitRun(t, s, accepted)
	if run["state"] != "SUCCEEDED" {
		t.Fatal(run)
	}
	again := call(t, s, "workflow.submit", p)
	b, _ := json.Marshal(accepted)
	c, _ := json.Marshal(again)
	if string(b) != string(c) {
		t.Fatal("idempotency lost", accepted, again)
	}
	raw, _ := json.Marshal(run["result"])
	var created domain.Task
	if e := json.Unmarshal(raw, &created); e != nil || created.ID == "" {
		t.Fatal(string(raw), e)
	}
	s.FreshnessBudget = time.Nanosecond
	spec.Alias = "other-change"
	p.Spec = &spec
	p.Operation = "async-timeout"
	run = waitRun(t, s, call(t, s, "workflow.submit", p))
	if run["state"] != "FAILED" || !strings.Contains(run["error"].(string), "FRESHNESS_TIMEOUT") {
		t.Fatal(run)
	}
	var count int
	if e := s.Store.DB.QueryRow("SELECT count(*) FROM tasks").Scan(&count); e != nil || count != 1 {
		t.Fatal("failed async operation committed task", count, e)
	}
}

func TestFreshnessBudgetConfigurationBounds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.json")
	s := &Service{}
	for _, budget := range []int{300, 1800, 0} {
		b, _ := json.Marshal(map[string]any{"schema_version": "seai.runtime/v1", "max_workers": 2, "max_node_attempts": 1, "max_run_seconds": 1800, "checks": []any{}, "freshness_budget_seconds": budget})
		if e := os.WriteFile(path, b, 0600); e != nil {
			t.Fatal(e)
		}
		if e := s.Configure(path); e != nil {
			t.Fatal(e)
		}
		expected := DefaultFreshnessBudget
		if budget > 0 {
			expected = time.Duration(budget) * time.Second
		}
		if s.FreshnessBudget != expected {
			t.Fatal(s.FreshnessBudget, expected)
		}
	}
	if e := os.WriteFile(path, []byte(`{"schema_version":"seai.runtime/v1","max_workers":2,"max_node_attempts":1,"max_run_seconds":1800,"checks":[],"freshness_budget_seconds":1801}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := s.Configure(path); e == nil {
		t.Fatal("unbounded freshness accepted")
	}
}
