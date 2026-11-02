package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"seai.local/seai/internal/domain"
)

type researchInput struct {
	Objective  string `json:"objective"`
	Topic      string `json:"topic"`
	Generation string `json:"index_generation_id"`
	Snapshot   string `json:"source_snapshot_id"`
	Workers    int    `json:"max_workers"`
}

func (s *Service) researchWorkers() int {
	n := s.Config.ResearchMaxWorkers
	if n == 0 {
		n = 2
	}
	return n
}
func (s *Service) startResearch(ctx context.Context, p Params) (any, error) {
	if p.Research == nil || strings.TrimSpace(p.Research.Objective) == "" || len(p.Research.Objective) > 8192 || len(p.Research.Topics) < 1 || len(p.Research.Topics) > 6 {
		return nil, domain.Err("SCHEMA_INVALID", "research needs objective (1..8192 bytes), 1..6 explicit topics")
	}
	seen := map[string]bool{}
	for _, topic := range p.Research.Topics {
		if strings.TrimSpace(topic) == "" || len(topic) > 2048 || seen[topic] {
			return nil, domain.Err("SCHEMA_INVALID", "research topics must be nonempty, unique and <=2048 bytes")
		}
		seen[topic] = true
	}
	if p.Generation == "" || p.Generation == "current" {
		return nil, domain.Err("SCHEMA_INVALID", "research requires an explicit immutable index_generation_id")
	}
	if p.Task != "" {
		return nil, domain.Err("SCHEMA_INVALID", "research sessions are independent of change tasks")
	}
	pin, e := s.resolve(ctx, p.Generation)
	if e != nil {
		return nil, e
	}
	workers := min(s.researchWorkers(), len(p.Research.Topics))
	return s.startRun(ctx, "research.session", p, func(c context.Context, run string, _ domain.Task) (any, error) {
		defer func() {
			state := "ABORTED"
			if c.Err() != nil {
				state = "CANCELLED"
			}
			_, _ = s.Store.DB.Exec("UPDATE execution_nodes SET state=?,lease_epoch=lease_epoch+1,error='research session ended before report' WHERE run_id=? AND state IN ('QUEUED','RUNNING')", state, run)
		}()
		jobs := []map[string]any{}
		for i, topic := range p.Research.Topics {
			input := researchInput{p.Research.Objective, topic, pin.Generation, pin.Snapshot, workers}
			digest, e := s.saveObject(input)
			if e != nil {
				return nil, e
			}
			id := uniqueID()
			if _, e = s.Store.DB.ExecContext(c, "INSERT INTO execution_nodes(id,run_id,name,role,state,input_digest,attempt,lease_epoch) VALUES(?,?,?,'researcher','QUEUED',?,0,1)", id, run, topic, digest); e != nil {
				return nil, e
			}
			jobs = append(jobs, map[string]any{"job_id": id, "topic": topic, "order": i})
		}
		packet := map[string]any{"run_id": run, "objective": p.Research.Objective, "index_generation_id": pin.Generation, "source_snapshot_id": pin.Snapshot, "max_workers": workers, "jobs": jobs, "mode": "gigacode-external-workers", "freshness": "NOT_CHECKED"}
		d, e := s.saveObject(packet)
		if e != nil {
			return nil, e
		}
		r, e := s.Store.DB.ExecContext(c, "UPDATE runs SET result_digest=? WHERE id=? AND state='RUNNING' AND lease_epoch=1", d, run)
		if e != nil {
			return nil, e
		}
		if n, _ := r.RowsAffected(); n != 1 {
			return nil, domain.Err("LEASE_LOST", "research session cancelled before initialization")
		}
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			var pending int
			if e = s.Store.DB.QueryRowContext(c, "SELECT count(*) FROM execution_nodes WHERE run_id=? AND state IN ('QUEUED','RUNNING')", run).Scan(&pending); e != nil {
				return packet, e
			}
			if pending == 0 {
				status, e := s.researchStatus(c, Params{ID: run})
				if e != nil {
					return packet, e
				}
				rows := status.(map[string]any)["jobs"].([]map[string]any)
				packet["reports"] = rows
				packet["review_required"] = true
				packet["aggregation"] = "Reports preserve each topic and evidence; coordinator must reconcile overlap, contradictions and gaps. SUCCEEDED means collected, not exhaustive coverage."
				gaps := false
				for _, job := range rows {
					if job["state"] != "SUCCEEDED" {
						gaps = true
					}
				}
				if gaps {
					return packet, domain.Err("NEEDS_INPUT", "research reports contain gaps; inspect per-job reports")
				}
				return packet, nil
			}
			select {
			case <-c.Done():
				return packet, c.Err()
			case <-ticker.C:
			}
		}
	})
}

