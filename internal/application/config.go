package application

import (
	"encoding/json"
	"os"
	"seai.local/seai/internal/business"
	"seai.local/seai/internal/checks"
	"seai.local/seai/internal/contracts"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/providers/qwen"
	"time"
)

func (s *Service) Configure(path string) error {
	c := domain.RuntimeConfig{Schema: "seai.runtime/v1", MaxWorkers: 2, MaxNodeAttempts: 1, MaxRunSeconds: 1800, Checks: []domain.CheckProfile{}}
	if path != "" {
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if len(b) > 1048576 {
			return domain.Err("SCHEMA_INVALID", "config too large")
		}
		if e = contracts.StrictJSON(b, &c); e != nil {
			return e
		}
	}
	if c.Schema != "seai.runtime/v1" || c.MaxWorkers < 1 || c.MaxWorkers > 2 || c.MaxNodeAttempts < 1 || c.MaxNodeAttempts > 2 || c.MaxRunSeconds < 1 || c.MaxRunSeconds > 7200 {
		return domain.Err("SCHEMA_INVALID", "runtime configuration bounds invalid")
	}
	if c.ResearchMaxWorkers < 0 || c.ResearchMaxWorkers > 4 {
		return domain.Err("SCHEMA_INVALID", "research_max_workers must be 0 (default 2) or 1..4")
	}
	if c.FreshnessBudgetSeconds < 0 || c.FreshnessBudgetSeconds > 1800 {
		return domain.Err("SCHEMA_INVALID", "freshness_budget_seconds must be 0 (default) or 1..1800")
	}
	if e := business.Validate(c.Business); e != nil {
		return e
	}
	profiles := map[string]domain.CheckProfile{}
	for _, p := range c.Checks {
		if p.ID == "domain-analysis" {
			return domain.Err("SCHEMA_INVALID", "domain-analysis is reserved for candidate business verification")
		}
		if e := checks.Validate(p); e != nil {
			return e
		}
		if _, ok := profiles[p.ID]; ok {
			return domain.Err("SCHEMA_INVALID", "duplicate check profile")
		}
		profiles[p.ID] = p
	}
	if c.Provider != nil {
		p, e := qwen.New(*c.Provider)
		if e != nil {
			return e
		}
		s.Provider = p
	}
	b, e := json.Marshal(c)
	if e != nil {
		return e
	}
	s.FreshnessBudget = DefaultFreshnessBudget
	if c.FreshnessBudgetSeconds > 0 {
		s.FreshnessBudget = time.Duration(c.FreshnessBudgetSeconds) * time.Second
	}
	s.Config = c
	s.ConfigDigest = domain.Digest(b)
	s.Profiles = profiles
	return nil
}
