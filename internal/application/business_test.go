package application

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"seai.local/seai/internal/business"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/indexer"
	"seai.local/seai/internal/storage"
	"seai.local/seai/internal/workspace"
	"strings"
	"testing"
)

func businessFixture(t *testing.T) *Service {
	t.Helper()
	root, data := t.TempDir(), t.TempDir()
	source := "../../fixtures/business"
	if e := filepath.WalkDir(source, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(source, path)
		if e != nil {
			return e
		}
		target := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		return os.WriteFile(target, b, 0600)
	}); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.invalid"}, {"add", "."}, {"commit", "-qm", "business fixture"}} {
		if _, e := workspace.Git(ctx, root, args...); e != nil {
			t.Fatal(e)
		}
	}
	store, e := storage.Open(data)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.DB.Close() })
	if e = store.Register("business", root); e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Join(data, "indexes"), 0700); e != nil {
		t.Fatal(e)
	}
	build, e := indexer.BuildProfile(ctx)
	if e != nil {
		t.Fatal(e)
	}
	s := &Service{Store: store, Data: data, Workspace: workspace.Service{Root: root, Project: "business", Objects: storage.CAS{Root: data}, Build: build}}
	if e = s.Configure(""); e != nil {
		t.Fatal(e)
	}
	s.Config.Business = &domain.BusinessConfig{Schema: "seai.business/v1", RequireForChanges: true, Adapters: []domain.RuleAdapter{{Name: "rules", Kind: "struct", Package: "seai.fixture/business", Symbol: "Rule", IDField: "ID", Handlers: map[string]string{"condition": "When", "exception": "Except", "action": "Apply"}, ContextFields: []string{"Product"}, PriorityField: "Priority"}}}
	epoch := 0
	call(t, s, "index.build", Params{Epoch: &epoch, Operation: "business-index"})
	s.Wait()
	job := call(t, s, "jobs.get", Params{Job: "business-index"}).(Job)
	if job.State != "SUCCEEDED" {
		t.Fatal(job)
	}
	return s
}
func TestBusinessRulesEvidenceSharedImpactAndFlow(t *testing.T) {
	s := businessFixture(t)
	ctx := context.Background()
	list := call(t, s, "rules.search", Params{Query: "AGE_MIN"}).(map[string]any)
	rules := list["rules"].([]domain.BusinessRule)
	if len(rules) != 2 {
		t.Fatalf("%+v", list)
	}
	if _, e := s.Handle(ctx, "rules.get", Params{Schema: domain.API, Query: "AGE_MIN"}); e == nil {
		t.Fatal("duplicate business IDs silently collapsed")
	}
	detail := call(t, s, "rules.get", Params{Query: "AGE_MIN", Context: map[string]string{"Product": "retail"}}).(map[string]any)
	r := detail["rule"].(domain.BusinessRule)
	if r.Priority != "10" || r.Certainty != "exact" || len(r.Handlers) != 3 || len(detail["source_facts"].([]domain.BusinessFact)) == 0 {
		t.Fatalf("%+v", detail)
	}
	bytes, e := s.Workspace.Objects.Get(r.Evidence.Blob)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(bytes[r.Evidence.Start:r.Evidence.End]), "AgeRule") {
		t.Fatal("registration evidence not source-bound")
	}
	impact := call(t, s, "impact.get", Params{Query: "MinimumAge", Context: map[string]string{"Product": "retail"}, Depth: 5, Limit: 100}).(map[string]any)
	affected := impact["affected_rules"].([]domain.BusinessRule)
	if len(affected) != 2 {
		t.Fatalf("shared consumers lost: %+v", impact)
	}
	if len(impact["related_tests"].([]business.Node)) == 0 || len(impact["possible"].([]business.Edge)) == 0 {
		t.Fatalf("tests/dynamic gaps not reported: %+v", impact)
	}
	flow := call(t, s, "flow.get", Params{Query: r.ID, Depth: 5, Limit: 100}).(map[string]any)
	found := false
	for _, node := range flow["nodes"].([]business.Node) {
		if node.Name == "MinimumAge" {
			found = true
		}
	}
	if !found {
		t.Fatalf("constant dependency absent: %+v", flow)
	}
	limitedFlow := call(t, s, "flow.get", Params{Query: r.ID, Limit: 1, Depth: 3}).(map[string]any)
	if len(limitedFlow["nodes"].([]business.Node)) > 1 || limitedFlow["truncated"] != true {
		t.Fatal("rule handler seeds exceeded node budget")
	}
	limited := call(t, s, "impact.get", Params{Query: "MinimumAge", Depth: 1, Limit: 1}).(map[string]any)
	if limited["truncated"] != true || len(limited["unexamined"].([]map[string]any)) == 0 {
		t.Fatal("bounded traversal claimed complete")
	}
	diagnostics := call(t, s, "rules.diagnostics", Params{}).(map[string]any)
	if len(diagnostics["diagnostics"].([]domain.BusinessDiagnostic)) != 0 {
		t.Fatalf("unexpected gaps %+v", diagnostics)
	}
	// Business evidence can anchor a plan just like structural/file evidence.
	pin, e := s.resolve(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.validateEvidence(ctx, pin.Path, domain.Plan{Steps: []domain.PlanStep{{Evidence: []string{r.Evidence.ID}}}}); e != nil {
		t.Fatal(e)
	}
	// Move the shared helper to its own file: file-scope analysis must still find both products.
	original, e := os.ReadFile(filepath.Join(s.Workspace.Root, "rules.go"))
	if e != nil {
		t.Fatal(e)
	}
	lines := strings.Split(string(original), "\n")
	kept := []string{}
	for _, line := range lines {
		if strings.HasPrefix(line, "const MinimumAge =") || strings.HasPrefix(line, "func AgeThreshold()") {
			continue
		}
		kept = append(kept, line)
	}
	if e = os.WriteFile(filepath.Join(s.Workspace.Root, "rules.go"), []byte(strings.Join(kept, "\n")), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(s.Workspace.Root, "shared.go"), []byte("package business\nconst MinimumAge=18\nfunc AgeThreshold()int{return MinimumAge}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	epoch := 1
	call(t, s, "index.build", Params{Epoch: &epoch, Operation: "split-shared-helper"})
	s.Wait()
	newPin, e := s.resolve(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	index, e := business.Open(newPin.Path)
	if e != nil {
		t.Fatal(e)
	}
	newGraph, e := business.Load(ctx, index)
	if e != nil {
		t.Fatal(e)
	}
	affectedRules, gaps, e := business.AffectedRules(ctx, index, newGraph, []string{"shared.go"})
	index.Close()
	if e != nil || len(gaps) > 0 || len(affectedRules) != 2 {
		t.Fatalf("shared helper scope lost consumers: %+v %v %v", affectedRules, gaps, e)
	}
	oldGen := list["index_generation_id"].(string)
	oldConfigDigest := business.Digest(s.Config.Business)
	s.Config.Business.Adapters[0].PriorityField = "UnknownField"
	if _, e = s.Handle(ctx, "rules.search", Params{Schema: domain.API, Query: "AGE_MIN"}); e == nil {
		t.Fatal("stale domain config accepted")
	}
	historical := call(t, s, "rules.search", Params{Query: "AGE_MIN", Generation: oldGen}).(map[string]any)
	if historical["config_digest"] != oldConfigDigest {
		t.Fatal("historical adapters drifted")
	}
}
func TestBusinessAdaptersDiagnosticsAndConfigIdentity(t *testing.T) {
	s := businessFixture(t)
	ctx := context.Background()
	root := s.Workspace.Root
	extra := `package business
 type OtherRule struct{ID string; Apply func(Input,*Decision)}
 var Ignored=OtherRule{ID:"NOT_A_RULE",Apply:RejectAge}
 func Register(id string,product string,fn func(Input,*Decision)){}
 func RegisterAll(id string,fn func(Input,*Decision)){}
 var Indirect=RejectAge
 var DynamicRules=[]Rule{{ID:dynamicID(),Product:"retail",Priority:40,When:Always,Except:Never,Apply:Indirect}}
 func dynamicID()string{return "DYNAMIC"}
 func Bind(){Register("CALL_RULE","retail",RejectAge);RegisterAll("WRONG_CALL",RejectAge)}
 `
	extra += "\nfunc DynamicConsumers(fn func(int)bool){\n" + strings.Repeat("fn(1)\n", 55) + "}\n"
	if e := os.WriteFile(filepath.Join(root, "registry.go"), []byte(extra), 0600); e != nil {
		t.Fatal(e)
	}
	s.Config.Business.Adapters = append(s.Config.Business.Adapters, domain.RuleAdapter{Name: "register", Kind: "call", Package: "seai.fixture/business", Symbol: "Register", IDArg: 0, HandlerArgs: map[string]int{"action": 2}, ContextArgs: map[string]int{"Product": 1}}, domain.RuleAdapter{Name: "function", Kind: "function", Package: "seai.fixture/business", Symbol: "Evaluate"})
	epoch := 1
	call(t, s, "index.build", Params{Epoch: &epoch, Operation: "extended-business"})
	s.Wait()
	job := call(t, s, "jobs.get", Params{Job: "extended-business"}).(Job)
	if job.State != "SUCCEEDED" {
		t.Fatal(job)
	}
	query := call(t, s, "rules.search", Params{}).(map[string]any)
	rules := query["rules"].([]domain.BusinessRule)
	if len(rules) != 7 {
		t.Fatalf("rules: %+v", rules)
	}
	for _, r := range rules {
		if r.Key == "NOT_A_RULE" || r.Key == "WRONG_CALL" {
			t.Fatal("name-based false positive", r)
		}
	}
	diagnostics := call(t, s, "rules.diagnostics", Params{}).(map[string]any)
	codes := map[string]bool{}
	for _, d := range diagnostics["diagnostics"].([]domain.BusinessDiagnostic) {
		codes[d.Code] = true
	}
	if !codes["RULE_ID_UNRESOLVED"] || !codes["HANDLER_UNRESOLVED"] {
		t.Fatal(diagnostics)
	}
	dynamicImpact := call(t, s, "impact.get", Params{Query: "MinimumAge", Depth: 5, Limit: 100}).(map[string]any)
	disclosed := false
	for _, gap := range dynamicImpact["unexamined"].([]map[string]any) {
		if gap["reason"] == "possible_consumer_limit" {
			disclosed = true
		}
	}
	if !disclosed {
		t.Fatal("omitted possible consumers were not disclosed")
	}
	pin, e := s.resolve(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.businessGate(ctx, pin.Path, []string{"registry.go"}); e == nil {
		t.Fatal("unresolved registration allowed through strict change gate")
	}
	before := pin.Generation
	s.Config.Business.Adapters[0].Name = "new-rules"
	epoch = 2
	call(t, s, "index.build", Params{Epoch: &epoch, Operation: "different-adapter"})
	s.Wait()
	after, e := s.resolve(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	if before == after.Generation || after.Snapshot != pin.Snapshot {
		t.Fatal("adapter identity not part of immutable generation")
	}
	// Snapshot source is unchanged by analysis.
	b, e := os.ReadFile(filepath.Join(root, "registry.go"))
	if e != nil || string(b) != extra {
		t.Fatal("source changed")
	}
	raw, _ := json.Marshal(s.Config)
	if strings.Contains(string(raw), "credential") {
		t.Fatal("unexpected config")
	}
}

func TestBusinessCandidateAnalysisBlocksNewUnknownRegistration(t *testing.T) {
	s := businessFixture(t)
	ctx := context.Background()
	pin, e := s.resolve(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	spec := domain.ChangeSpec{Schema: "seai.change/v1", Alias: "DOMAIN-GUARD", Revision: 1, Kind: "refactor", Objective: "Preserve rule behavior", Requirements: []domain.Requirement{}, Scope: domain.Scope{Files: []string{"rules.go"}}, Expected: []domain.Behavior{}, Preserved: []domain.Behavior{{ID: "INV", Statement: "All rule behavior preserved"}}, Criteria: []domain.Criterion{{ID: "AC1", BehaviorIDs: []string{"INV"}, Expected: "Same outputs", Profile: "business"}}, Unresolved: []string{}}
	zero, epoch := 0, 1
	task := call(t, s, "task.create", Params{Operation: "domain-create", Epoch: &epoch, Revision: &zero, Spec: &spec}).(domain.Task)
	gdb, e := business.Open(pin.Path)
	if e != nil {
		t.Fatal(e)
	}
	graph, e := business.Load(ctx, gdb)
	gdb.Close()
	if e != nil {
		t.Fatal(e)
	}
	p := mutation("domain-plan", task)
	p.Plan = &domain.Plan{Schema: "seai.plan/v1", Steps: []domain.PlanStep{{ID: "step", Description: "Refactor registry", Files: []string{"rules.go"}, Criteria: []string{"AC1"}, Evidence: []string{graph.Rules[0].Evidence.ID}}}, Checks: []string{"compile", "business"}}
	task = call(t, s, "task.plan", p).(domain.Task)
	manifest, e := s.manifest(ctx, task.Candidate)
	if e != nil {
		t.Fatal(e)
	}
	var file domain.File
	for _, f := range manifest.Files {
		if f.Path == "rules.go" {
			file = f
		}
	}
	original, e := s.Workspace.Objects.Get(file.Digest)
	if e != nil {
		t.Fatal(e)
	}
	content := string(original) + "\nfunc runtimeRuleID()string{return \"DYNAMIC\"}\nvar NewRule=Rule{ID:runtimeRuleID(),Product:\"retail\",Priority:40,When:Always,Except:Never,Apply:RejectAge}\n"
	after := domain.Digest([]byte(content))
	p = mutation("domain-patch", task)
	p.Patch = &domain.Patch{Schema: "seai.patch/v1", Base: task.Candidate, SpecDigest: task.SpecDigest, Files: []domain.FileOperation{{Path: file.Path, Before: &file.Digest, BeforeMode: file.Mode, AfterDigest: &after, Content: &content, Mode: file.Mode}}}
	task = call(t, s, "patch.apply", p).(domain.Task)
	s.Profiles = map[string]domain.CheckProfile{"compile": {ID: "compile", Kind: "compile", Program: "/bin/sh", Args: []string{"-c", "exit 0"}, Isolation: "local-approved", AcknowledgeLocal: true, TimeoutSeconds: 3, MaxOutputBytes: 4096}}
	run := waitRun(t, s, call(t, s, "task.verify", mutation("domain-verify", task)))
	if run["state"] != "FAILED" {
		t.Fatal("candidate unknown registration accepted", run)
	}
	var results []domain.CheckResult
	b, _ := json.Marshal(run["result"])
	if e = json.Unmarshal(b, &results); e != nil || len(results) != 1 || results[0].ProfileID != "domain-analysis" || results[0].Status != "FAILED" || len(results[0].Domain.Gaps) == 0 {
		t.Fatal(string(b), e)
	}
	current := call(t, s, "task.get", Params{Task: task.ID}).(domain.Task)
	if current.Readiness != "NOT_REVIEW_READY" {
		t.Fatal("failed domain analysis became ready")
	}
	// Removing a registration must retain its old product/context in fixture requirements.
	lines := strings.Split(string(original), "\n")
	kept := []string{}
	for _, line := range lines {
		if strings.Contains(line, `Product: "business"`) {
			continue
		}
		kept = append(kept, line)
	}
	removedContent := strings.Join(kept, "\n")
	currentManifest, e := s.manifest(ctx, current.Candidate)
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range currentManifest.Files {
		if f.Path == "rules.go" {
			file = f
		}
	}
	removedDigest := domain.Digest([]byte(removedContent))
	p = mutation("domain-remove", current)
	p.Patch = &domain.Patch{Schema: "seai.patch/v1", Base: current.Candidate, SpecDigest: current.SpecDigest, Files: []domain.FileOperation{{Path: file.Path, Before: &file.Digest, BeforeMode: file.Mode, AfterDigest: &removedDigest, Content: &removedContent, Mode: file.Mode}}}
	current = call(t, s, "patch.apply", p).(domain.Task)
	s.Profiles["business"] = domain.CheckProfile{ID: "business", Kind: "comparison", Program: "/bin/sh", Args: []string{"-c", "exit 0"}, Isolation: "local-approved", AcknowledgeLocal: true, TimeoutSeconds: 3, MaxOutputBytes: 4096, AssertionProfile: "business", Fixtures: []domain.Fixture{{ID: "retail", Input: "", Expected: json.RawMessage(`{}`), Criteria: []string{"AC1"}, AllowedPaths: []string{}, RuleKeys: []string{"AGE_MIN", "INCOME_MIN", "CREDIT_LIMIT"}, Context: map[string]string{"Product": "retail"}}}}
	removalRun := waitRun(t, s, call(t, s, "task.verify", mutation("domain-remove-verify", current)))
	if removalRun["state"] != "FAILED" {
		t.Fatal("removed business context silently lost fixture obligation", removalRun)
	}
	raw, _ := json.Marshal(removalRun["result"])
	results = nil
	if e = json.Unmarshal(raw, &results); e != nil || len(results) != 1 || results[0].Domain == nil || len(results[0].Domain.RemovedRules) != 1 || results[0].Domain.RemovedRules[0].Context["Product"] != "business" {
		t.Fatal(string(raw), e)
	}
}

func TestBusinessReviewerUsesVerifiedCandidateGeneration(t *testing.T) {
	s := businessFixture(t)
	ctx := context.Background()
	task := newTask(t, s)
	baseline, e := s.resolve(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	manifest, e := s.manifest(ctx, task.Candidate)
	if e != nil {
		t.Fatal(e)
	}
	blob, e := s.Workspace.Objects.Put([]byte("package business\nconst ReviewerCheckpoint=true\n"))
	if e != nil {
		t.Fatal(e)
	}
	manifest.Files = append(manifest.Files, domain.File{Path: "review_checkpoint.go", Mode: "100644", Digest: blob})
	candidate, e := s.saveObject(manifest)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Store.DB.Exec("INSERT INTO source_snapshots VALUES(?,?)", candidate, candidate); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Store.DB.Exec("UPDATE tasks SET candidate_id=?,revision=revision+1 WHERE id=?", candidate, task.ID); e != nil {
		t.Fatal(e)
	}
	task, e = s.getTask(ctx, s.Store.DB, task.ID)
	if e != nil {
		t.Fatal(e)
	}
	gen, path, coverage, e := indexer.Build(ctx, s.Data, s.Workspace.Objects, manifest, candidate, s.Config.Business)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Store.DB.Exec("INSERT INTO index_generations VALUES(?,?,?,?,'READY')", gen, candidate, path, coverage); e != nil {
		t.Fatal(e)
	}
	result := domain.CheckResult{ProfileID: "domain-analysis", Status: "PASSED", Domain: &domain.BusinessVerification{Source: candidate, Generation: gen}}
	digest, e := s.saveObject(result)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Store.DB.Exec("INSERT INTO runs(id,kind,task_id,state,fingerprint,task_revision,epoch,created_at) VALUES('review-checkpoint','task.verify',?,'SUCCEEDED',?,?,?,?)", task.ID, s.fingerprint(task), task.Revision, task.Epoch, now()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Store.DB.Exec("INSERT INTO check_runs VALUES('review-domain','review-checkpoint',?,?,?,?,?,?,?,?,'PASSED',?,?)", task.ID, task.Candidate, task.Baseline, task.SpecDigest, task.PlanDigest, s.ConfigDigest, "domain-analysis", s.fingerprint(task), digest, now()); e != nil {
		t.Fatal(e)
	}
	pin, e := s.workerPin(ctx, "reviewer", task)
	if e != nil || pin.Generation != gen || pin.Snapshot != candidate || pin.Generation == baseline.Generation {
		t.Fatal(pin, e)
	}
	pin, e = s.workerPin(ctx, "implementer", task)
	if e != nil || pin.Generation != baseline.Generation {
		t.Fatal("unverified implementer graph changed", pin, e)
	}
	task.Candidate = task.Baseline
	if _, e = s.workerPin(ctx, "reviewer", task); e == nil {
		t.Fatal("candidate checkpoint mismatch accepted")
	}
}
