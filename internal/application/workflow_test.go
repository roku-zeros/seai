package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/indexer"
	"seai.local/seai/internal/storage"
	"seai.local/seai/internal/workspace"
)

const fixtureCode = "package fixture\nconst MinimumAge = 18\ntype Checker interface { Eligible(int) bool }\nfunc Eligible(age int) bool { return age >= MinimumAge }\nfunc Explain(age int) bool { return Eligible(age) }\nfunc Dynamic(c Checker, age int) bool { return c.Eligible(age) }\nfunc Indirect(fn func(int) bool, age int) bool { return fn(age) }\n"

func workflowFixture(t *testing.T) *Service {
	t.Helper()
	ctx := context.Background()
	root, data := t.TempDir(), t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.invalid"}} {
		if _, e := workspace.Git(ctx, root, args...); e != nil {
			t.Fatal(e)
		}
	}
	for name, content := range map[string]string{"go.mod": "module fixture\n\ngo 1.26.6\n", "contest.go": "package fixture\nfunc TestPretend() bool { return Eligible(18) }\n", "rule.go": fixtureCode, "rule_test.go": "package fixture\nimport \"testing\"\nfunc TestEligible(t *testing.T) { if !Eligible(18) { t.Fatal(\"boundary\") } }\n"} {
		if e := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "baseline"}} {
		if _, e := workspace.Git(ctx, root, args...); e != nil {
			t.Fatal(e)
		}
	}
	r, e := storage.Open(data)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { r.DB.Close() })
	if e = r.Register("fixture", root); e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Join(data, "indexes"), 0700); e != nil {
		t.Fatal(e)
	}
	profile, e := indexer.BuildProfile(ctx)
	if e != nil {
		t.Fatal(e)
	}
	s := &Service{Store: r, Data: data, Workspace: workspace.Service{Root: root, Project: "fixture", Objects: storage.CAS{Root: data}, Build: profile}}
	epoch := 0
	if _, e = s.Handle(ctx, "index.build", Params{Schema: domain.API, Operation: "initial", Epoch: &epoch}); e != nil {
		t.Fatal(e)
	}
	s.Wait()
	j, e := s.Handle(ctx, "jobs.get", Params{Schema: domain.API, Job: "initial"})
	if e != nil || j.(Job).State != "SUCCEEDED" {
		t.Fatalf("index %+v %v", j, e)
	}
	return s
}
func call(t *testing.T, s *Service, method string, p Params) any {
	t.Helper()
	p.Schema = domain.API
	v, e := s.Handle(context.Background(), method, p)
	if e != nil {
		t.Fatalf("%s: %v", method, e)
	}
	return v
}
func lookup(t *testing.T, s *Service, name string) domain.Hit {
	t.Helper()
	v := call(t, s, "search.entities", Params{Query: name}).(domain.SearchResult)
	for _, h := range v.Hits {
		if h.Name == name {
			return h
		}
	}
	t.Fatal("no symbol", name)
	return domain.Hit{}
}
func mutation(op string, task domain.Task) Params {
	revision, epoch := task.Revision, task.Epoch
	return Params{Schema: domain.API, Task: task.ID, Operation: op, Revision: &revision, Epoch: &epoch}
}
func sampleSpec() domain.ChangeSpec {
	content := "Minimum age must be 21; preserve reason semantics."
	return domain.ChangeSpec{Schema: "seai.change/v1", Alias: "CHG-1", Revision: 1, Kind: "change", Objective: "Raise minimum age to 21", Requirements: []domain.Requirement{{ID: "REQ-1", Revision: "1", Digest: domain.Digest([]byte(content)), Content: content}}, Scope: domain.Scope{Files: []string{"rule.go"}}, Expected: []domain.Behavior{{ID: "B1", Statement: "Minimum is 21"}}, Preserved: []domain.Behavior{{ID: "I1", Statement: "Eligibility uses inclusive threshold"}}, Criteria: []domain.Criterion{{ID: "AC1", BehaviorIDs: []string{"B1", "I1"}, Expected: "20 false; 21 true; 22 true", Profile: "boundary-v1"}}, Unresolved: []string{}}
}
func newTask(t *testing.T, s *Service) domain.Task {
	t.Helper()
	epoch, revision := 1, 0
	spec := sampleSpec()
	return call(t, s, "task.create", Params{Operation: "create", Epoch: &epoch, Revision: &revision, Spec: &spec}).(domain.Task)
}
func plannedTask(t *testing.T, s *Service) domain.Task {
	t.Helper()
	task := newTask(t, s)
	h := lookup(t, s, "MinimumAge")
	p := mutation("plan", task)
	p.Plan = &domain.Plan{Schema: "seai.plan/v1", Steps: []domain.PlanStep{{ID: "S1", Description: "Update threshold", Files: []string{"rule.go"}, Criteria: []string{"AC1"}, Evidence: []string{h.Evidence}}}, Checks: []string{"compile", "targeted", "regression", "version-comparison"}}
	return call(t, s, "task.plan", p).(domain.Task)
}
func approvedTask(t *testing.T, s *Service) domain.Task {
	t.Helper()
	task := plannedTask(t, s)
	p := mutation("approve", task)
	p.Actor = "test-human-driver"
	p.Digest = task.BehaviorDigest
	value, e := s.ApproveHuman(context.Background(), p)
	if e != nil {
		t.Fatal(e)
	}
	return value.(domain.Task)
}
func patchRequest(t *testing.T, s *Service, task domain.Task) Params {
	t.Helper()
	m, e := s.manifest(context.Background(), task.Candidate)
	if e != nil {
		t.Fatal(e)
	}
	var before, mode string
	for _, f := range m.Files {
		if f.Path == "rule.go" {
			before, mode = f.Digest, f.Mode
		}
	}
	content := strings.Replace(fixtureCode, "MinimumAge = 18", "MinimumAge = 21", 1)
	after := domain.Digest([]byte(content))
	p := mutation("patch", task)
	p.Patch = &domain.Patch{Schema: "seai.patch/v1", Base: task.Candidate, SpecDigest: task.SpecDigest, Files: []domain.FileOperation{{Path: "rule.go", Before: &before, BeforeMode: mode, AfterDigest: &after, Content: &content, Mode: "100644"}}}
	return p
}
func TestResearchSnapshotGraphAndFreshness(t *testing.T) {
	s := workflowFixture(t)
	h := lookup(t, s, "Eligible")
	result := call(t, s, "references.find", Params{Symbol: h.ID}).(map[string]any)
	refs := result["references"].([]domain.Reference)
	calls := 0
	for _, r := range refs {
		if r.Kind == "CALLS" && r.Certainty == "exact" {
			calls++
		}
	}
	if calls != 3 {
		t.Fatalf("expected 3 exact callers: %+v", refs)
	}
	related := call(t, s, "tests.related", Params{Symbol: h.ID}).(map[string]any)
	if len(related["references"].([]domain.Reference)) != 1 {
		t.Fatal(related)
	}
	dynamic := lookup(t, s, "Dynamic")
	out := call(t, s, "calls.list", Params{Symbol: dynamic.ID}).(map[string]any)["references"].([]domain.Reference)
	if len(out) != 1 || out[0].Certainty != "possible" {
		t.Fatal(out)
	}
	indirect := lookup(t, s, "Indirect")
	out = call(t, s, "calls.list", Params{Symbol: indirect.ID}).(map[string]any)["references"].([]domain.Reference)
	if len(out) != 1 || out[0].Certainty != "unresolved" {
		t.Fatal(out)
	}
	pin, _ := s.resolve(context.Background(), "")
	file := call(t, s, "file.read", Params{Path: "rule.go", Limit: 20}).(map[string]any)
	if !file["truncated"].(bool) || file["text"] != fixtureCode[:20] {
		t.Fatal(file)
	}
	symbol := call(t, s, "symbol.get", Params{Symbol: h.ID, Limit: 65536}).(map[string]any)
	if !strings.Contains(symbol["text"].(string), "age >= MinimumAge") {
		t.Fatal(symbol)
	}
	text := call(t, s, "search.text", Params{Query: "Eligible", Limit: 1}).(map[string]any)
	cursor := text["next_cursor"].(string)
	if cursor == "" {
		t.Fatal(text)
	}
	if _, e := s.Handle(context.Background(), "search.text", Params{Schema: domain.API, Query: "other", Cursor: cursor}); e == nil {
		t.Fatal("cursor query mismatch accepted")
	}
	if e := os.WriteFile(filepath.Join(s.Workspace.Root, "rule.go"), []byte("live invalid source"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Handle(context.Background(), "file.read", Params{Schema: domain.API, Path: "rule.go"}); e == nil {
		t.Fatal("stale alias read accepted")
	}
	historical := call(t, s, "file.read", Params{Path: "rule.go", Generation: pin.Generation, Limit: 65536}).(map[string]any)
	if historical["text"] != fixtureCode {
		t.Fatal("historical read used live bytes")
	}
	if _, e := s.Handle(context.Background(), "file.read", Params{Schema: domain.API, Path: "../outside", Generation: pin.Generation}); e == nil {
		t.Fatal("traversal accepted")
	}
}
func TestTaskApprovalPatchExportAndRecovery(t *testing.T) {
	s := workflowFixture(t)
	task := plannedTask(t, s)
	p := patchRequest(t, s, task)
	if _, e := s.Handle(context.Background(), "patch.apply", p); e == nil {
		t.Fatal("unapproved change applied")
	}
	if _, e := s.Handle(context.Background(), "human.approve", mutation("model-approve", task)); e == nil {
		t.Fatal("approval exposed on model dispatcher")
	}
	approve := mutation("human", task)
	approve.Actor = "test-driver"
	approve.Digest = task.BehaviorDigest
	v, e := s.ApproveHuman(context.Background(), approve)
	if e != nil {
		t.Fatal(e)
	}
	task = v.(domain.Task)
	p = patchRequest(t, s, task)
	bad := p
	copyPatch := *p.Patch
	copyPatch.Files = append([]domain.FileOperation{}, p.Patch.Files...)
	wrong := strings.Repeat("0", 64)
	copyPatch.Files[0].Before = &wrong
	bad.Patch = &copyPatch
	bad.Operation = "wrong-before"
	if _, e = s.Handle(context.Background(), "patch.apply", bad); e == nil {
		t.Fatal("wrong before hash accepted")
	}
	task = call(t, s, "patch.apply", p).(domain.Task)
	if task.Candidate == task.Baseline || task.State != "IMPLEMENTING" {
		t.Fatal(task)
	}
	repeated := call(t, s, "patch.apply", p)
	a, _ := json.Marshal(task)
	b, _ := json.Marshal(repeated)
	if string(a) != string(b) {
		t.Fatal("retry changed published result")
	}
	p.Patch.Files[0].Mode = "100755"
	if _, e = s.Handle(context.Background(), "patch.apply", p); e == nil {
		t.Fatal("operation payload conflict accepted")
	}
	live, e := os.ReadFile(filepath.Join(s.Workspace.Root, "rule.go"))
	if e != nil || string(live) != fixtureCode {
		t.Fatal("user source modified", e)
	}
	candidate := call(t, s, "file.read", Params{Path: "rule.go", Snapshot: task.Candidate, Limit: 65536}).(map[string]any)
	if !strings.Contains(candidate["text"].(string), "MinimumAge = 21") {
		t.Fatal(candidate)
	}
	export := mutation("export", task)
	if _, e = s.Handle(context.Background(), "task.export", export); e == nil {
		t.Fatal("ready export accepted without checks")
	}
	export.AllowIncomplete = true
	result := call(t, s, "task.export", export).(map[string]any)
	if result["release_readiness"] != "NOT_REVIEW_READY" || !strings.Contains(result["patch"].(string), "+const MinimumAge = 21") {
		t.Fatal(result)
	}
	// Restart runtime; accepted specs/plans/candidates and the operation result survive.
	s.Store.DB.Close()
	store, e := storage.Open(s.Data)
	if e != nil {
		t.Fatal(e)
	}
	defer store.DB.Close()
	s.Store = store
	restored := call(t, s, "task.get", Params{Task: task.ID}).(domain.Task)
	if restored.Candidate != task.Candidate || restored.PlanDigest == "" || !restored.Approved {
		t.Fatal(restored)
	}
	cancel := mutation("cancel", restored)
	cancelled := call(t, s, "task.cancel", cancel).(domain.Task)
	resumed := call(t, s, "task.resume", mutation("resume", cancelled)).(domain.Task)
	if resumed.State != "IMPLEMENTING" || resumed.Candidate != task.Candidate {
		t.Fatal(resumed)
	}
}
func TestSpecRevisionInvalidatesApprovalAndPlan(t *testing.T) {
	s := workflowFixture(t)
	task := approvedTask(t, s)
	spec := task.Spec
	spec.Revision = 2
	spec.Requirements = append([]domain.Requirement{}, spec.Requirements...)
	spec.Requirements[0].Revision = "2"
	p := mutation("revise", task)
	p.Spec = &spec
	task = call(t, s, "task.revise", p).(domain.Task)
	if task.Approved || task.PlanDigest != "" || task.State != "SPEC_READY" {
		t.Fatal(task)
	}
	var count int
	if e := s.Store.DB.QueryRow("SELECT count(*) FROM task_specs WHERE task_id=?", task.ID).Scan(&count); e != nil || count != 2 {
		t.Fatal(count, e)
	}
}
func TestCrossMethodOperationsAndStableReindex(t *testing.T) {
	s := workflowFixture(t)
	task := plannedTask(t, s)
	epoch := 1
	if _, e := s.Handle(context.Background(), "index.build", Params{Schema: domain.API, Operation: "plan", Epoch: &epoch}); e == nil {
		t.Fatal("operation namespace not shared")
	}
	spec := sampleSpec()
	spec.Alias = "OTHER"
	zero := 0
	if _, e := s.Handle(context.Background(), "task.create", Params{Schema: domain.API, Spec: &spec, Operation: "initial", Epoch: &epoch, Revision: &zero}); e == nil {
		t.Fatal("index operation ID reused for task")
	}
	if _, e := s.Handle(context.Background(), "index.build", Params{Schema: domain.API, Operation: "same-generation", Epoch: &epoch}); e != nil {
		t.Fatal(e)
	}
	s.Wait()
	pin, e := s.resolve(context.Background(), "")
	if e != nil || pin.Epoch != 1 {
		t.Fatal("same generation invalidated task epoch", pin, e)
	}
	recovered := call(t, s, "task.get", Params{Task: task.ID}).(domain.Task)
	if recovered.Freshness != "CURRENT" {
		t.Fatal(recovered)
	}
}
