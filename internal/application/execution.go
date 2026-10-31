package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"seai.local/seai/internal/checks"
	"seai.local/seai/internal/domain"
)

type runWork func(context.Context, string, domain.Task) (any, error)

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func (s *Service) fingerprint(t domain.Task) string {
	b, _ := domain.Canonical(map[string]any{"task": t.ID, "baseline": t.Baseline, "candidate": t.Candidate, "spec": t.SpecDigest, "plan": t.PlanDigest, "config": s.ConfigDigest, "epoch": t.Epoch, "approved": t.Approved})
	return domain.Digest(b)
}
func (s *Service) advanced(ctx context.Context, method string, p Params) (any, error) {
	switch method {
	case "checks.profiles":
		return checks.ProfileSummaries(s.Profiles), nil
	case "provider.status":
		if s.Provider == nil {
			return map[string]any{"configured": false, "verified": false}, nil
		}
		return s.Provider.Status(), nil
	case "provider.probe":
		if s.Provider == nil {
			return nil, domain.Err("PROVIDER_UNAVAILABLE", "configure Qwen first")
		}
		return s.startRun(ctx, method, p, func(c context.Context, _ string, _ domain.Task) (any, error) {
			e := s.Provider.Probe(c)
			return s.Provider.Status(), e
		})
	case "runs.artifact":
		return s.runArtifact(ctx, p)
	case "runs.logs":
		return s.runLogs(ctx, p)
	case "runs.get":
		var id, kind, state string
		var result, errtext sql.NullString
		var lease int
		e := s.Store.DB.QueryRowContext(ctx, "SELECT id,kind,state,result_digest,error,lease_epoch FROM runs WHERE id=?", p.ID).Scan(&id, &kind, &state, &result, &errtext, &lease)
		if e == sql.ErrNoRows {
			return nil, domain.Err("NOT_FOUND", "run not found")
		}
		if e != nil {
			return nil, e
		}
		out := map[string]any{"run_id": id, "kind": kind, "state": state, "lease_epoch": lease, "error": errtext.String}
		if result.Valid {
			b, e := s.Workspace.Objects.Get(result.String)
			if e != nil {
				return nil, e
			}
			out["result"] = json.RawMessage(b)
		}
		return out, nil
	case "runs.cancel":
		s.controlMu.Lock()
		cancel := s.cancellations[p.ID]
		s.controlMu.Unlock()
		if cancel == nil {
			return nil, domain.Err("INVALID_STATE", "run is not active in this daemon")
		}
		cancel()
		_, e := s.Store.DB.ExecContext(ctx, "UPDATE runs SET state='CANCELLED',lease_epoch=lease_epoch+1,finished_at=? WHERE id=? AND state='RUNNING'", now(), p.ID)
		return map[string]any{"run_id": p.ID, "state": "CANCELLED"}, e
	case "agents.list":
		return s.agents(ctx, p)
	case "checks.run", "task.verify":
		return s.startRun(ctx, method, p, func(c context.Context, id string, t domain.Task) (any, error) { return s.verify(c, id, t, p.Profile) })
	case "task.run":
		return s.startRun(ctx, method, p, func(c context.Context, id string, t domain.Task) (any, error) {
			return s.orchestrateResume(c, id, t, p.Resume)
		})
	case "release.plan", "release.inspect", "release.apply", "release.rollback", "task.rebase":
		return s.release(ctx, method, p)
	case "backup.create":
		return s.startRun(ctx, method, p, s.backup)
	}
	return nil, domain.Err("METHOD_NOT_FOUND", "runtime command")
}
func (s *Service) startRun(ctx context.Context, method string, p Params, work runWork) (any, error) {
	if p.Operation == "" || len(p.Operation) > 128 || p.Epoch == nil {
		return nil, domain.Err("SCHEMA_INVALID", "operation_id and expected_project_epoch required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, e := domain.Canonical(p)
	if e != nil {
		return nil, e
	}
	digest := domain.Digest(b)
	var om, od, result string
	e = s.Store.DB.QueryRowContext(ctx, "SELECT method,payload_digest,result_json FROM operations WHERE operation_id=?", p.Operation).Scan(&om, &od, &result)
	if e == nil {
		if om != method || od != digest {
			return nil, domain.Err("IDEMPOTENCY_CONFLICT", "operation payload differs")
		}
		return json.RawMessage(result), nil
	}
	if e != sql.ErrNoRows {
		return nil, e
	}
	if s.busy {
		return nil, domain.Err("WORKFLOW_BUSY", "another operation is active")
	}
	var epoch int
	if e = s.Store.DB.QueryRowContext(ctx, "SELECT epoch FROM projects").Scan(&epoch); e != nil {
		return nil, e
	}
	if epoch != *p.Epoch {
		return nil, domain.Err("REVISION_CONFLICT", "project epoch differs")
	}
	var t domain.Task
	var task any
	if p.Task != "" {
		t, e = s.getTask(ctx, s.Store.DB, p.Task)
		if e != nil {
			return nil, e
		}
		if p.Revision == nil || *p.Revision != t.Revision {
			return nil, domain.Err("REVISION_CONFLICT", "task revision differs")
		}
		if t.State == "CANCELLED" || (method != "task.rebase" && (t.Reconciliation || t.Epoch != epoch)) {
			return nil, domain.Err("STALE_TASK", "resume/reconcile task first")
		}
		pin, e := s.resolve(ctx, "")
		if e != nil {
			return nil, e
		}
		if method != "task.rebase" {
			if e = s.guard(ctx, pin); e != nil {
				return nil, e
			}
		}
		task = t.ID
	}
	if p.Resume != "" {
		var previousTask, previousFingerprint, state string
		if e = s.Store.DB.QueryRowContext(ctx, "SELECT coalesce(task_id,''),fingerprint,state FROM runs WHERE id=? AND kind='task.run'", p.Resume).Scan(&previousTask, &previousFingerprint, &state); e != nil {
			return nil, e
		}
		if previousTask != t.ID || previousFingerprint != s.fingerprint(t) || (state != "FAILED" && state != "ABORTED" && state != "CANCELLED" && state != "NEEDS_INPUT") {
			return nil, domain.Err("CHECKPOINT_MISMATCH", "resume requires failed/aborted/cancelled run and unchanged task/config checkpoint")
		}
	}
	id := uniqueID()
	out := map[string]any{"run_id": id, "state": "RUNNING"}
	raw, _ := json.Marshal(out)
	tx, e := s.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(ctx, "INSERT INTO runs(id,kind,task_id,state,fingerprint,task_revision,epoch,created_at) VALUES(?,?,?,'RUNNING',?,?,?,?)", id, method, task, s.fingerprint(t), t.Revision, epoch, now())
	if e != nil {
		return nil, domain.Err("WORKFLOW_BUSY", "another durable run is active")
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO operations VALUES(?,?,?,?)", p.Operation, method, digest, string(raw)); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	maxSeconds := s.Config.MaxRunSeconds
	if maxSeconds == 0 {
		maxSeconds = 1800
	}
	runctx, cancel := context.WithTimeout(context.Background(), time.Duration(maxSeconds)*time.Second)
	s.controlMu.Lock()
	if s.cancellations == nil {
		s.cancellations = map[string]context.CancelFunc{}
	}
	s.cancellations[id] = cancel
	s.controlMu.Unlock()
	s.busy = true
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		value, err := work(runctx, id, t)
		s.mu.Lock()
		defer s.mu.Unlock()
		defer func() { s.busy = false; s.controlMu.Lock(); delete(s.cancellations, id); s.controlMu.Unlock() }()
		state := "SUCCEEDED"
		message := ""
		if err != nil {
			state = "FAILED"
			message = err.Error()
		}
		if runctx.Err() != nil {
			state = "CANCELLED"
			message = runctx.Err().Error()
		}
		// Check result publication and cancellation are fenced by the durable run lease.
		var currentEpoch int
		_ = s.Store.DB.QueryRow("SELECT epoch FROM projects").Scan(&currentEpoch)
		if currentEpoch != epoch && method != "release.apply" && method != "release.rollback" {
			state = "ABORTED"
			message = "project changed during execution"
		}
		d, saveErr := s.saveObject(value)
		if saveErr != nil {
			state = "FAILED"
			message = saveErr.Error()
		}
		_, _ = s.Store.DB.Exec("UPDATE runs SET state=?,result_digest=?,error=?,finished_at=? WHERE id=? AND state='RUNNING' AND lease_epoch=1", state, d, message, now(), id)
	}()
	return out, nil
}
func (s *Service) verify(ctx context.Context, run string, t domain.Task, profile string) (any, error) {
	if t.ID == "" || t.Plan == nil {
		return nil, domain.Err("PLAN_REQUIRED", "verification requires task and plan")
	}
	names := t.Plan.Checks
	if profile != "" {
		names = []string{profile}
	}
	if len(names) == 0 {
		return nil, domain.Err("CHECKS_REQUIRED", "plan requires registered profiles")
	}
	base, e := s.manifest(ctx, t.Baseline)
	if e != nil {
		return nil, e
	}
	candidate, e := s.manifest(ctx, t.Candidate)
	if e != nil {
		return nil, e
	}
	parent := filepath.Join(s.Data, "staging")
	if e = os.MkdirAll(parent, 0700); e != nil {
		return nil, e
	}
	results := []domain.CheckResult{}
	for _, name := range names {
		p, ok := s.Profiles[name]
		if !ok {
			return results, domain.Err("PROFILE_NOT_FOUND", "unregistered check: %s", name)
		}
		id := uniqueID()
		_, e = s.Store.DB.ExecContext(ctx, "INSERT INTO check_runs VALUES(?,?,?,?,?,?,?,?,?,?,?,NULL,?)", id, run, t.ID, t.Candidate, t.Baseline, t.SpecDigest, t.PlanDigest, s.ConfigDigest, name, s.fingerprint(t), "RUNNING", now())
		if e != nil {
			return results, e
		}
		result := checks.Run(ctx, p, base, candidate, t.Spec, s.Workspace.Objects, parent)
		d, e := s.saveObject(result)
		if e != nil {
			return results, e
		}
		s.mu.Lock()
		current, err := s.getTask(ctx, s.Store.DB, t.ID)
		var lease int
		var state string
		fenceErr := s.Store.DB.QueryRowContext(ctx, "SELECT state,lease_epoch FROM runs WHERE id=?", run).Scan(&state, &lease)
		if err != nil || fenceErr != nil || state != "RUNNING" || lease != 1 || current.Revision != t.Revision || s.fingerprint(current) != s.fingerprint(t) {
			result.Status = "ABORTED"
			result.Error = "task revision or execution lease changed"
			d, e = s.saveObject(result)
		}
		if e == nil {
			_, e = s.Store.DB.ExecContext(context.Background(), "UPDATE check_runs SET status=?,result_digest=? WHERE id=?", result.Status, d, id)
		}
		s.mu.Unlock()
		if e != nil {
			return results, e
		}
		results = append(results, result)
		if result.Status != "PASSED" {
			return results, domain.Err("CHECK_FAILED", "profile %s: %s %s", name, result.Status, result.Error)
		}
	}
	return results, nil
}
func (s *Service) checkSummary(ctx context.Context, t domain.Task) map[string]any {
	summary := map[string]any{"checks_passed": false, "review_passed": false, "input_digest": s.fingerprint(t), "results": []domain.CheckResult{}}
	if t.Plan == nil || len(t.Plan.Checks) == 0 {
		return summary
	}
	criteria := map[string]bool{}
	kinds := map[string]bool{}
	results := []domain.CheckResult{}
	ids := []string{}
	all := true
	var active int
	_ = s.Store.DB.QueryRowContext(ctx, "SELECT count(*) FROM runs WHERE task_id=? AND state IN ('QUEUED','RUNNING')", t.ID).Scan(&active)
	if active > 0 {
		all = false
	}
	for _, name := range t.Plan.Checks {
		p, ok := s.Profiles[name]
		if !ok {
			all = false
			continue
		}
		var checkID, digest, status, runState string
		e := s.Store.DB.QueryRowContext(ctx, "SELECT c.id,coalesce(c.result_digest,''),c.status,r.state FROM check_runs c JOIN runs r ON r.id=c.run_id WHERE c.task_id=? AND c.profile_id=? AND c.input_digest=? ORDER BY c.rowid DESC LIMIT 1", t.ID, name, s.fingerprint(t)).Scan(&checkID, &digest, &status, &runState)
		if e != nil || status != "PASSED" || runState != "SUCCEEDED" {
			all = false
			continue
		}
		b, e := s.Workspace.Objects.Get(digest)
		var result domain.CheckResult
		if e != nil || json.Unmarshal(b, &result) != nil {
			all = false
			continue
		}
		results = append(results, result)
		ids = append(ids, checkID)
		kinds[p.Kind] = true
		for _, id := range result.Criteria {
			criteria[id] = true
		}
	}
	for _, c := range t.Spec.Criteria {
		if !criteria[c.ID] {
			all = false
		}
	}
	if len(t.Spec.Criteria) == 0 {
		all = false
	}
	all = all && kinds["compile"] && kinds["tests"] && kinds["comparison"] && (t.Kind != "change" || t.Approved) && !t.Reconciliation
	reviewInput, _ := json.Marshal(map[string]any{"input": s.fingerprint(t), "results": results, "check_run_ids": ids})
	reviewDigest := domain.Digest(reviewInput)
	summary["review_digest"] = reviewDigest
	var n int
	_ = s.Store.DB.QueryRowContext(ctx, "SELECT count(*) FROM reviews WHERE task_id=? AND input_digest=? AND verdict='PASS'", t.ID, reviewDigest).Scan(&n)
	summary["checks_passed"] = all
	summary["review_passed"] = all && n > 0
	summary["results"] = results
	return summary
}

func (s *Service) CancelRuns() {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	for _, cancel := range s.cancellations {
		cancel()
	}
}

func (s *Service) runLogs(ctx context.Context, p Params) (any, error) {
	var d string
	if p.Profile == "" {
		return nil, domain.Err("SCHEMA_INVALID", "profile_id required to bound logs")
	}
	if e := s.Store.DB.QueryRowContext(ctx, "SELECT result_digest FROM check_runs WHERE run_id=? AND profile_id=? AND result_digest IS NOT NULL ORDER BY rowid DESC LIMIT 1", p.ID, p.Profile).Scan(&d); e != nil {
		return nil, e
	}
	raw, e := s.Workspace.Objects.Get(d)
	if e != nil {
		return nil, e
	}
	var result domain.CheckResult
	if e = json.Unmarshal(raw, &result); e != nil {
		return nil, e
	}
	out := map[string]any{"run_id": p.ID, "profile_id": p.Profile, "result": result}
	for label, digest := range map[string]string{"stdout": result.StdoutDigest, "stderr": result.StderrDigest} {
		if digest != "" {
			b, e := s.Workspace.Objects.Get(digest)
			if e != nil {
				return nil, e
			}
			if len(b) > 1048576 {
				b = b[:1048576]
				out["truncated"] = true
			}
			out[label] = string(b)
		}
	}
	return out, nil
}

func (s *Service) runArtifact(ctx context.Context, p Params) (any, error) {
	var resultDigest string
	if e := s.Store.DB.QueryRowContext(ctx, "SELECT coalesce(result_digest,'') FROM runs WHERE id=?", p.ID).Scan(&resultDigest); e != nil {
		return nil, e
	}
	allowed := map[string]bool{}
	comparisonIndexes := map[string]bool{}
	var collect func(any)
	collect = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, item := range x {
				collect(item)
			}
		case map[string]any:
			for k, item := range x {
				if k == "stdout_digest" || k == "stderr_digest" || k == "marker_digest" {
					if d, ok := item.(string); ok && len(d) == 64 {
						allowed[d] = true
					}
				}
				collect(item)
			}
		}
	}
	if resultDigest != "" {
		raw, e := s.Workspace.Objects.Get(resultDigest)
		if e != nil {
			return nil, e
		}
		var v any
		if json.Unmarshal(raw, &v) == nil {
			collect(v)
		}
	}
	rows, e := s.Store.DB.QueryContext(ctx, "SELECT result_digest FROM check_runs WHERE run_id=? AND result_digest IS NOT NULL", p.ID)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var d string
		if e = rows.Scan(&d); e != nil {
			rows.Close()
			return nil, e
		}
		b, e := s.Workspace.Objects.Get(d)
		if e != nil {
			rows.Close()
			return nil, e
		}
		var v any
		if json.Unmarshal(b, &v) == nil {
			collect(v)
		}
		var check domain.CheckResult
		if json.Unmarshal(b, &check) == nil && check.FixtureDigest != "" && check.StdoutDigest != "" {
			comparisonIndexes[check.StdoutDigest] = true
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	// Comparison logs are an index of the four executions per fixture.
	for d := range comparisonIndexes {
		b, e := s.Workspace.Objects.Get(d)
		if e != nil {
			return nil, e
		}
		var logs []map[string]any
		if json.Unmarshal(b, &logs) == nil {
			for _, log := range logs {
				if _, ok := log["fixture_id"]; ok {
					collect(log)
				}
			}
		}
	}
	if !allowed[p.Artifact] {
		return nil, domain.Err("ARTIFACT_NOT_FOUND", "digest is not a log/conflict artifact owned by this run")
	}
	b, e := s.Workspace.Objects.Get(p.Artifact)
	if e != nil {
		return nil, e
	}
	truncated := len(b) > 1048576
	if truncated {
		b = b[:1048576]
	}
	return map[string]any{"run_id": p.ID, "artifact_digest": p.Artifact, "text": string(b), "truncated": truncated}, nil
}
