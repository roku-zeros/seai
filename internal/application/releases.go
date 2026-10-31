package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/indexer"
	"seai.local/seai/internal/patches"
	"seai.local/seai/internal/reconcile"
)

func (s *Service) release(ctx context.Context, method string, p Params) (any, error) {
	if method == "release.inspect" {
		var d string
		if e := s.Store.DB.QueryRowContext(ctx, "SELECT digest FROM release_plans WHERE id=?", p.ID).Scan(&d); e != nil {
			return nil, e
		}
		b, e := s.Workspace.Objects.Get(d)
		return json.RawMessage(b), e
	}
	if method == "task.rebase" {
		return s.startRun(ctx, method, p, func(c context.Context, run string, _ domain.Task) (any, error) { return s.rebase(c, run, p) })
	}
	return s.startRun(ctx, method, p, func(c context.Context, run string, _ domain.Task) (any, error) {
		pin, e := s.resolve(c, "")
		if e != nil {
			return nil, e
		}
		if method == "release.plan" {
			if p.Ref == "" || p.ReleaseID == "" || len(p.ReleaseID) > 128 {
				return nil, domain.Err("SCHEMA_INVALID", "ref and release_id required")
			}
			m, id, e := s.Workspace.Capture(c, p.Ref)
			if e != nil {
				return nil, e
			}
			old, e := s.manifest(c, pin.Snapshot)
			if e != nil {
				return nil, e
			}
			plan := domain.ReleasePlan{ID: uniqueID(), ReleaseID: p.ReleaseID, Ref: p.Ref, Commit: m.Commit, Source: id, FromSource: pin.Snapshot, FromGeneration: pin.Generation, Epoch: pin.Epoch, Extractor: indexer.Version, Paths: reconcile.Paths(old, m), Tasks: []string{}}
			rows, e := s.Store.DB.QueryContext(c, "SELECT id FROM tasks ORDER BY id")
			if e != nil {
				return nil, e
			}
			for rows.Next() {
				var id string
				if e = rows.Scan(&id); e != nil {
					rows.Close()
					return nil, e
				}
				plan.Tasks = append(plan.Tasks, id)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return nil, e
			}
			d, e := s.saveObject(plan)
			if e != nil {
				return nil, e
			}
			_, e = s.Store.DB.ExecContext(c, "INSERT INTO release_plans VALUES(?,?,?,'PLANNED',?)", plan.ID, d, plan.Epoch, plan.ReleaseID)
			return plan, e
		}
		var plan domain.ReleasePlan
		if method == "release.rollback" {
			var source, commit string
			if e = s.Store.DB.QueryRowContext(c, "SELECT source_id,commit_sha FROM releases WHERE release_id=?", p.ReleaseID).Scan(&source, &commit); e != nil {
				return nil, e
			}
			plan = domain.ReleasePlan{ID: uniqueID(), ReleaseID: p.ReleaseID, Source: source, Commit: commit, Epoch: pin.Epoch, FromGeneration: pin.Generation, Extractor: indexer.Version}
		} else {
			var d string
			if e = s.Store.DB.QueryRowContext(c, "SELECT digest FROM release_plans WHERE id=? AND state='PLANNED'", p.ID).Scan(&d); e != nil {
				return nil, e
			}
			b, e := s.Workspace.Objects.Get(d)
			if e != nil {
				return nil, e
			}
			if e = json.Unmarshal(b, &plan); e != nil {
				return nil, e
			}
		}
		if plan.Epoch != pin.Epoch || plan.FromGeneration != pin.Generation || plan.Extractor != indexer.Version {
			return nil, domain.Err("STALE_RELEASE_PLAN", "replan against current epoch and extractor")
		}
		m, e := s.loadManifest(plan.Source)
		if e != nil {
			return nil, e
		}
		generation, path, coverage, e := indexer.Build(c, s.Data, s.Workspace.Objects, m, plan.Source)
		if e != nil {
			return nil, e
		}
		if c.Err() != nil {
			return nil, c.Err()
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		tx, e := s.Store.DB.BeginTx(c, nil)
		if e != nil {
			return nil, e
		}
		defer tx.Rollback()
		var state string
		var lease int
		if e = tx.QueryRowContext(c, "SELECT state,lease_epoch FROM runs WHERE id=?", run).Scan(&state, &lease); e != nil || state != "RUNNING" || lease != 1 {
			return nil, domain.Err("LEASE_LOST", "release cancelled")
		}
		if _, e = tx.ExecContext(c, "INSERT OR IGNORE INTO source_snapshots VALUES(?,?)", plan.Source, plan.Source); e != nil {
			return nil, e
		}
		if _, e = tx.ExecContext(c, "INSERT OR IGNORE INTO index_generations VALUES(?,?,?,?,'READY')", generation, plan.Source, path, coverage); e != nil {
			return nil, e
		}
		r, e := tx.ExecContext(c, "UPDATE projects SET current_generation_id=?,epoch=epoch+1 WHERE epoch=? AND current_generation_id=?", generation, pin.Epoch, pin.Generation)
		if e != nil {
			return nil, e
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			return nil, domain.Err("REVISION_CONFLICT", "project changed")
		}
		var existingSource string
		aliasErr := tx.QueryRowContext(c, "SELECT source_id FROM releases WHERE release_id=?", plan.ReleaseID).Scan(&existingSource)
		if aliasErr == nil && existingSource != plan.Source {
			return nil, domain.Err("RELEASE_ID_CONFLICT", "release ID already identifies another snapshot")
		}
		if aliasErr != nil && aliasErr != sql.ErrNoRows {
			return nil, aliasErr
		}
		if _, e = tx.ExecContext(c, "INSERT OR IGNORE INTO releases VALUES(?,?,?,?,?,?)", uniqueID(), plan.ReleaseID, plan.Source, generation, plan.Commit, now()); e != nil {
			return nil, e
		}
		if _, e = tx.ExecContext(c, "UPDATE project_control SET active_release_id=? WHERE id=1", plan.ReleaseID); e != nil {
			return nil, e
		}
		if _, e = tx.ExecContext(c, "UPDATE tasks SET reconciliation_required=1,requirements_freshness='not_checked',revision=revision+1"); e != nil {
			return nil, e
		}
		if _, e = tx.ExecContext(c, "UPDATE release_plans SET state='APPLIED' WHERE id=?", plan.ID); e != nil {
			return nil, e
		}
		if _, e = tx.ExecContext(c, "INSERT INTO events(type,payload,occurred_at) VALUES('RELEASE_SWITCHED',?,?)", plan.Source, now()); e != nil {
			return nil, e
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
		return map[string]any{"release_id": plan.ReleaseID, "source_snapshot_id": plan.Source, "index_generation_id": generation, "epoch": pin.Epoch + 1, "reconciliation_required": true, "checkout_changed": false}, nil
	})
}
func (s *Service) loadManifest(id string) (domain.Manifest, error) {
	var m domain.Manifest
	b, e := s.Workspace.Objects.Get(id)
	if e != nil {
		return m, e
	}
	e = json.Unmarshal(b, &m)
	if e == nil {
		actual, err := m.ID()
		if err != nil {
			return m, err
		}
		if actual != id {
			return m, domain.Err("STORAGE_CORRUPT", "manifest ID differs")
		}
	}
	return m, e
}
func (s *Service) rebase(ctx context.Context, run string, p Params) (any, error) {
	t, e := s.getTask(ctx, s.Store.DB, p.Task)
	if e != nil {
		return nil, e
	}
	if p.Revision == nil || *p.Revision != t.Revision {
		return nil, domain.Err("REVISION_CONFLICT", "task revision differs")
	}
	pin, e := s.resolve(ctx, "")
	if e != nil {
		return nil, e
	}
	if t.Baseline == pin.Snapshot && !t.Reconciliation {
		return nil, domain.Err("INVALID_STATE", "task already on current release")
	}
	base, e := s.manifest(ctx, t.Baseline)
	if e != nil {
		return nil, e
	}
	ours, e := s.manifest(ctx, t.Candidate)
	if e != nil {
		return nil, e
	}
	target, e := s.manifest(ctx, pin.Snapshot)
	if e != nil {
		return nil, e
	}
	clean, _, e := s.Workspace.Capture(ctx, base.Commit)
	if e != nil {
		return nil, e
	}
	dirty := len(reconcile.Paths(clean, base)) > 0
	if dirty {
		if p.Overlay != "task-only" && p.Overlay != "carry" {
			return nil, domain.Err("OVERLAY_POLICY_REQUIRED", "dirty baseline: choose task-only or carry explicitly")
		}
		if p.Overlay == "carry" {
			base = clean
		}
	}
	staging := filepath.Join(s.Data, "staging")
	if e = os.MkdirAll(staging, 0700); e != nil {
		return nil, e
	}
	merged, conflicts, e := reconcile.Merge(ctx, base, ours, target, s.Workspace.Objects, staging)
	if e != nil {
		return nil, e
	}
	intermediate, e := s.saveObject(merged)
	if e != nil {
		return nil, e
	}
	if _, e = s.Store.DB.ExecContext(ctx, "INSERT OR IGNORE INTO source_snapshots VALUES(?,?)", intermediate, intermediate); e != nil {
		return nil, e
	}
	candidate := intermediate
	if len(conflicts) > 0 && p.Resolution != nil {
		covered := map[string]bool{}
		for _, op := range p.Resolution.Files {
			covered[op.Path] = true
		}
		for _, conflict := range conflicts {
			if !covered[conflict.Path] {
				return nil, domain.Err("RESOLUTION_INCOMPLETE", "resolve %s", conflict.Path)
			}
		}
		scope := append([]string{}, t.Spec.Scope.Files...)
		for _, conflict := range conflicts {
			scope = append(scope, conflict.Path)
		}
		_, candidate, e = patches.Apply(merged, intermediate, t.SpecDigest, *p.Resolution, scope, s.Workspace.Objects)
		if e != nil {
			return nil, e
		}
	} else if p.Resolution != nil {
		return nil, domain.Err("INVALID_STATE", "no conflicts to resolve")
	}
	result := map[string]any{"task_id": t.ID, "target_release_snapshot_id": pin.Snapshot, "resolution_base_snapshot_id": intermediate, "conflicts": conflicts, "overlay_policy": p.Overlay, "dirty_baseline": dirty, "applied": false}
	d, e := s.saveObject(result)
	if e != nil {
		return nil, e
	}
	if len(conflicts) > 0 && p.Resolution == nil {
		_, e = s.Store.DB.ExecContext(ctx, "INSERT INTO rebase_attempts VALUES(?,?,?,?,?,?)", uniqueID(), t.ID, pin.Snapshot, intermediate, d, now())
		return result, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var state string
	if e = tx.QueryRowContext(ctx, "SELECT state FROM runs WHERE id=? AND lease_epoch=1", run).Scan(&state); e != nil || state != "RUNNING" {
		return nil, domain.Err("LEASE_LOST", "rebase cancelled")
	}
	if _, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO source_snapshots VALUES(?,?)", candidate, candidate); e != nil {
		return nil, e
	}
	r, e := tx.ExecContext(ctx, "UPDATE tasks SET baseline_id=?,candidate_id=?,baseline_epoch=?,revision=revision+1,reconciliation_required=0,requirements_freshness='not_checked',plan_digest=NULL,state='SPEC_READY',checkpoint='SPEC_READY',provenance_digest=? WHERE id=? AND revision=?", pin.Snapshot, candidate, pin.Epoch, d, t.ID, t.Revision)
	if e != nil {
		return nil, e
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return nil, domain.Err("REVISION_CONFLICT", "task changed")
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO task_history VALUES(?,?,?,?,?)", uniqueID(), t.ID, "release.rebase", d, now()); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	result["applied"] = true
	result["candidate_snapshot_id"] = candidate
	return result, nil
}
