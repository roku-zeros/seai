package application

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"seai.local/seai/internal/business"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/indexer"
	"seai.local/seai/internal/workspace"
)

type pinned struct {
	Generation, Snapshot, Path, Coverage string
	Epoch                                int
}

func (s *Service) resolve(ctx context.Context, generation string) (pinned, error) {
	var p pinned
	var e error
	if generation == "" || generation == "current" {
		e = s.Store.DB.QueryRowContext(ctx, "SELECT g.id,g.snapshot_id,g.path,g.coverage,p.epoch FROM projects p JOIN index_generations g ON g.id=p.current_generation_id WHERE g.state='READY'").Scan(&p.Generation, &p.Snapshot, &p.Path, &p.Coverage, &p.Epoch)
	} else {
		e = s.Store.DB.QueryRowContext(ctx, "SELECT id,snapshot_id,path,coverage,0 FROM index_generations WHERE id=?", generation).Scan(&p.Generation, &p.Snapshot, &p.Path, &p.Coverage, &p.Epoch)
	}
	if e == sql.ErrNoRows {
		return p, domain.Err("NOT_READY", "no matching published generation")
	}
	return p, e
}

const DefaultFreshnessBudget = 15 * time.Second

func (s *Service) observe(ctx context.Context) (domain.Manifest, string, error) {
	profile, e := indexer.BuildProfile(ctx)
	if e != nil {
		return domain.Manifest{}, "", e
	}
	ws := s.Workspace
	ws.Build = profile
	return ws.Observe(ctx)
}
func (s *Service) live(ctx context.Context) (domain.Manifest, string, error) {
	budget := s.FreshnessBudget
	if budget <= 0 {
		budget = DefaultFreshnessBudget
	}
	check, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	m, id, e := s.observe(check)
	if e != nil && check.Err() != nil && ctx.Err() == nil {
		return domain.Manifest{}, "", &domain.Error{Code: "FRESHNESS_TIMEOUT", Message: fmt.Sprintf("live verification exceeded %s; freshness is unknown, daemon may still be healthy", budget), Retryable: true, SuggestedAction: "Run workspace check (async), or read immutable --generation/--snapshot. Do not retry live scans in a loop or kill seaid."}
	}
	return m, id, e
}
func (s *Service) workspaceCheck(ctx context.Context, p Params) (any, error) {
	return s.startRun(ctx, "workspace.check", p, func(c context.Context, _ string, _ domain.Task) (any, error) {
		pin, e := s.resolve(c, "")
		if e != nil {
			return nil, e
		}
		_, id, e := s.observe(c)
		if e != nil {
			return nil, e
		}
		fresh := "CURRENT"
		if id != pin.Snapshot {
			fresh = "STALE"
		}
		var generation string
		var epoch int
		if e = s.Store.DB.QueryRowContext(c, "SELECT current_generation_id,epoch FROM projects").Scan(&generation, &epoch); e != nil {
			return nil, e
		}
		if generation != pin.Generation || epoch != pin.Epoch {
			return nil, domain.Err("REVISION_CONFLICT", "project changed during workspace check")
		}
		return map[string]any{"freshness": fresh, "active_snapshot_id": pin.Snapshot, "observed_snapshot_id": id, "index_generation_id": pin.Generation, "epoch": pin.Epoch, "read_only": true}, nil
	})
}
func (s *Service) guard(ctx context.Context, p pinned) error {
	_, live, e := s.live(ctx)
	if e != nil {
		return e
	}
	if live != p.Snapshot {
		return domain.Err("STALE_WORKSPACE", "live source/build differs from indexed snapshot; run index build or request an explicit historical generation")
	}
	var epoch int
	var generation string
	if e = s.Store.DB.QueryRowContext(ctx, "SELECT epoch,current_generation_id FROM projects").Scan(&epoch, &generation); e != nil {
		return e
	}
	if epoch != p.Epoch || generation != p.Generation {
		return domain.Err("REVISION_CONFLICT", "index changed during freshness check")
	}
	return nil
}
func (s *Service) manifest(ctx context.Context, id string) (domain.Manifest, error) {
	var digest string
	e := s.Store.DB.QueryRowContext(ctx, "SELECT manifest_digest FROM source_snapshots WHERE id=?", id).Scan(&digest)
	if e == sql.ErrNoRows {
		return domain.Manifest{}, domain.Err("NOT_FOUND", "snapshot is not registered in this project")
	}
	if e != nil {
		return domain.Manifest{}, e
	}
	b, e := s.Workspace.Objects.Get(digest)
	if e != nil {
		return domain.Manifest{}, e
	}
	var m domain.Manifest
	e = json.Unmarshal(b, &m)
	if e != nil {
		return m, e
	}
	computed, e := m.ID()
	if e != nil {
		return m, e
	}
	if computed != id || m.Repository != s.Workspace.Project {
		return m, domain.Err("SNAPSHOT_MISMATCH", "manifest/project mismatch")
	}
	return m, nil
}
func (s *Service) research(ctx context.Context, method string, p Params) (any, error) {
	if p.Offset < 0 || p.Limit < 0 {
		return nil, domain.Err("SCHEMA_INVALID", "invalid offset/limit")
	}
	pin, e := s.resolve(ctx, p.Generation)
	if e != nil {
		return nil, e
	}
	if method == "workspace.status" {
		_, id, e := s.live(ctx)
		if e != nil {
			return nil, e
		}
		fresh := "CURRENT"
		if id != pin.Snapshot {
			fresh = "STALE"
		}
		return map[string]any{"active_snapshot_id": pin.Snapshot, "observed_snapshot_id": id, "index_generation_id": pin.Generation, "epoch": pin.Epoch, "freshness": fresh, "source_policy": workspace.SourcePolicy, "excluded_metadata": workspace.MetadataExclusions()}, nil
	}
	explicitSnapshot := p.Snapshot != ""
	if explicitSnapshot && p.Generation != "" && p.Generation != "current" && p.Snapshot != pin.Snapshot {
		return nil, domain.Err("SNAPSHOT_MISMATCH", "snapshot and generation do not match")
	}
	if (p.Generation == "" || p.Generation == "current") && !explicitSnapshot {
		if e = s.guard(ctx, pin); e != nil {
			return nil, e
		}
	}
	if explicitSnapshot && method != "file.read" && method != "files.list" && p.Snapshot != pin.Snapshot {
		return nil, domain.Err("SNAPSHOT_MISMATCH", "select matching generation for symbol/graph/text search")
	}
	snapshot := pin.Snapshot
	if explicitSnapshot {
		snapshot = p.Snapshot
	}
	envelope := func(data map[string]any) map[string]any {
		data["source_snapshot_id"] = snapshot
		if snapshot == pin.Snapshot {
			data["index_generation_id"] = pin.Generation
		}
		data["historical_or_explicit"] = explicitSnapshot || (p.Generation != "" && p.Generation != "current")
		return data
	}
	switch method {
	case "search.entities":
		if p.Query == "" || len(p.Query) > 1024 || p.Limit > 50 {
			return nil, domain.Err("SCHEMA_INVALID", "query required (max 1024 bytes), limit <=50")
		}
		limit := p.Limit
		if limit == 0 {
			limit = 50
		}
		hits, more, e := indexer.Search(ctx, pin.Path, p.Query, limit, s.Workspace.Objects)
		if e != nil {
			return nil, e
		}
		diagnostics, e := indexer.Diagnostics(ctx, pin.Path)
		businessRules := []domain.BusinessRule{}
		if db, err := business.Open(pin.Path); err == nil {
			if graph, err := business.Load(ctx, db); err == nil {
				if p.Generation != "" && p.Generation != "current" || graph.ConfigDigest == business.Digest(s.Config.Business) {
					for _, rule := range graph.Rules {
						if business.Matches(rule, p.Query, nil) {
							businessRules = append(businessRules, business.BriefRule(rule))
						}
					}
					if len(businessRules) > limit {
						businessRules = businessRules[:limit]
						more = true
					}
					if len(hits)+len(businessRules) > limit {
						hits = hits[:limit-len(businessRules)]
						more = true
					}
				} else {
					diagnostics = append(diagnostics, "business adapters differ from indexed config; rebuild before domain retrieval")
				}
			}
			db.Close()
		}
		return domain.SearchResult{BusinessRules: businessRules, Snapshot: pin.Snapshot, Generation: pin.Generation, Coverage: pin.Coverage, Hits: hits, Truncated: more, Limitations: diagnostics}, e
	case "files.list", "file.read":
		m, e := s.manifest(ctx, snapshot)
		if e != nil {
			return nil, e
		}
		if method == "files.list" {
			limit := p.Limit
			if limit == 0 {
				limit = 100
			}
			if limit > 200 {
				return nil, domain.Err("SCHEMA_INVALID", "file list limit <=200")
			}
			files := []domain.File{}
			for _, f := range m.Files {
				if strings.HasPrefix(f.Path, p.Path) {
					files = append(files, f)
				}
			}
			if p.Offset > len(files) {
				return nil, domain.Err("INVALID_RANGE", "file list offset")
			}
			end := min(p.Offset+limit, len(files))
			return envelope(map[string]any{"files": files[p.Offset:end], "truncated": end < len(files), "next_offset": end}), nil
		}
		if !workspace.ValidPath(p.Path) {
			return nil, domain.Err("INVALID_PATH", "repository-relative normalized path required")
		}
		for _, f := range m.Files {
			if f.Path == p.Path {
				b, e := s.Workspace.Objects.Get(f.Digest)
				if e != nil {
					return nil, e
				}
				if p.Offset > len(b) {
					return nil, domain.Err("INVALID_RANGE", "offset exceeds file length")
				}
				limit := p.Limit
				if limit == 0 {
					limit = 8192
				}
				if limit > 65536 {
					return nil, domain.Err("SCHEMA_INVALID", "read limit <=65536 bytes")
				}
				end := min(p.Offset+limit, len(b))
				part := b[p.Offset:end]
				text := ""
				if utf8.Valid(part) {
					text = string(part)
				}
				return envelope(map[string]any{"file_path": f.Path, "blob_digest": f.Digest, "mode": f.Mode, "size_bytes": len(b), "start_byte": p.Offset, "end_byte": end, "text": text, "content_base64": base64.StdEncoding.EncodeToString(part), "truncated": end < len(b), "next_offset": end, "file_evidence_id": domain.Digest([]byte(f.Digest + ":" + f.Path)), "evidence_id": domain.Digest([]byte(fmt.Sprintf("%s:%s:%d:%d", f.Digest, f.Path, p.Offset, end)))}), nil
			}
		}
		return nil, domain.Err("NOT_FOUND", "file absent from selected snapshot")
	case "search.text":
		limit := p.Limit
		if limit == 0 {
			limit = 25
		}
		if limit > 50 {
			return nil, domain.Err("SCHEMA_INVALID", "text search limit <=50")
		}
		result, e := indexer.TextSearch(ctx, pin.Path, pin.Generation, p.Query, p.Cursor, limit, s.Workspace.Objects)
		if e != nil {
			return nil, e
		}
		return envelope(result), nil
	case "symbol.get":
		limit := p.Limit
		if limit == 0 {
			limit = 8192
		}
		if limit > 65536 {
			return nil, domain.Err("SCHEMA_INVALID", "symbol page <=65536 bytes")
		}
		result, e := indexer.Symbol(ctx, pin.Path, p.Symbol, p.Offset, limit, s.Workspace.Objects)
		if e != nil {
			return nil, e
		}
		return envelope(result), nil
	case "references.find", "calls.list", "tests.related":
		limit := p.Limit
		if limit == 0 {
			limit = 50
		}
		if limit > 200 {
			return nil, domain.Err("SCHEMA_INVALID", "graph page <=200")
		}
		result, e := indexer.References(ctx, pin.Path, p.Symbol, method == "calls.list", method == "tests.related", p.Offset, limit)
		if e != nil {
			return nil, e
		}
		return envelope(result), nil
	}
	return nil, domain.Err("METHOD_NOT_FOUND", "research method")
}
