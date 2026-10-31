package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"seai.local/seai/internal/checks"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/maintenance"
	"seai.local/seai/internal/providers/qwen"
	"seai.local/seai/internal/storage"
	"seai.local/seai/internal/workspace"
	"strings"
	"testing"
)

func executionTask(t *testing.T, s *Service) domain.Task {
	t.Helper()
	if e := s.Configure(""); e != nil {
		t.Fatal(e)
	}
	goexe, e := exec.LookPath("go")
	if e != nil {
		t.Fatal(e)
	}
	common := domain.CheckProfile{Program: goexe, Isolation: "local-approved", AcknowledgeLocal: true, TimeoutSeconds: 30, MaxOutputBytes: 65536}
	compile := common
	compile.ID = "compile"
	compile.Kind = "compile"
	compile.Args = []string{"build", "./..."}
	tests := common
	tests.ID = "tests"
	tests.Kind = "tests"
	tests.Args = []string{"test", "-json", "-count=1", "./..."}
	tests.TestNames = []string{"TestEligible"}
	compare := common
	compare.ID = "comparison"
	compare.Kind = "comparison"
	compare.Program = "/bin/sh"
	compare.Args = []string{"-c", `value=$(sed -n 's/^const MinimumAge = //p' rule.go); printf '{"minimum":%s}' "$value"`}
	compare.AssertionProfile = "boundary-v1"
	compare.Fixtures = []domain.Fixture{{ID: "minimum", Input: "", Expected: json.RawMessage(`{"minimum":21}`), Criteria: []string{"AC1"}, AllowedPaths: []string{"/minimum"}}}
	s.Profiles = map[string]domain.CheckProfile{"compile": compile, "tests": tests, "comparison": compare}
	s.Config.Checks = []domain.CheckProfile{compile, tests, compare}
	b, _ := json.Marshal(s.Config)
	s.ConfigDigest = domain.Digest(b)
	spec := sampleSpec()
	spec.Scope.Files = append(spec.Scope.Files, "rule_test.go")
	spec.Criteria[0].FixtureDigest = checks.FixtureDigest(compare)
	epoch, revision := 1, 0
	task := call(t, s, "task.create", Params{Operation: "execution-create", Epoch: &epoch, Revision: &revision, Spec: &spec}).(domain.Task)
	h := lookup(t, s, "MinimumAge")
	p := mutation("execution-plan", task)
	p.Plan = &domain.Plan{Schema: "seai.plan/v1", Steps: []domain.PlanStep{{ID: "step", Description: "change minimum", Files: []string{"rule.go", "rule_test.go"}, Criteria: []string{"AC1"}, Evidence: []string{h.Evidence}}}, Checks: []string{"compile", "tests", "comparison"}}
	task = call(t, s, "task.plan", p).(domain.Task)
	p = mutation("execution-approve", task)
	p.Actor = "test-human"
	p.Digest = task.BehaviorDigest
	v, e := s.ApproveHuman(context.Background(), p)
	if e != nil {
		t.Fatal(e)
	}
	return v.(domain.Task)
}
func waitRun(t *testing.T, s *Service, v any) map[string]any {
	t.Helper()
	s.Wait()
	id := v.(map[string]any)["run_id"].(string)
	return call(t, s, "runs.get", Params{ID: id}).(map[string]any)
}
func TestRegisteredChecksReviewInvalidationAndBackup(t *testing.T) {
	s := workflowFixture(t)
	task := executionTask(t, s)
	task = call(t, s, "patch.apply", executionPatch(t, s, task)).(domain.Task)
	p := mutation("verify", task)
	run := waitRun(t, s, call(t, s, "task.verify", p))
	if run["state"] != "SUCCEEDED" {
		t.Fatal(run)
	}
	logResult := call(t, s, "runs.logs", Params{ID: run["run_id"].(string), Profile: "tests"}).(map[string]any)
	check := logResult["result"].(domain.CheckResult)
	artifact := call(t, s, "runs.artifact", Params{ID: run["run_id"].(string), Artifact: check.StdoutDigest}).(map[string]any)
	if !strings.Contains(artifact["text"].(string), "TestEligible") {
		t.Fatal("test log missing")
	}
	if _, e := s.Handle(context.Background(), "runs.artifact", Params{Schema: domain.API, ID: run["run_id"].(string), Artifact: task.SpecDigest}); e == nil {
		t.Fatal("arbitrary CAS digest exposed")
	}
	task = call(t, s, "task.get", Params{Task: task.ID}).(domain.Task)
	if task.CheckSummary["checks_passed"] != true || task.Readiness != "NOT_REVIEW_READY" {
		t.Fatalf("%+v", task)
	}
	p = mutation("review", task)
	p.Actor = "independent-human"
	p.Digest = task.CheckSummary["review_digest"].(string)
	if _, e := s.DecideHuman(context.Background(), "human.review", p); e != nil {
		t.Fatal(e)
	}
	task = call(t, s, "task.get", Params{Task: task.ID}).(domain.Task)
	if task.Readiness != "REVIEW_READY" {
		t.Fatalf("%+v", task.CheckSummary)
	}
	p = mutation("ready-export", task)
	if _, e := s.Handle(context.Background(), "task.export", p); e != nil {
		t.Fatal(e)
	}
	// A new check execution invalidates the review bound to the previous log/result set.
	p = mutation("verify-again", task)
	run = waitRun(t, s, call(t, s, "task.verify", p))
	if run["state"] != "SUCCEEDED" {
		t.Fatal(run)
	}
	task = call(t, s, "task.get", Params{Task: task.ID}).(domain.Task)
	if task.Readiness == "REVIEW_READY" {
		t.Fatal("old review reused for a new check set")
	}
	epoch := 1
	run = waitRun(t, s, call(t, s, "backup.create", Params{Operation: "backup", Epoch: &epoch}))
	if run["state"] != "SUCCEEDED" {
		t.Fatal(run)
	}
	var result struct {
		Path string `json:"backup_path"`
	}
	b, _ := json.Marshal(run["result"])
	if e := json.Unmarshal(b, &result); e != nil {
		t.Fatal(e)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if _, e := maintenance.Restore(context.Background(), result.Path, restored); e != nil {
		t.Fatal(e)
	}
	r, e := storage.Open(restored)
	if e != nil {
		t.Fatal(e)
	}
	defer r.DB.Close()
	var candidate string
	if e = r.DB.QueryRow("SELECT candidate_id FROM tasks WHERE id=?", task.ID).Scan(&candidate); e != nil || candidate != task.Candidate {
		t.Fatal(candidate, e)
	}
	if _, e = maintenance.Restore(context.Background(), result.Path, restored); e == nil {
		t.Fatal("overwrote existing destination")
	}
	if e = os.WriteFile(filepath.Join(result.Path, "runtime.db"), []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = maintenance.Restore(context.Background(), result.Path, filepath.Join(t.TempDir(), "bad")); e == nil {
		t.Fatal("corrupt backup accepted")
	}
	// Untrusted program stdout must not be interpreted as a comparison log index.
	forged, _ := s.Workspace.Objects.Put([]byte(`[{"fixture_id":"forged","stdout_digest":"` + task.SpecDigest + `"}]`))
	synthetic, e := s.saveObject(domain.CheckResult{ProfileID: "compile", Status: "PASSED", StdoutDigest: forged})
	if e != nil {
		t.Fatal(e)
	}
	var checkRunID string
	if e = s.Store.DB.QueryRow("SELECT run_id FROM check_runs WHERE task_id=? AND profile_id='compile' ORDER BY rowid DESC LIMIT 1", task.ID).Scan(&checkRunID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Store.DB.Exec("UPDATE check_runs SET result_digest=? WHERE run_id=? AND profile_id='compile'", synthetic, checkRunID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Handle(context.Background(), "runs.artifact", Params{Schema: domain.API, ID: checkRunID, Artifact: task.SpecDigest}); e == nil {
		t.Fatal("forged stdout exposed arbitrary CAS")
	}
}
func TestReleaseRebaseConflictBarrier(t *testing.T) {
	s := workflowFixture(t)
	task := approvedTask(t, s)
	task = call(t, s, "patch.apply", patchRequest(t, s, task)).(domain.Task)
	root := s.Workspace.Root
	ctx := context.Background()
	content := strings.Replace(fixtureCode, "MinimumAge = 18", "MinimumAge = 19", 1)
	if e := os.WriteFile(filepath.Join(root, "rule.go"), []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
	for _, a := range [][]string{{"add", "."}, {"commit", "-qm", "release"}} {
		if _, e := workspace.Git(ctx, root, a...); e != nil {
			t.Fatal(e)
		}
	}
	epoch := 1
	run := waitRun(t, s, call(t, s, "release.plan", Params{Operation: "release-plan", Epoch: &epoch, Ref: "HEAD", ReleaseID: "v2"}))
	if run["state"] != "SUCCEEDED" {
		t.Fatal(run)
	}
	var plan domain.ReleasePlan
	b, _ := json.Marshal(run["result"])
	if e := json.Unmarshal(b, &plan); e != nil {
		t.Fatal(e)
	}
	run = waitRun(t, s, call(t, s, "release.apply", Params{Operation: "release-apply", Epoch: &epoch, ID: plan.ID}))
	if run["state"] != "SUCCEEDED" {
		t.Fatal(run)
	}
	task = call(t, s, "task.get", Params{Task: task.ID}).(domain.Task)
	if !task.Reconciliation || task.Freshness != "STALE" {
		t.Fatalf("%+v", task)
	}
	oldCandidate := task.Candidate
	epoch = 2
	p := mutation("rebase", task)
	p.Epoch = &epoch
	run = waitRun(t, s, call(t, s, "task.rebase", p))
	if run["state"] != "SUCCEEDED" {
		t.Fatal(run)
	}
	task = call(t, s, "task.get", Params{Task: task.ID}).(domain.Task)
	if task.Candidate != oldCandidate || !task.Reconciliation {
		t.Fatal("conflict destroyed task")
	}
	var result struct {
		Base      string            `json:"resolution_base_snapshot_id"`
		Conflicts []domain.Conflict `json:"conflicts"`
	}
	b, _ = json.Marshal(run["result"])
	if e := json.Unmarshal(b, &result); e != nil || len(result.Conflicts) != 1 {
		t.Fatal(string(b), e)
	}
	artifact := call(t, s, "runs.artifact", Params{ID: run["run_id"].(string), Artifact: result.Conflicts[0].MarkerDigest}).(map[string]any)
	if !strings.Contains(artifact["text"].(string), "<<<<<<<") {
		t.Fatal("conflict markers missing")
	}
	manifest, e := s.manifest(ctx, result.Base)
	if e != nil {
		t.Fatal(e)
	}
	var before, mode string
	for _, f := range manifest.Files {
		if f.Path == "rule.go" {
			before = f.Digest
			mode = f.Mode
		}
	}
	resolved := strings.Replace(fixtureCode, "MinimumAge = 18", "MinimumAge = 21", 1)
	after := domain.Digest([]byte(resolved))
	p = mutation("resolve", task)
	p.Epoch = &epoch
	p.Resolution = &domain.Patch{Schema: "seai.patch/v1", Base: result.Base, SpecDigest: task.SpecDigest, Files: []domain.FileOperation{{Path: "rule.go", Before: &before, BeforeMode: mode, AfterDigest: &after, Content: &resolved, Mode: mode}}}
	run = waitRun(t, s, call(t, s, "task.rebase", p))
	if run["state"] != "SUCCEEDED" {
		t.Fatal(run)
	}
	task = call(t, s, "task.get", Params{Task: task.ID}).(domain.Task)
	if task.Reconciliation || task.Epoch != 2 || task.Plan != nil || !task.Approved || task.RequirementsFreshness != "not_checked" {
		t.Fatalf("%+v", task)
	}
}
func TestWorkerDAGWithDeterministicProviderAndResume(t *testing.T) {
	s := workflowFixture(t)
	task := executionTask(t, s)
	patch := executionPatch(t, s, task).Patch
	h := lookup(t, s, "MinimumAge")
	failReview := true
	implementations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []qwen.Message `json:"messages"`
		}
		if e := json.NewDecoder(r.Body).Decode(&request); e != nil {
			t.Error(e)
		}
		message := qwen.Message{Role: "assistant"}
		if len(request.Messages) > 0 && request.Messages[0].Role == "user" {
			if len(request.Messages) == 1 {
				message.ToolCalls = []qwen.ToolCall{{ID: "probe", Type: "function", Function: qwen.Function{Name: "seai_probe", Arguments: `{"value":"ok"}`}}}
			} else {
				message.Content = "SEAI_OK"
			}
		} else {
			role := strings.TrimPrefix(strings.Split(request.Messages[0].Content, ".")[0], "You are SEAI role ")
			result := domain.WorkerResult{Role: role, Status: "DONE", Summary: "deterministic fixture", Evidence: []string{h.Evidence}, Gaps: []string{}}
			if role == "implementer" {
				implementations++
				result.Patch = patch
			}
			if role == "reviewer" {
				result.Verdict = "PASS"
				if failReview {
					result.Verdict = "FAIL"
				}
			}
			b, _ := json.Marshal(result)
			message.Content = string(b)
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "fixture", "choices": []any{map[string]any{"message": message}}})
	}))
	defer server.Close()
	provider, e := qwen.New(domain.ProviderProfile{Endpoint: server.URL, Model: "fixture", AllowLoopbackHTTP: true, ContextTokens: 32000, MaxOutputTokens: 4096, MaxCalls: 20, MaxTaskTokens: 200000, TimeoutSeconds: 10})
	if e != nil {
		t.Fatal(e)
	}
	s.Provider = provider
	if e = provider.Probe(context.Background()); e != nil {
		t.Fatal(e)
	}
	p := mutation("workers", task)
	run := waitRun(t, s, call(t, s, "task.run", p))
	if run["state"] != "FAILED" || implementations != 1 {
		t.Fatal(run, implementations)
	}
	firstID := run["run_id"].(string)
	task = call(t, s, "task.get", Params{Task: task.ID}).(domain.Task)
	if task.Candidate == task.Baseline {
		t.Fatal("candidate checkpoint missing")
	}
	failReview = false
	p = mutation("workers-resume", task)
	p.Resume = firstID
	run = waitRun(t, s, call(t, s, "task.run", p))
	if run["state"] != "SUCCEEDED" || implementations != 1 {
		t.Fatal(run, implementations)
	}
	task = call(t, s, "task.get", Params{Task: task.ID}).(domain.Task)
	if task.Readiness != "REVIEW_READY" {
		t.Fatalf("%+v", task.CheckSummary)
	}
	nodes := call(t, s, "agents.list", Params{ID: run["run_id"].(string)}).([]map[string]any)
	if len(nodes) != 5 {
		t.Fatal(nodes)
	}
}

