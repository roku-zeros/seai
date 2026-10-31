package application

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"seai.local/seai/internal/changespec"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/patches"
	"seai.local/seai/internal/reconcile"
)

func uniqueID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	h := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[:8], h[8:12], h[12:16], h[16:20], h[20:])
}

type taskReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Service) getTask(ctx context.Context, reader taskReader, id string) (domain.Task, error) {
	var t domain.Task
	var plan, approved sql.NullString
	e := reader.QueryRowContext(ctx, "SELECT id,alias,kind,state,revision,baseline_id,candidate_id,baseline_epoch,spec_digest,behavior_digest,plan_digest,approved_digest,checkpoint,reconciliation_required,requirements_freshness FROM tasks WHERE id=? OR alias=? ORDER BY CASE WHEN id=? THEN 0 ELSE 1 END LIMIT 1", id, id, id).Scan(&t.ID, &t.Alias, &t.Kind, &t.State, &t.Revision, &t.Baseline, &t.Candidate, &t.Epoch, &t.SpecDigest, &t.BehaviorDigest, &plan, &approved, &t.Checkpoint, &t.Reconciliation, &t.RequirementsFreshness)
	if e == sql.ErrNoRows {
		return t, domain.Err("NOT_FOUND", "task not found")
	}
	if e != nil {
		return t, e
	}
	t.PlanDigest = plan.String
	t.Approved = approved.Valid && approved.String == t.BehaviorDigest
	t.Freshness = "CURRENT"
	t.Readiness = "NOT_REVIEW_READY"
	b, e := s.Workspace.Objects.Get(t.SpecDigest)
	if e != nil {
		return t, e
	}
	if e = json.Unmarshal(b, &t.Spec); e != nil {
		return t, e
	}
	sd, bd, err := changespec.Digests(t.Spec)
	if err != nil {
		return t, err
	}
	if sd != t.SpecDigest || bd != t.BehaviorDigest {
		return t, domain.Err("STORAGE_CORRUPT", "task spec/behavior identities differ")
	}

	if plan.Valid {
		b, e = s.Workspace.Objects.Get(plan.String)
		if e != nil {
			return t, e
		}
		t.Plan = &domain.Plan{}
		if e = json.Unmarshal(b, t.Plan); e != nil {
			return t, e
		}
	}
	return t, nil
}
func (s *Service) saveObject(v any) (string, error) {
	b, e := domain.Canonical(v)
	if e != nil {
		b, e = json.Marshal(v)
		if e != nil {
			return "", e
		}
	}
	return s.Workspace.Objects.Put(b)
}
func (s *Service) workflow(ctx context.Context, method string, p Params) (any, error) {
	if method == "task.get" || method == "task.diff" {
		t, e := s.getTask(ctx, s.Store.DB, p.Task)
		if e != nil {
			return nil, e
		}
		pin, e := s.resolve(ctx, "")
		if e != nil {
			return nil, e
		}
		if t.Epoch != pin.Epoch || t.Baseline != pin.Snapshot {
			t.Freshness = "STALE"
		} else if e = s.guard(ctx, pin); e != nil {
			t.Freshness = "STALE"
		}
		t.CheckSummary = s.checkSummary(ctx, t)
		if t.Freshness == "CURRENT" && t.CheckSummary["review_passed"] == true && t.RequirementsFreshness != "not_checked" && t.State != "CANCELLED" {
			t.Readiness = "REVIEW_READY"
		}
		if method == "task.get" {
			return t, nil
		}
		return s.diff(ctx, t)
	}
	if p.Operation == "" || len(p.Operation) > 128 || p.Epoch == nil || p.Revision == nil || *p.Revision < 0 {
		return nil, domain.Err("SCHEMA_INVALID", "operation_id, expected_revision and expected_project_epoch required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	payload, e := domain.Canonical(p)
	if e != nil {
		return nil, e
	}
	payloadDigest := domain.Digest(payload)
	var oldMethod, oldDigest, result string
	e = s.Store.DB.QueryRowContext(ctx, "SELECT method,payload_digest,result_json FROM operations WHERE operation_id=?", p.Operation).Scan(&oldMethod, &oldDigest, &result)
	if e == nil {
		if oldMethod != method || oldDigest != payloadDigest {
			return nil, domain.Err("IDEMPOTENCY_CONFLICT", "operation ID already binds another command/payload")
		}
		if !json.Valid([]byte(result)) {
			return nil, domain.Err("STORAGE_CORRUPT", "invalid saved operation result")
		}
		return json.RawMessage(result), nil
	}
	if e != sql.ErrNoRows {
		return nil, e
	}
	if s.busy && ctx.Value(integratorKey{}) == nil {
		return nil, domain.Err("WORKFLOW_BUSY", "index publication is in progress")
	}
	pin, e := s.resolve(ctx, "")
	if e != nil {
		return nil, e
	}
	if *p.Epoch != pin.Epoch {
		return nil, domain.Err("REVISION_CONFLICT", "expected project epoch %d, actual %d", *p.Epoch, pin.Epoch)
	}
	if method != "task.cancel" {
		if e = s.guard(ctx, pin); e != nil {
			return nil, e
		}
	}
	var t domain.Task
	if method != "task.create" {
		t, e = s.getTask(ctx, s.Store.DB, p.Task)
		if e != nil {
			return nil, e
		}
		if *p.Revision != t.Revision {
			return nil, domain.Err("REVISION_CONFLICT", "expected task revision %d, actual %d", *p.Revision, t.Revision)
		}
		if method != "task.cancel" && (t.Epoch != pin.Epoch || t.Baseline != pin.Snapshot) {
			return nil, domain.Err("STALE_TASK", "task baseline/epoch changed; reconcile the task with task rebase")
		}
		if t.State == "CANCELLED" && method != "task.resume" {
			return nil, domain.Err("INVALID_STATE", "resume the cancelled task first")
		}
	}
	var patchID, patchBase, patchManifest string
	var decision bool
	var output any
	switch method {
	case "task.create":
		if *p.Revision != 0 || p.Spec == nil {
			return nil, domain.Err("SCHEMA_INVALID", "new task requires revision=0 and spec")
		}
		if e = changespec.Validate(*p.Spec); e != nil {
			return nil, e
		}
		sd, bd, e := changespec.Digests(*p.Spec)
		if e != nil {
			return nil, e
		}
		if _, e = s.saveObject(p.Spec); e != nil {
			return nil, e
		}
		t = domain.Task{ID: uniqueID(), Alias: p.Spec.Alias, Kind: p.Spec.Kind, State: "SPEC_READY", Revision: 1, Baseline: pin.Snapshot, Candidate: pin.Snapshot, Epoch: pin.Epoch, SpecDigest: sd, BehaviorDigest: bd, Spec: *p.Spec, Checkpoint: "SPEC_READY", Freshness: "CURRENT", Readiness: "NOT_REVIEW_READY"}
	case "task.revise":
		if p.Spec == nil || p.Spec.Revision <= t.Spec.Revision || p.Spec.Alias != t.Alias || p.Spec.Kind != t.Kind {
			return nil, domain.Err("SCHEMA_INVALID", "revision must increase; change_id/kind immutable")
		}
		if t.Candidate != t.Baseline {
			base, err := s.manifest(ctx, t.Baseline)
			if err != nil {
				return nil, err
			}
			candidate, err := s.manifest(ctx, t.Candidate)
			if err != nil {
				return nil, err
			}
			scope := map[string]bool{}
			for _, path := range p.Spec.Scope.Files {
				scope[path] = true
			}
			for _, path := range reconcile.Paths(base, candidate) {
				if !scope[path] {
					return nil, domain.Err("SCOPE_VIOLATION", "revised contract excludes existing candidate path %s", path)
				}
			}
		}
		if e = changespec.Validate(*p.Spec); e != nil {
			return nil, e
		}
		sd, bd, e := changespec.Digests(*p.Spec)
		if e != nil {
			return nil, e
		}
		if _, e = s.saveObject(p.Spec); e != nil {
			return nil, e
		}
		t.Spec = *p.Spec
		t.SpecDigest = sd
		if bd != t.BehaviorDigest {
			t.Approved = false
		}
		t.BehaviorDigest = bd
		t.Plan = nil
		t.PlanDigest = ""
		t.State = "SPEC_READY"
		t.Revision++
	case "task.plan":
		if p.Plan == nil {
			return nil, domain.Err("SCHEMA_INVALID", "plan required")
		}
		if e = changespec.ValidatePlan(*p.Plan, t.Spec); e != nil {
			return nil, e
		}
		if e = s.validateEvidence(ctx, pin.Path, *p.Plan); e != nil {
			return nil, e
		}
		digest, e := s.saveObject(p.Plan)
		if e != nil {
			return nil, e
		}
		t.PlanDigest = digest
		t.Plan = p.Plan
		t.State = "PLANNED"
		if t.Candidate != t.Baseline {
			t.State = "IMPLEMENTING"
		}
		t.Revision++
	case "human.approve":
		if t.Kind != "change" || strings.TrimSpace(p.Actor) == "" || len(p.Actor) > 128 || strings.ContainsAny(p.Actor, "\x00\r\n\x1b") || p.Digest != t.BehaviorDigest {
			return nil, domain.Err("APPROVAL_MISMATCH", "actor and current behavior digest required for a change task")
		}
		t.Approved = true
		t.Revision++
		decision = true
	case "human.review":
		summary := s.checkSummary(ctx, t)
		if p.Actor == "" || len(p.Actor) > 128 || strings.ContainsAny(p.Actor, "\x00\r\n\x1b") || p.Digest != summary["review_digest"] || summary["checks_passed"] != true {
			return nil, domain.Err("REVIEW_MISMATCH", "actor, current input digest and successful checks required")
		}
		t.Revision++
	case "human.requirements":
		if p.Actor == "" || len(p.Actor) > 128 || strings.ContainsAny(p.Actor, "\x00\r\n\x1b") || p.Digest != t.BehaviorDigest {
			return nil, domain.Err("APPROVAL_MISMATCH", "actor and current behavior digest required")
		}
		t.RequirementsFreshness = "human-confirmed"
		t.Revision++
	case "task.cancel":
		if t.State == "CANCELLED" {
			return nil, domain.Err("INVALID_STATE", "already cancelled")
		}
		t.Checkpoint = t.State
		t.State = "CANCELLED"
		t.Revision++
	case "task.resume":
		if t.State != "CANCELLED" {
			return nil, domain.Err("INVALID_STATE", "only cancelled tasks have a resumable checkpoint in this release")
		}
		t.State = t.Checkpoint
		t.Revision++
	case "patch.apply":
		if t.Kind == "explain" || t.Plan == nil {
			return nil, domain.Err("PLAN_REQUIRED", "accepted change/refactor plan required")
		}
		if t.Kind == "change" && !t.Approved {
			return nil, domain.Err("APPROVAL_REQUIRED", "human must approve the current behavior digest")
		}
		if p.Patch == nil {
			return nil, domain.Err("SCHEMA_INVALID", "patch required")
		}
		if e = s.typeGuard(ctx, pin.Path); e != nil {
			return nil, e
		}
		planned := map[string]bool{}
		for _, step := range t.Plan.Steps {
			for _, f := range step.Files {
				planned[f] = true
			}
		}
		for _, op := range p.Patch.Files {
			if !planned[op.Path] {
				return nil, domain.Err("SCOPE_VIOLATION", "path absent from accepted plan: %s", op.Path)
			}
		}
		base, e := s.manifest(ctx, t.Candidate)
		if e != nil {
			return nil, e
		}
		_, id, e := patches.Apply(base, t.Candidate, t.SpecDigest, *p.Patch, t.Spec.Scope.Files, s.Workspace.Objects)
		if e != nil {
			return nil, e
		}
		patchManifest, e = s.saveObject(p.Patch)
		if e != nil {
			return nil, e
		}
		patchID = uniqueID()
		patchBase = t.Candidate
		t.Candidate = id
		t.State = "IMPLEMENTING"
		t.Revision++
	case "task.export":
		t.CheckSummary = s.checkSummary(ctx, t)
		if t.CheckSummary["review_passed"] == true && t.RequirementsFreshness != "not_checked" {
			t.Readiness = "REVIEW_READY"
		}
		if !p.AllowIncomplete && t.Readiness != "REVIEW_READY" {
			return nil, domain.Err("NOT_REVIEW_READY", "current checks and independent review required; allow_incomplete exports diagnostics")
		}
		output, e = s.diff(ctx, t)
		if e != nil {
			return nil, e
		}
	default:
		return nil, domain.Err("METHOD_NOT_FOUND", "workflow method")
	}
	if output == nil {
		output = t
	}
	if method != "task.cancel" {
		if e = s.guard(ctx, pin); e != nil {
			return nil, e
		}
	}
	// Files/CAS objects are durable before one atomic pointer/revision/event/operation commit.
	tx, e := s.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var currentEpoch int
	if e = tx.QueryRowContext(ctx, "SELECT epoch FROM projects").Scan(&currentEpoch); e != nil {
		return nil, e
	}
	if currentEpoch != pin.Epoch {
		return nil, domain.Err("REVISION_CONFLICT", "project changed before commit")
	}
	if method == "task.create" {
		_, e = tx.ExecContext(ctx, "INSERT INTO tasks(id,alias,kind,state,revision,baseline_id,candidate_id,baseline_epoch,spec_digest,behavior_digest,checkpoint) VALUES(?,?,?,?,?,?,?,?,?,?,?)", t.ID, t.Alias, t.Kind, t.State, t.Revision, t.Baseline, t.Candidate, t.Epoch, t.SpecDigest, t.BehaviorDigest, t.Checkpoint)
	} else if method != "task.export" {
		var approved any
		if t.Approved {
			approved = t.BehaviorDigest
		}
		var plan any
		if t.PlanDigest != "" {
			plan = t.PlanDigest
		}
		if patchID != "" {
			if _, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO source_snapshots VALUES(?,?)", t.Candidate, t.Candidate); e != nil {
				return nil, e
			}
		}
		res, err := tx.ExecContext(ctx, "UPDATE tasks SET state=?,revision=?,candidate_id=?,spec_digest=?,behavior_digest=?,plan_digest=?,approved_digest=?,checkpoint=? WHERE id=? AND revision=?", t.State, t.Revision, t.Candidate, t.SpecDigest, t.BehaviorDigest, plan, approved, t.Checkpoint, t.ID, *p.Revision)
		e = err
		if e == nil {
			n, err := res.RowsAffected()
			e = err
			if n != 1 {
				return nil, domain.Err("REVISION_CONFLICT", "task changed before commit")
			}
		}
	}
	if e != nil {
		if strings.Contains(e.Error(), "UNIQUE constraint failed: tasks.alias") {
			return nil, domain.Err("ALIAS_CONFLICT", "change_id already exists")
		}
		return nil, e
	}
	if method == "task.create" || method == "task.revise" {
		if _, e = tx.ExecContext(ctx, "INSERT INTO task_specs VALUES(?,?,?)", t.ID, t.Spec.Revision, t.SpecDigest); e != nil {
			return nil, e
		}
	}
	if method == "human.review" {
		d, err := s.saveObject(map[string]any{"actor": p.Actor, "input_digest": p.Digest, "verdict": "PASS"})
		if err != nil {
			return nil, err
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO reviews VALUES(?,?,?,?,?,?,?,?)", uniqueID(), t.ID, p.Digest, "PASS", p.Actor, "interactive-human", d, now()); e != nil {
			return nil, e
		}
	}
	if method == "human.requirements" {
		if _, e = tx.ExecContext(ctx, "UPDATE tasks SET requirements_freshness=? WHERE id=?", t.RequirementsFreshness, t.ID); e != nil {
			return nil, e
		}
		d, err := s.saveObject(map[string]any{"actor": p.Actor, "behavior_digest": p.Digest})
		if err != nil {
			return nil, err
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO task_history VALUES(?,?,?,?,?)", uniqueID(), t.ID, "human.requirements", d, now()); e != nil {
			return nil, e
		}
	}
	if decision {
		if _, e = tx.ExecContext(ctx, "INSERT INTO decisions VALUES(?,?,?,?,?,?,?)", uniqueID(), t.ID, t.BehaviorDigest, t.SpecDigest, p.Actor, "interactive-cli", time.Now().UTC().Format(time.RFC3339Nano)); e != nil {
			return nil, e
		}
	}
	if patchID != "" {
		if _, e = tx.ExecContext(ctx, "INSERT INTO patches VALUES(?,?,?,?,?)", patchID, t.ID, patchBase, t.Candidate, patchManifest); e != nil {
			return nil, e
		}
	}
	resultJSON, e := json.Marshal(output)
	if e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO operations VALUES(?,?,?,?)", p.Operation, method, payloadDigest, string(resultJSON)); e != nil {
		return nil, e
	}
	event, _ := json.Marshal(map[string]any{"task_id": t.ID, "revision": t.Revision, "operation_id": p.Operation, "candidate_snapshot_id": t.Candidate})
	if _, e = tx.ExecContext(ctx, "INSERT INTO events(type,payload,occurred_at) VALUES(?,?,?)", method, string(event), time.Now().UTC().Format(time.RFC3339Nano)); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return output, nil
}
func (s *Service) diff(ctx context.Context, t domain.Task) (map[string]any, error) {
	a, e := s.manifest(ctx, t.Baseline)
	if e != nil {
		return nil, e
	}
	b, e := s.manifest(ctx, t.Candidate)
	if e != nil {
		return nil, e
	}
	diff, files, e := patches.Diff(ctx, a, b, s.Workspace.Objects)
	if e != nil {
		return nil, e
	}
	digest, e := s.Workspace.Objects.Put([]byte(diff))
	if e != nil {
		return nil, e
	}
	required := []string{}
	if t.Plan != nil {
		required = t.Plan.Checks
	}
	return map[string]any{"required_checks": required, "plan_digest": t.PlanDigest, "acceptance_criteria": t.Spec.Criteria, "approval_required": t.Kind == "change", "behavior_approved": t.Approved, "task_id": t.ID, "revision": t.Revision, "baseline_snapshot_id": t.Baseline, "candidate_snapshot_id": t.Candidate, "spec_digest": t.SpecDigest, "behavior_digest": t.BehaviorDigest, "freshness": t.Freshness, "release_readiness": t.Readiness, "checks": t.CheckSummary, "changed_paths": files, "patch": diff, "patch_digest": digest, "base_manifest_digest": t.Baseline, "candidate_manifest_digest": t.Candidate, "base_commit": a.Commit, "build": a.Build, "files": changedFiles(a, b, files), "warning": "See current check summary; incomplete exports are diagnostic."}, nil
}
func (s *Service) validateEvidence(ctx context.Context, path string, plan domain.Plan) error {
	db, e := sql.Open("sqlite3", indexReadDSN(path))
	if e != nil {
		return e
	}
	defer db.Close()
	for _, step := range plan.Steps {
		for _, id := range step.Evidence {
			var n int
			e = db.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM symbols WHERE evidence=?)+(SELECT count(*) FROM refs WHERE evidence=?)+(SELECT count(*) FROM chunks WHERE evidence=?)+(SELECT count(*) FROM source_files WHERE evidence=?)", id, id, id, id).Scan(&n)
			if e != nil {
				return e
			}
			if n == 0 {
				return domain.Err("EVIDENCE_NOT_FOUND", "plan evidence absent from baseline generation")
			}
		}
	}
	return nil
}
func (s *Service) typeGuard(ctx context.Context, path string) error {
	db, e := sql.Open("sqlite3", indexReadDSN(path))
	if e != nil {
		return e
	}
	defer db.Close()
	var count int
	if e = db.QueryRowContext(ctx, "SELECT type_errors FROM analysis_meta").Scan(&count); e != nil {
		return domain.Err("INDEX_UPGRADE_REQUIRED", "rebuild with current indexer")
	}
	if count != 0 {
		return domain.Err("TYPE_COVERAGE_GAP", "baseline has %d package load/type errors", count)
	}
	return nil
}