func (s *Service) researchStatus(ctx context.Context, p Params) (any, error) {
	if p.WaitMS < 0 || p.WaitMS > 10000 {
		return nil, domain.Err("SCHEMA_INVALID", "research wait_ms must be 0..10000")
	}
	deadline := time.Now().Add(time.Duration(p.WaitMS) * time.Millisecond)
	for {
		value, e := s.researchStatusOnce(ctx, p)
		if e != nil {
			return nil, e
		}
		out := value.(map[string]any)
		raw, e := domain.Canonical(out)
		if e != nil {
			return nil, e
		}
		cursor := domain.Digest(raw)
		out["cursor"] = cursor
		if p.Cursor == "" || p.Cursor != cursor {
			out["unchanged"] = false
			return out, nil
		}
		if p.WaitMS == 0 || time.Now().After(deadline) || out["state"] != "RUNNING" {
			return map[string]any{"run_id": p.ID, "state": out["state"], "cursor": cursor, "unchanged": true, "error": out["error"]}, nil
		}
		timer := time.NewTimer(min(200*time.Millisecond, time.Until(deadline)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Service) researchStatusOnce(ctx context.Context, p Params) (any, error) {
	var kind, state, failure string
	var result sql.NullString
	if e := s.Store.DB.QueryRowContext(ctx, "SELECT kind,state,result_digest,coalesce(error,'') FROM runs WHERE id=?", p.ID).Scan(&kind, &state, &result, &failure); e != nil {
		if e == sql.ErrNoRows {
			return nil, domain.Err("NOT_FOUND", "research run not found")
		}
		return nil, e
	}
	if kind != "research.session" {
		return nil, domain.Err("INVALID_STATE", "run is not a research session")
	}
	rows, e := s.Store.DB.QueryContext(ctx, "SELECT id,name,state,attempt,lease_epoch,input_digest,coalesce(result_digest,''),coalesce(error,'') FROM execution_nodes WHERE run_id=? ORDER BY rowid", p.ID)
	if e != nil {
		return nil, e
	}
	jobs := []map[string]any{}
	for rows.Next() {
		var id, topic, status, input, digest, message string
		var attempt, lease int
		if e = rows.Scan(&id, &topic, &status, &attempt, &lease, &input, &digest, &message); e != nil {
			rows.Close()
			return nil, e
		}
		job := map[string]any{"job_id": id, "topic": topic, "state": status, "attempt": attempt, "lease_epoch": lease, "error": message}
		if b, e := s.Workspace.Objects.Get(input); e == nil {
			var source researchInput
			if err := json.Unmarshal(b, &source); err != nil {
				rows.Close()
				return nil, err
			}
			job["index_generation_id"] = source.Generation
			job["source_snapshot_id"] = source.Snapshot
		} else {
			rows.Close()
			return nil, e
		}
		if digest != "" {
			b, e := s.Workspace.Objects.Get(digest)
			if e != nil {
				rows.Close()
				return nil, e
			}
			job["report"] = json.RawMessage(b)
		}
		jobs = append(jobs, job)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := map[string]any{"run_id": p.ID, "state": state, "jobs": jobs, "initializing": !result.Valid, "error": failure, "freshness": "NOT_CHECKED"}
	if result.Valid {
		b, e := s.Workspace.Objects.Get(result.String)
		if e != nil {
			return nil, e
		}
		var metadata map[string]any
		if e = json.Unmarshal(b, &metadata); e != nil {
			return nil, e
		}
		delete(metadata, "reports") // reports are already returned once in jobs
		out["result"] = metadata
	}
	return out, nil
}

func (s *Service) researchControl(ctx context.Context, method string, p Params) (any, error) {
	if p.Operation == "" || len(p.Operation) > 128 {
		return nil, domain.Err("SCHEMA_INVALID", "operation_id required (max 128 bytes)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	payload, e := domain.Canonical(p)
	if e != nil {
		return nil, e
	}
	payloadDigest := domain.Digest(payload)
	var oldMethod, oldDigest, oldResult string
	e = s.Store.DB.QueryRowContext(ctx, "SELECT method,payload_digest,result_json FROM operations WHERE operation_id=?", p.Operation).Scan(&oldMethod, &oldDigest, &oldResult)
	if e == nil {
		if oldMethod != method || oldDigest != payloadDigest {
			return nil, domain.Err("IDEMPOTENCY_CONFLICT", "operation ID binds another request")
		}
		return json.RawMessage(oldResult), nil
	}
	if e != sql.ErrNoRows {
		return nil, e
	}
	var kind, state string
	if e = s.Store.DB.QueryRowContext(ctx, "SELECT kind,state FROM runs WHERE id=?", p.ID).Scan(&kind, &state); e != nil {
		return nil, domain.Err("NOT_FOUND", "research session not found")
	}
	if kind != "research.session" || state != "RUNNING" {
		return nil, domain.Err("INVALID_STATE", "active research session required")
	}
	if method == "research.claim" && p.ResearchJob == "" {
		e = s.Store.DB.QueryRowContext(ctx, "SELECT id FROM execution_nodes WHERE run_id=? AND state='QUEUED' ORDER BY rowid LIMIT 1", p.ID).Scan(&p.ResearchJob)
		if e == sql.ErrNoRows {
			return nil, domain.Err("NO_PENDING_JOB", "all jobs are claimed or complete; inspect research.get")
		}
		if e != nil {
			return nil, e
		}
	}
	var inputDigest, nodeState string
	var lease int
	e = s.Store.DB.QueryRowContext(ctx, "SELECT input_digest,state,lease_epoch FROM execution_nodes WHERE id=? AND run_id=?", p.ResearchJob, p.ID).Scan(&inputDigest, &nodeState, &lease)
	if e != nil {
		return nil, domain.Err("NOT_FOUND", "research job does not belong to this session")
	}
	b, e := s.Workspace.Objects.Get(inputDigest)
	if e != nil {
		return nil, e
	}
	var input researchInput
	if e = json.Unmarshal(b, &input); e != nil {
		return nil, e
	}
	var result map[string]any
	var resultDigest string
	targetState := "RUNNING"
	switch method {
	case "research.claim":
		if nodeState != "QUEUED" {
			return nil, domain.Err("INVALID_STATE", "job already claimed or complete")
		}
		var running int
		if e = s.Store.DB.QueryRowContext(ctx, "SELECT count(*) FROM execution_nodes WHERE run_id=? AND state='RUNNING'", p.ID).Scan(&running); e != nil {
			return nil, e
		}
		if running >= input.Workers {
			return nil, domain.Err("RESEARCH_BUSY", "parallel research limit %d reached; wait for a report", input.Workers)
		}
		lease++
		result = map[string]any{"run_id": p.ID, "job_id": p.ResearchJob, "lease_epoch": lease, "input": input, "instructions": "Use explicit input generation for every SEAI read. Report evidence_ids and gaps through research.report; no source edits, approvals or task mutations. Claim another job only after reporting this one."}
	case "research.report":
		if nodeState != "RUNNING" || p.Lease != lease {
			return nil, domain.Err("LEASE_LOST", "report requires the current claimed job lease")
		}
		if p.Report == nil || strings.TrimSpace(p.Report.Summary) == "" || len(p.Report.Summary) > 8192 || p.Report.Findings == nil || p.Report.Gaps == nil || len(p.Report.Findings) > 30 || len(p.Report.Gaps) > 30 {
			return nil, domain.Err("SCHEMA_INVALID", "report needs summary <=8192 bytes and explicit findings/gaps arrays, each <=30")
		}
		if len(p.Report.Findings) == 0 && len(p.Report.Gaps) == 0 {
			return nil, domain.Err("EVIDENCE_REQUIRED", "empty research needs an explicit gap; absence is not proof")
		}
		pin, e := s.resolve(ctx, input.Generation)
		if e != nil {
			return nil, e
		}
		if pin.Snapshot != input.Snapshot {
			return nil, domain.Err("SNAPSHOT_MISMATCH", "research input differs from indexed snapshot")
		}
		count := 0
		for _, finding := range p.Report.Findings {
			if strings.TrimSpace(finding.Statement) == "" || len(finding.Statement) > 4096 || len(finding.Evidence) == 0 || len(finding.Evidence) > 20 {
				return nil, domain.Err("SCHEMA_INVALID", "finding needs statement <=4096 bytes and 1..20 evidence_ids")
			}
			count += len(finding.Evidence)
			if count > 100 {
				return nil, domain.Err("LIMIT_EXCEEDED", "report evidence_ids <=100")
			}
			if e = s.validateEvidence(ctx, pin.Path, domain.Plan{Steps: []domain.PlanStep{{ID: p.ResearchJob, Evidence: finding.Evidence}}}); e != nil {
				return nil, e
			}
		}
		for _, gap := range p.Report.Gaps {
			if strings.TrimSpace(gap) == "" || len(gap) > 4096 {
				return nil, domain.Err("SCHEMA_INVALID", "gap must be nonempty and <=4096 bytes")
			}
		}
		resultDigest, e = s.saveObject(p.Report)
		if e != nil {
			return nil, e
		}
		targetState = "SUCCEEDED"
		if len(p.Report.Gaps) > 0 {
			targetState = "NEEDS_INPUT"
		}
		result = map[string]any{"run_id": p.ID, "job_id": p.ResearchJob, "lease_epoch": lease, "state": targetState, "report_digest": resultDigest}
	default:
		return nil, domain.Err("METHOD_NOT_FOUND", "research control method")
	}
	raw, e := json.Marshal(result)
	if e != nil {
		return nil, e
	}
	tx, e := s.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var updated sql.Result
	if method == "research.claim" {
		updated, e = tx.ExecContext(ctx, "UPDATE execution_nodes SET state='RUNNING',attempt=attempt+1,lease_epoch=? WHERE id=? AND run_id=? AND state='QUEUED' AND EXISTS(SELECT 1 FROM runs WHERE id=? AND state='RUNNING' AND lease_epoch=1)", lease, p.ResearchJob, p.ID, p.ID)
	} else {
		updated, e = tx.ExecContext(ctx, "UPDATE execution_nodes SET state=?,result_digest=? WHERE id=? AND run_id=? AND state='RUNNING' AND lease_epoch=? AND EXISTS(SELECT 1 FROM runs WHERE id=? AND state='RUNNING' AND lease_epoch=1)", targetState, resultDigest, p.ResearchJob, p.ID, lease, p.ID)
	}
	if e != nil {
		return nil, e
	}
	if n, _ := updated.RowsAffected(); n != 1 {
		return nil, domain.Err("LEASE_LOST", "research publication was cancelled or superseded")
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO operations VALUES(?,?,?,?)", p.Operation, method, payloadDigest, string(raw)); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return result, nil
}

func (s *Service) cancelResearch(ctx context.Context, p Params) (any, error) {
	var kind string
	if e := s.Store.DB.QueryRowContext(ctx, "SELECT kind FROM runs WHERE id=?", p.ID).Scan(&kind); e != nil {
		return nil, e
	}
	if kind != "research.session" {
		return nil, domain.Err("INVALID_STATE", "only research sessions can be cancelled here")
	}
	return s.advanced(ctx, "runs.cancel", p)
}

func researchNeedsInput(method string, e error) bool {
	var de *domain.Error
	return method == "research.session" && errors.As(e, &de) && de.Code == "NEEDS_INPUT"
}