func executionPatch(t *testing.T, s *Service, task domain.Task) Params {
	p := patchRequest(t, s, task)
	m, e := s.manifest(context.Background(), task.Candidate)
	if e != nil {
		t.Fatal(e)
	}
	content := "package fixture\nimport \"testing\"\nfunc TestEligible(t *testing.T) { for _,c:=range []struct{age int;want bool}{{20,false},{21,true},{22,true}} {if Eligible(c.age)!=c.want {t.Fatalf(\"boundary %d\",c.age)}} }\n"
	after := domain.Digest([]byte(content))
	for _, f := range m.Files {
		if f.Path == "rule_test.go" {
			before := f.Digest
			p.Patch.Files = append(p.Patch.Files, domain.FileOperation{Path: f.Path, Before: &before, BeforeMode: f.Mode, AfterDigest: &after, Content: &content, Mode: f.Mode})
		}
	}
	return p
}

func TestRunCancellationReplayAndRestartFencing(t *testing.T) {
	s := workflowFixture(t)
	task := approvedTask(t, s)
	if e := s.Configure(""); e != nil {
		t.Fatal(e)
	}
	s.Profiles["compile"] = domain.CheckProfile{ID: "compile", Kind: "compile", Program: "/bin/sh", Args: []string{"-c", "sleep 20"}, Isolation: "local-approved", AcknowledgeLocal: true, TimeoutSeconds: 30, MaxOutputBytes: 4096}
	p := mutation("cancellable", task)
	p.Profile = "compile"
	accepted := call(t, s, "checks.run", p)
	runID := accepted.(map[string]any)["run_id"].(string)
	replay := call(t, s, "checks.run", p)
	a, _ := json.Marshal(accepted)
	b, _ := json.Marshal(replay)
	if string(a) != string(b) {
		t.Fatal("acceptance replay changed")
	}
	changed := p
	changed.Profile = "other"
	if _, e := s.Handle(context.Background(), "checks.run", changed); e == nil {
		t.Fatal("conflicting idempotency key accepted")
	}
	if _, e := s.Handle(context.Background(), "patch.apply", patchRequest(t, s, task)); e == nil {
		t.Fatal("mutation entered active execution")
	}
	call(t, s, "runs.cancel", Params{ID: runID})
	s.Wait()
	run := call(t, s, "runs.get", Params{ID: runID}).(map[string]any)
	if run["state"] != "CANCELLED" {
		t.Fatal(run)
	}
	current := call(t, s, "task.get", Params{Task: task.ID}).(domain.Task)
	if current.Candidate != task.Candidate {
		t.Fatal("cancel changed candidate")
	}
	// Simulate a killed daemon with a durable worker lease, then reopen the database.
	if _, e := s.Store.DB.Exec("INSERT INTO runs(id,kind,task_id,state,fingerprint,task_revision,epoch,created_at) VALUES('dead','task.run',?,'RUNNING',?,?,?,?)", task.ID, s.fingerprint(task), task.Revision, task.Epoch, now()); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Store.DB.Exec("INSERT INTO execution_nodes(id,run_id,name,role,state,input_digest,attempt,lease_epoch) VALUES('late','dead','reviewer','reviewer','RUNNING','input',1,1)"); e != nil {
		t.Fatal(e)
	}
	s.Store.DB.Close()
	reopened, e := storage.Open(s.Data)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.DB.Close()
	var state string
	var lease int
	if e = reopened.DB.QueryRow("SELECT state,lease_epoch FROM runs WHERE id='dead'").Scan(&state, &lease); e != nil || state != "ABORTED" || lease != 2 {
		t.Fatal(state, lease, e)
	}
	result, e := reopened.DB.Exec("UPDATE execution_nodes SET state='SUCCEEDED' WHERE id='late' AND state='RUNNING' AND lease_epoch=1")
	if e != nil {
		t.Fatal(e)
	}
	n, _ := result.RowsAffected()
	if n != 0 {
		t.Fatal("late result crossed restart fence")
	}
}
