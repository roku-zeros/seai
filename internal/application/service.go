// Package application owns use cases and runtime transaction boundaries.
package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/indexer"
	"seai.local/seai/internal/storage"
	"seai.local/seai/internal/workspace"
	"sync"
	"time"
)

type Service struct {
	Store     *storage.Runtime
	Workspace workspace.Service
	Data      string
	mu        sync.Mutex
	wg        sync.WaitGroup
	busy      bool
}
type Params struct {
	Schema    string `json:"schema_version"`
	Operation string `json:"operation_id,omitempty"`
	Epoch     *int   `json:"expected_project_epoch,omitempty"`
	Ref       string `json:"ref,omitempty"`
	Job       string `json:"job_id,omitempty"`
	Query     string `json:"query,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}
type Job struct {
	ID     string          `json:"job_id"`
	State  string          `json:"state"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func (s *Service) Wait() { s.wg.Wait() }
func (s *Service) Handle(ctx context.Context, method string, p Params) (any, error) {
	if p.Schema != domain.API {
		return nil, domain.Err("SCHEMA_INVALID", "schema_version must be %s", domain.API)
	}
	switch method {
	case "handshake":
		return map[string]string{"schema_version": domain.API, "server_version": "0.1.0-dev"}, nil
	case "capabilities.get":
		return map[string]any{"structural_search": true, "snapshot_modes": []string{"working_tree", "explicit_ref"}, "provider_ready": false, "multi_agent_ready": false, "pilot_ready": false, "blocking_issues": []string{"P0 Qwen and GigaCode profiles are unverified", "domain patterns and verification isolation are unconfigured", "change/release/upgrade workflows are not implemented"}, "limitations": indexer.Limitations}, nil
	case "project.get":
		var id, root string
		var gen sql.NullString
		var epoch int
		e := s.Store.DB.QueryRowContext(ctx, "SELECT id,root,current_generation_id,epoch FROM projects").Scan(&id, &root, &gen, &epoch)
		return map[string]any{"project_id": id, "root": root, "current_generation_id": gen.String, "epoch": epoch}, e
	case "index.build":
		return s.start(ctx, p)
	case "jobs.get":
		var j Job
		var result, errtext sql.NullString
		e := s.Store.DB.QueryRowContext(ctx, "SELECT id,state,result,error FROM jobs WHERE id=?", p.Job).Scan(&j.ID, &j.State, &result, &errtext)
		if e == sql.ErrNoRows {
			return nil, domain.Err("NOT_FOUND", "job not found")
		}
		j.Result = json.RawMessage(result.String)
		j.Error = errtext.String
		return j, e
	case "search.entities":
		if p.Query == "" || len(p.Query) > 1024 || p.Limit < 0 || p.Limit > 50 {
			return nil, domain.Err("SCHEMA_INVALID", "query required (max 1024 bytes), limit 1..50")
		}
		if p.Limit == 0 {
			p.Limit = 50
		}
		var gen, snapshot, path, coverage string
		e := s.Store.DB.QueryRowContext(ctx, "SELECT g.id,g.snapshot_id,g.path,g.coverage FROM projects p JOIN index_generations g ON g.id=p.current_generation_id WHERE g.state='READY'").Scan(&gen, &snapshot, &path, &coverage)
		if e == sql.ErrNoRows {
			return nil, domain.Err("NOT_READY", "build an index first")
		}
		if e != nil {
			return nil, e
		}
		hits, truncated, e := indexer.Search(ctx, path, p.Query, p.Limit, s.Workspace.Objects)
		if e != nil {
			return nil, e
		}
		diagnostics, e := indexer.Diagnostics(ctx, path)
		if e != nil {
			return nil, e
		}
		return domain.SearchResult{Snapshot: snapshot, Generation: gen, Coverage: coverage, Hits: hits, Truncated: truncated, Limitations: diagnostics}, e
	default:
		return nil, domain.Err("METHOD_NOT_FOUND", "unsupported method %q", method)
	}
}
func (s *Service) start(ctx context.Context, p Params) (any, error) {
	if p.Operation == "" || len(p.Operation) > 128 || p.Epoch == nil || *p.Epoch < 0 {
		return nil, domain.Err("SCHEMA_INVALID", "operation_id and expected_project_epoch are required")
	}
	payload, _ := domain.Canonical(p)
	digest := domain.Digest(payload)
	s.mu.Lock()
	defer s.mu.Unlock()
	var prev, state string
	e := s.Store.DB.QueryRowContext(ctx, "SELECT payload_digest,state FROM jobs WHERE id=?", p.Operation).Scan(&prev, &state)
	if e == nil {
		if prev != digest {
			return nil, domain.Err("IDEMPOTENCY_CONFLICT", "operation_id already has another payload")
		}
		return map[string]string{"job_id": p.Operation, "state": state}, nil
	}
	if e != sql.ErrNoRows {
		return nil, e
	}
	if s.busy {
		return nil, domain.Err("WORKFLOW_BUSY", "one index job is already active")
	}
	var epoch int
	if e = s.Store.DB.QueryRowContext(ctx, "SELECT epoch FROM projects").Scan(&epoch); e != nil {
		return nil, e
	}
	if epoch != *p.Epoch {
		return nil, domain.Err("REVISION_CONFLICT", "expected project epoch %d, actual %d", *p.Epoch, epoch)
	}
	if _, e = s.Store.DB.ExecContext(ctx, "INSERT INTO jobs(id,payload_digest,state) VALUES(?,?,'QUEUED')", p.Operation, digest); e != nil {
		return nil, e
	}
	s.busy = true
	s.wg.Add(1)
	// Work is detached from the short RPC request. Daemon shutdown waits for completion.
	go func() {
		defer s.wg.Done()
		defer func() { s.mu.Lock(); s.busy = false; s.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_, e := s.Store.DB.Exec("UPDATE jobs SET state='RUNNING' WHERE id=?", p.Operation)
		var m domain.Manifest
		var snapshot, id, path, coverage string
		if e == nil {
			m, snapshot, e = s.Workspace.Capture(ctx, p.Ref)
		}
		if e == nil {
			id, path, coverage, e = indexer.Build(ctx, s.Data, s.Workspace.Objects, m, snapshot)
		}
		if e == nil {
			e = s.Store.Publish(s.Workspace.Project, id, snapshot, path, coverage, epoch, p.Operation)
		}
		if e != nil {
			_, _ = s.Store.DB.Exec("UPDATE jobs SET state='FAILED',error=? WHERE id=?", e.Error(), p.Operation)
		}
	}()
	return map[string]string{"job_id": p.Operation, "state": "QUEUED"}, nil
}
func ValidateLocations(repo, data string) (string, string, error) {
	root, e := filepath.Abs(repo)
	if e != nil {
		return "", "", e
	}
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		return "", "", e
	}
	b, e := workspace.Git(context.Background(), root, "rev-parse", "--show-toplevel")
	if e != nil {
		return "", "", e
	}
	top := string(b)
	for len(top) > 0 && (top[len(top)-1] == '\n' || top[len(top)-1] == '\r') {
		top = top[:len(top)-1]
	}
	top, e = filepath.EvalSymlinks(top)
	if e != nil {
		return "", "", e
	}
	if top != root {
		return "", "", domain.Err("INVALID_ROOT", "repository root must be %s", top)
	}
	d, e := filepath.Abs(data)
	if e != nil {
		return "", "", e
	}
	// Resolve existing ancestors before creating anything so data cannot alias source.
	ancestor := d
	suffix := []string{}
	for {
		_, e = os.Lstat(ancestor)
		if e == nil {
			break
		}
		if !os.IsNotExist(e) {
			return "", "", e
		}
		suffix = append(suffix, filepath.Base(ancestor))
		next := filepath.Dir(ancestor)
		if next == ancestor {
			return "", "", fmt.Errorf("invalid data directory")
		}
		ancestor = next
	}
	resolved, e := filepath.EvalSymlinks(ancestor)
	if e != nil {
		return "", "", e
	}
	for i := len(suffix) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, suffix[i])
	}
	d = resolved
	rel, e := filepath.Rel(root, d)
	if e != nil {
		return "", "", e
	}
	if rel == "." || rel != ".." && len(rel) > 0 && !startsParent(rel) {
		return "", "", domain.Err("INVALID_DATA_ROOT", "runtime must be outside indexed repository")
	}
	return root, d, nil
}
func startsParent(rel string) bool {
	return len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)
}
