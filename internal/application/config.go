package application

import (
	"encoding/json"
	"os"
	"seai.local/seai/internal/checks"
	"seai.local/seai/internal/contracts"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/providers/qwen"
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
	profiles := map[string]domain.CheckProfile{}
	for _, p := range c.Checks {
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
	s.Config = c
	s.ConfigDigest = domain.Digest(b)
	s.Profiles = profiles
	return nil
}