// ApproveHuman is intentionally absent from the general tool/RPC method dispatcher.
// The CLI invokes this dedicated local route only after confirmation through /dev/tty.
// This separates workflows; it does not authenticate a human against another process owned by the same OS user.
func (s *Service) ApproveHuman(ctx context.Context, p Params) (any, error) {
	if p.Schema != domain.API {
		return nil, domain.Err("SCHEMA_INVALID", "API version")
	}
	return s.workflow(ctx, "human.approve", p)
}

func (s *Service) DecideHuman(ctx context.Context, method string, p Params) (any, error) {
	if p.Schema != domain.API {
		return nil, domain.Err("SCHEMA_INVALID", "API version")
	}
	if method != "human.approve" && method != "human.review" && method != "human.requirements" {
		return nil, domain.Err("METHOD_NOT_FOUND", "human decision")
	}
	return s.workflow(ctx, method, p)
}
func indexReadDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	return u.String() + "?mode=ro&immutable=1"
}

func changedFiles(a, b domain.Manifest, paths []string) []map[string]any {
	before := map[string]domain.File{}
	after := map[string]domain.File{}
	for _, f := range a.Files {
		before[f.Path] = f
	}
	for _, f := range b.Files {
		after[f.Path] = f
	}
	result := []map[string]any{}
	for _, p := range paths {
		var old, new any
		if f, ok := before[p]; ok {
			old = f
		}
		if f, ok := after[p]; ok {
			new = f
		}
		result = append(result, map[string]any{"path": p, "before": old, "after": new})
	}
	return result
}
