package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"seai.local/seai/internal/domain"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func researchFixture(t *testing.T, topics ...string) (*Service, Params, string) {
	t.Helper()
	s := workflowFixture(t)
	t.Cleanup(func() { s.CancelRuns(); s.Wait() })
	pin, e := s.resolve(context.Background(), "")
	if e != nil {
		t.Fatal(e)
	}
	s.FreshnessBudget = time.Nanosecond // session must never live-scan or require provider
	p := Params{Schema: domain.API, Operation: "research-start", Generation: pin.Generation, Epoch: &pin.Epoch, Research: &domain.ResearchRequest{Objective: "Explain eligibility and consumers", Topics: topics}}
	accepted := call(t, s, "research.start", p).(map[string]any)
	run := accepted["run_id"].(string)
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		v, e := s.researchStatus(context.Background(), Params{ID: run})
		if e != nil {
			t.Fatal(e)
		}
		if len(v.(map[string]any)["jobs"].([]map[string]any)) == len(topics) {
			return s, p, run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("research jobs not initialized")
	return nil, p, ""
}
func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	var m map[string]any
	if e = json.Unmarshal(b, &m); e != nil {
		t.Fatal(e)
	}
	return m
}
func researchError(t *testing.T, s *Service, method string, p Params, code string) {
	t.Helper()
	p.Schema = domain.API
	_, e := s.Handle(context.Background(), method, p)
	var de *domain.Error
	if !errors.As(e, &de) || de.Code != code {
		t.Fatalf("%s: got %v, want %s", method, e, code)
	}
}
func TestResearchConcurrentClientsPinnedReportsNoProvider(t *testing.T) {
	s, p, run := researchFixture(t, "rules", "impact", "tests")
	original, e := os.ReadFile(filepath.Join(s.Workspace.Root, "rule.go"))
	if e != nil {
		t.Fatal(e)
	}
	var claims [2]map[string]any
	var wg sync.WaitGroup
	var active, peak atomic.Int32
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	errs := make(chan error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := s.Handle(context.Background(), "research.claim", Params{Schema: domain.API, ID: run, Operation: []string{"claim-A", "claim-B"}[i]})
			if e != nil {
				errs <- e
				ready <- struct{}{}
				return
			}
			b, _ := json.Marshal(v)
			json.Unmarshal(b, &claims[i])
			n := active.Add(1)
			for {
				old := peak.Load()
				if n <= old || peak.CompareAndSwap(old, n) {
					break
				}
			}
			ready <- struct{}{}
			<-release
			active.Add(-1)
		}()
	}
	<-ready
	<-ready
	// Both clients hold independent jobs simultaneously; third cannot exceed bound.
	researchError(t, s, "research.claim", Params{ID: run, Operation: "claim-C"}, "RESEARCH_BUSY")
	close(release)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	if peak.Load() != 2 {
		t.Fatal("clients serialized", peak.Load())
	}
	if claims[0]["job_id"] == claims[1]["job_id"] {
		t.Fatal("duplicate job assignment")
	}
	// Request replay cannot allocate another job.
	replay := asMap(t, call(t, s, "research.claim", Params{ID: run, Operation: "claim-A"}))
	if replay["job_id"] != claims[0]["job_id"] {
		t.Fatal(replay, claims[0])
	}
	h := call(t, s, "search.entities", Params{Query: "MinimumAge", Generation: p.Generation}).(domain.SearchResult).Hits[0]
	report := &domain.ResearchReport{Summary: "Eligibility threshold", Findings: []domain.ResearchFinding{{Statement: "MinimumAge is declared in rule.go", Evidence: []string{h.Evidence}}}, Gaps: []string{}}
	for i, claim := range claims {
		input := claim["input"].(map[string]any)
		if input["index_generation_id"] != p.Generation {
			t.Fatal("pin drift", input)
		}
		file := call(t, s, "file.read", Params{Path: "rule.go", Generation: input["index_generation_id"].(string)}).(map[string]any)
		if file["text"] != string(original) {
			t.Fatal(file)
		}
		call(t, s, "research.report", Params{ID: run, ResearchJob: claim["job_id"].(string), Lease: int(claim["lease_epoch"].(float64)), Operation: []string{"report-A", "report-B"}[i], Report: report})
	}
	third := asMap(t, call(t, s, "research.claim", Params{ID: run, Operation: "claim-C"}))
	call(t, s, "research.report", Params{ID: run, ResearchJob: third["job_id"].(string), Lease: int(third["lease_epoch"].(float64)), Operation: "report-C", Report: report})
	result := waitRun(t, s, map[string]any{"run_id": run})
	s.Wait()
	if result["state"] != "SUCCEEDED" {
		t.Fatal(result)
	}
	final := asMap(t, result["result"])
	if final["index_generation_id"] != p.Generation || len(final["reports"].([]any)) != 3 || final["review_required"] != true {
		t.Fatal(final)
	}
	again := asMap(t, call(t, s, "research.start", p))
	if again["run_id"] != run {
		t.Fatal(again)
	}
	b, _ := os.ReadFile(filepath.Join(s.Workspace.Root, "rule.go"))
	if string(b) != string(original) {
		t.Fatal("research changed source")
	}
	var n int
	if e = s.Store.DB.QueryRow("SELECT count(*) FROM tasks").Scan(&n); e != nil || n != 0 {
		t.Fatal("research created change task", n, e)
	}
}
func TestResearchRejectsUnboundEvidenceAndReportsGaps(t *testing.T) {
	s, _, run := researchFixture(t, "rules")
	claim := asMap(t, call(t, s, "research.claim", Params{ID: run, Operation: "claim"}))
	p := Params{ID: run, ResearchJob: claim["job_id"].(string), Lease: int(claim["lease_epoch"].(float64)), Operation: "report", Report: &domain.ResearchReport{Summary: "unsupported claim", Findings: []domain.ResearchFinding{{Statement: "invented", Evidence: []string{strings.Repeat("0", 64)}}}, Gaps: []string{}}}
	researchError(t, s, "research.report", p, "EVIDENCE_NOT_FOUND")
	p.Lease--
	researchError(t, s, "research.report", p, "LEASE_LOST")
	p.Lease++
	p.Report = &domain.ResearchReport{Summary: "Need domain mapping", Findings: []domain.ResearchFinding{}, Gaps: []string{"Cannot resolve dynamic dispatch from static evidence"}}
	call(t, s, "research.report", p)
	result := waitResearchTerminal(t, s, run)
	s.Wait()
	if result["state"] != "NEEDS_INPUT" {
		t.Fatal(result)
	}
	// Exact retry remains valid after session completion; another write does not.
	call(t, s, "research.report", p)
	p.Operation = "late-report"
	researchError(t, s, "research.report", p, "INVALID_STATE")
}
func waitResearchTerminal(t *testing.T, s *Service, run string) map[string]any {
	t.Helper()
	for range 500 {
		r := call(t, s, "runs.get", Params{ID: run}).(map[string]any)
		if r["state"] != "RUNNING" {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("research wait timed out")
	return nil
}
func TestResearchCancellationFencesReportsAndQueuedJobs(t *testing.T) {
	s, _, run := researchFixture(t, "one", "two")
	claim := asMap(t, call(t, s, "research.claim", Params{ID: run, Operation: "claim"}))
	call(t, s, "research.cancel", Params{ID: run})
	s.Wait()
	status := call(t, s, "research.get", Params{ID: run}).(map[string]any)
	if status["state"] != "CANCELLED" {
		t.Fatal(status)
	}
	for _, job := range status["jobs"].([]map[string]any) {
		if job["state"] != "CANCELLED" {
			t.Fatal(job)
		}
	}
	researchError(t, s, "research.report", Params{ID: run, ResearchJob: claim["job_id"].(string), Lease: 2, Operation: "late"}, "INVALID_STATE")
}

func TestResearchConfigurationLimits(t *testing.T) {
	s := &Service{}
	for _, n := range []int{0, 1, 4, -1, 5} {
		raw, _ := json.Marshal(map[string]any{"schema_version": "seai.runtime/v1", "checks": []any{}, "max_workers": 2, "max_node_attempts": 1, "max_run_seconds": 1800, "research_max_workers": n})
		path := filepath.Join(t.TempDir(), "runtime.json")
		if e := os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
		e := s.Configure(path)
		if n < 0 || n > 4 {
			if e == nil {
				t.Fatal("unbounded research workers accepted", n)
			}
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		want := n
		if n == 0 {
			want = 2
		}
		if s.researchWorkers() != want {
			t.Fatal(n, s.researchWorkers())
		}
	}
}

func TestResearchPollingCursorAndCancellation(t *testing.T) {
	s, _, run := researchFixture(t, "rules")
	board := call(t, s, "research.get", Params{ID: run}).(map[string]any)
	// Wait until metadata initialization is complete before comparing stable cursors.
	for board["initializing"] == true {
		time.Sleep(time.Millisecond)
		board = call(t, s, "research.get", Params{ID: run}).(map[string]any)
	}
	cursor := board["cursor"].(string)
	unchanged := call(t, s, "research.get", Params{ID: run, Cursor: cursor, WaitMS: 1}).(map[string]any)
	if unchanged["unchanged"] != true || unchanged["jobs"] != nil {
		t.Fatal("idle poll duplicated reports", unchanged)
	}
	claim := asMap(t, call(t, s, "research.claim", Params{ID: run, Operation: "claim"}))
	changed := call(t, s, "research.get", Params{ID: run, Cursor: cursor, WaitMS: 1000}).(map[string]any)
	if changed["unchanged"] != false || changed["cursor"] == cursor {
		t.Fatal("claim did not wake poll", claim, changed)
	}
	c, cancel := context.WithCancel(context.Background())
	cancel()
	_, e := s.Handle(c, "research.get", Params{Schema: domain.API, ID: run, Cursor: changed["cursor"].(string), WaitMS: 10000})
	if !errors.Is(e, context.Canceled) {
		t.Fatal("poll ignored cancellation", e)
	}
}
