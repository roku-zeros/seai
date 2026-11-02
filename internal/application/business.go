package application

import (
	"context"
	"encoding/json"
	"seai.local/seai/internal/business"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/indexer"
	"sort"
)

func (s *Service) businessQuery(ctx context.Context, method string, p Params) (any, error) {
	if p.Offset < 0 || p.Limit < 0 || p.Limit > 500 || p.Depth < 0 || p.Depth > 8 || len(p.Query) > 1024 || len(p.Context) > 16 || p.Snapshot != "" {
		return nil, domain.Err("SCHEMA_INVALID", "business query bounds or snapshot; use --generation for indexed snapshots")
	}
	if (method == "rules.get" || method == "flow.get" || method == "impact.get") && p.Query == "" {
		return nil, domain.Err("SCHEMA_INVALID", "rule key or symbol required")
	}
	pin, e := s.resolve(ctx, p.Generation)
	if e != nil {
		return nil, e
	}
	current := p.Generation == "" || p.Generation == "current"
	if current {
		if e = s.guard(ctx, pin); e != nil {
			return nil, e
		}
	}
	db, e := business.Open(pin.Path)
	if e != nil {
		return nil, e
	}
	defer db.Close()
	graph, e := business.Load(ctx, db)
	if e != nil {
		return nil, e
	}
	if current && graph.ConfigDigest != business.Digest(s.Config.Business) {
		return nil, domain.Err("STALE_DOMAIN_CONFIG", "configured adapters changed; rebuild index before business queries")
	}
	limit := p.Limit
	if limit == 0 {
		limit = 50
	}
	out := map[string]any{}
	switch method {
	case "rules.search":
		if limit > 50 {
			return nil, domain.Err("SCHEMA_INVALID", "rules search limit <=50")
		}
		out = business.Rules(graph, p.Query, p.Context, p.Offset, limit)
	case "rules.get":
		rules, e := business.ResolveRules(graph, p.Query, p.Context)
		if e != nil {
			return nil, e
		}
		if len(rules) == 0 {
			return nil, domain.Err("NOT_FOUND", "rule key/entity not found")
		}
		facts := []domain.BusinessFact{}
		owners := map[string]bool{}
		for _, h := range rules[0].Handlers {
			owners[h.Symbol] = true
		}
		for _, f := range graph.Facts {
			if owners[f.Owner] {
				facts = append(facts, f)
			}
		}
		lo := min(p.Offset, len(facts))
		hi := min(lo+limit, len(facts))
		out = map[string]any{"rule": rules[0], "source_facts": facts[lo:hi], "next_offset": hi, "truncated": hi < len(facts), "limitations": graph.Limitations, "coverage": "partial"}
	case "rules.discover":
		lo := min(p.Offset, len(graph.Discovery))
		hi := min(lo+limit, len(graph.Discovery))
		out = map[string]any{"candidates": graph.Discovery[lo:hi], "next_offset": hi, "truncated": hi < len(graph.Discovery), "configured": graph.Configured, "suggestions_are_not_rules": true}
	case "rules.diagnostics":
		lo := min(p.Offset, len(graph.Diagnostics))
		hi := min(lo+limit, len(graph.Diagnostics))
		out = map[string]any{"diagnostics": graph.Diagnostics[lo:hi], "next_offset": hi, "truncated": hi < len(graph.Diagnostics), "configured": graph.Configured, "rule_count": len(graph.Rules), "coverage": "partial", "config_digest": graph.ConfigDigest, "limitations": graph.Limitations}
	case "flow.get", "impact.get":
		depth := p.Depth
		if depth == 0 {
			depth = 3
		}
		out, e = business.Traverse(ctx, db, graph, p.Query, p.Context, method == "impact.get", depth, limit)
		if e != nil {
			return nil, e
		}
		profiles := []string{}
		for name, profile := range s.Profiles {
			if profile.Kind == "compile" || profile.Kind == "tests" {
				profiles = append(profiles, name)
				continue
			}
			needed := false
			raw, _ := json.Marshal(out["affected_rules"])
			for _, f := range profile.Fixtures {
				for _, key := range f.RuleKeys {
					var rules []domain.BusinessRule
					_ = json.Unmarshal(raw, &rules)
					for _, r := range rules {
						if r.Key == key {
							needed = true
						}
					}
				}
			}
			if needed {
				profiles = append(profiles, name)
			}
		}
		sort.Strings(profiles)
		out["suggested_checks"] = profiles
	}
	out["source_snapshot_id"] = pin.Snapshot
	out["index_generation_id"] = pin.Generation
	out["historical_or_explicit"] = !current
	if current {
		if e = s.guard(ctx, pin); e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (s *Service) businessGate(ctx context.Context, path string, scope []string) error {
	if s.Config.Business == nil || !s.Config.Business.RequireForChanges {
		return nil
	}
	db, e := business.Open(path)
	if e != nil {
		return e
	}
	defer db.Close()
	g, e := business.Load(ctx, db)
	if e != nil {
		return e
	}
	if g.ConfigDigest != business.Digest(s.Config.Business) {
		return domain.Err("STALE_DOMAIN_CONFIG", "rebuild domain index")
	}
	paths := map[string]bool{}
	for _, p := range scope {
		paths[p] = true
	}
	affected, gaps, err := business.AffectedRules(ctx, db, g, scope)
	if err != nil {
		return err
	}
	if len(gaps) > 0 {
		return domain.Err("DOMAIN_COVERAGE_GAP", "%s", gaps[0])
	}
	for _, r := range affected {
		paths[r.Evidence.Path] = true
	}
	for _, d := range g.Diagnostics {
		if d.Blocking && (d.Evidence.Path == "" || paths[d.Evidence.Path]) {
			return domain.Err("DOMAIN_COVERAGE_GAP", "%s: %s (%s)", d.Code, d.Message, d.Evidence.Path)
		}
	}
	// A configured adapter must match at least one registration; otherwise success is empty coverage.
	if len(g.Rules) == 0 {
		return domain.Err("DOMAIN_COVERAGE_GAP", "no configured rules were extracted; inspect rules diagnostics/discover")
	}
	return nil
}

func (s *Service) verifyBusiness(ctx context.Context, run string, t domain.Task, candidate domain.Manifest) (domain.CheckResult, error) {
	result := domain.CheckResult{ProfileID: "domain-analysis", Status: "RUNNING", ExitCode: 0, Isolation: "static-analysis", Tests: []string{}, Criteria: []string{}, Differences: []domain.Difference{}}
	id := uniqueID()
	if _, e := s.Store.DB.ExecContext(ctx, "INSERT INTO check_runs VALUES(?,?,?,?,?,?,?,?,?,?,?,NULL,?)", id, run, t.ID, t.Candidate, t.Baseline, t.SpecDigest, t.PlanDigest, s.ConfigDigest, result.ProfileID, s.fingerprint(t), "RUNNING", now()); e != nil {
		return result, e
	}
	generation, path, coverage, e := indexer.Build(ctx, s.Data, s.Workspace.Objects, candidate, t.Candidate, s.Config.Business)
	if e == nil {
		db, err := business.Open(path)
		if err != nil {
			e = err
		} else {
			graph, err := business.Load(ctx, db)
			db.Close()
			if err != nil {
				e = err
			} else {
				paths := map[string]bool{}
				for _, p := range t.Spec.Scope.Files {
					paths[p] = true
				}
				verification := &domain.BusinessVerification{Source: t.Candidate, Generation: generation, RuleIDs: []string{}, Diagnostics: []domain.BusinessDiagnostic{}, Gaps: []string{}, Coverage: "partial"}
				result.Domain = verification
				if len(graph.Rules) == 0 {
					verification.Gaps = append(verification.Gaps, "configured adapters extracted no candidate rules")
				}

				// File scope is deliberately conservative: it includes all rules declared in or using scoped handlers.
				index, e2 := business.Open(path)
				if e2 != nil {
					e = e2
				} else {
					affected, impactGaps, impactErr := business.AffectedRules(ctx, index, graph, t.Spec.Scope.Files)
					if impactErr != nil {
						e = impactErr
					}
					verification.Gaps = append(verification.Gaps, impactGaps...)
					// Removed registrations still need regression fixtures for their previous context.
					pin, pinErr := s.resolve(ctx, "")
					if pinErr != nil {
						e = pinErr
					} else {
						baselineDB, openErr := business.Open(pin.Path)
						if openErr != nil {
							e = openErr
						} else {
							baselineGraph, loadErr := business.Load(ctx, baselineDB)
							if loadErr != nil {
								e = loadErr
							} else {
								baselineAffected, baselineGaps, walkErr := business.AffectedRules(ctx, baselineDB, baselineGraph, t.Spec.Scope.Files)
								if walkErr != nil {
									e = walkErr
								}
								verification.Gaps = append(verification.Gaps, baselineGaps...)
								key := func(r domain.BusinessRule) string {
									b, _ := domain.Canonical(map[string]any{"key": r.Key, "context": r.Context, "adapter": r.Adapter})
									return string(b)
								}
								remaining := map[string]int{}
								for _, r := range affected {
									remaining[key(r)]++
								}
								for _, r := range baselineAffected {
									k := key(r)
									if remaining[k] > 0 {
										remaining[k]--
										continue
									}
									affected = append(affected, r)
									verification.RemovedRules = append(verification.RemovedRules, business.BriefRule(r))
								}
							}
							baselineDB.Close()
						}
					}
					for _, rule := range affected {
						paths[rule.Evidence.Path] = true
						verification.RuleIDs = append(verification.RuleIDs, rule.ID)
						covered := false
						for _, name := range t.Plan.Checks {
							profile, ok := s.Profiles[name]
							if !ok || profile.Kind != "comparison" {
								continue
							}
							for _, fixture := range profile.Fixtures {
								keyMatch := false
								for _, key := range fixture.RuleKeys {
									if key == rule.Key {
										keyMatch = true
									}
								}
								contextMatch := true
								for key, value := range rule.Context {
									if fixture.Context[key] != value {
										contextMatch = false
									}
								}
								if keyMatch && contextMatch {
									covered = true
								}
							}
						}
						if !covered {
							verification.Gaps = append(verification.Gaps, "no business fixture in accepted plan for affected rule "+rule.Key+" / "+rule.ID)
						}
					}
					index.Close()
				}
				for _, d := range graph.Diagnostics {
					if d.Blocking && (d.Evidence.Path == "" || paths[d.Evidence.Path]) {
						verification.Diagnostics = append(verification.Diagnostics, d)
						verification.Gaps = append(verification.Gaps, d.Code+": "+d.Message)
					}
				}
				if e == nil && len(verification.Gaps) > 0 {
					e = domain.Err("DOMAIN_COVERAGE_GAP", "candidate has %d business coverage gaps; inspect domain-analysis report", len(verification.Gaps))
				}
			}
		}
	}
	result.Status = "PASSED"
	if e != nil {
		result.Status = "FAILED"
		result.Error = e.Error()
	}
	digest, saveErr := s.saveObject(result)
	if saveErr != nil {
		return result, saveErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.getTask(ctx, s.Store.DB, t.ID)
	var state string
	var lease int
	fenceErr := s.Store.DB.QueryRowContext(ctx, "SELECT state,lease_epoch FROM runs WHERE id=?", run).Scan(&state, &lease)
	if err != nil || fenceErr != nil || state != "RUNNING" || lease != 1 || current.Revision != t.Revision {
		result.Status = "ABORTED"
		result.Error = "execution lease or task revision changed"
		e = domain.Err("LEASE_LOST", "%s", result.Error)
		digest, saveErr = s.saveObject(result)
	}
	if saveErr != nil {
		return result, saveErr
	}
	if _, err = s.Store.DB.ExecContext(context.Background(), "UPDATE check_runs SET status=?,result_digest=? WHERE id=?", result.Status, digest, id); err != nil {
		return result, err
	}
	if result.Status == "PASSED" {
		if _, err = s.Store.DB.ExecContext(ctx, "INSERT OR IGNORE INTO index_generations VALUES(?,?,?,?,'READY')", generation, t.Candidate, path, coverage); err != nil {
			return result, err
		}
	}
	return result, e
}
