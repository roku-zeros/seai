package changespec

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/workspace"
)

func Validate(s domain.ChangeSpec) error {
	if s.Schema != "seai.change/v1" || !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,127}$`).MatchString(s.Alias) || s.Revision < 1 || strings.TrimSpace(s.Objective) == "" || len(s.Objective) > 8192 {
		return domain.Err("SCHEMA_INVALID", "ChangeSpec version, change_id, revision and objective are required")
	}
	if s.Requirements == nil || s.Scope.Files == nil || s.Expected == nil || s.Preserved == nil || s.Criteria == nil || s.Unresolved == nil || len(s.Requirements) > 32 || len(s.Expected) > 200 || len(s.Preserved) > 200 || len(s.Criteria) > 200 || len(s.Unresolved) > 200 {
		return domain.Err("SCHEMA_INVALID", "all contract collections must be explicit arrays; collection limits exceeded")
	}
	if s.Kind != "change" && s.Kind != "refactor" && s.Kind != "explain" {
		return domain.Err("SCHEMA_INVALID", "unsupported task kind")
	}
	paths := map[string]bool{}
	if len(s.Scope.Files) > 200 {
		return domain.Err("SCHEMA_INVALID", "scope <=200 explicit files")
	}
	for _, p := range s.Scope.Files {
		if !workspace.ValidSourcePath(p) || p == ".git" || strings.HasPrefix(p, ".git/") || p == ".seai" || strings.HasPrefix(p, ".seai/") {
			return domain.Err("INVALID_PATH", "invalid scope path %q", p)
		}
		if paths[p] {
			return domain.Err("SCHEMA_INVALID", "duplicate scope path")
		}
		paths[p] = true
	}
	ids := map[string]bool{}
	for _, list := range [][]domain.Behavior{s.Expected, s.Preserved} {
		for _, b := range list {
			if b.ID == "" || len(b.ID) > 128 || ids[b.ID] || strings.TrimSpace(b.Statement) == "" || len(b.Statement) > 8192 {
				return domain.Err("SCHEMA_INVALID", "behavior IDs/statements required and unique")
			}
			ids[b.ID] = true
		}
	}
	criteria := map[string]bool{}
	for _, c := range s.Criteria {
		if c.ID == "" || criteria[c.ID] || c.Expected == "" || c.Profile == "" || len(c.BehaviorIDs) == 0 {
			return domain.Err("SCHEMA_INVALID", "criterion ID, behavior_ids, expected and assertion_profile required")
		}
		criteria[c.ID] = true
		for _, id := range c.BehaviorIDs {
			if !ids[id] {
				return domain.Err("SCHEMA_INVALID", "criterion references unknown behavior %s", id)
			}
		}
	}
	for id := range ids {
		covered := false
		for _, c := range s.Criteria {
			for _, ref := range c.BehaviorIDs {
				if ref == id {
					covered = true
				}
			}
		}
		if !covered && s.Kind != "explain" {
			return domain.Err("SPEC_NOT_READY", "behavior %s has no acceptance criterion", id)
		}
	}
	for _, r := range s.Requirements {
		digest := strings.TrimPrefix(r.Digest, "sha256:")
		raw, e := hex.DecodeString(digest)
		if r.ID == "" || r.Revision == "" || e != nil || len(raw) != 32 || r.Content == "" || len(r.Content) > 65536 || domain.Digest([]byte(r.Content)) != digest {
			return domain.Err("SCHEMA_INVALID", "requirement needs external_id, revision and SHA-256 content_digest")
		}
	}
	if s.Kind != "explain" {
		if len(paths) == 0 || len(s.Unresolved) > 0 || len(s.Preserved) == 0 || len(s.Criteria) == 0 {
			return domain.Err("SPEC_NOT_READY", "explicit file scope, preserved behavior, criteria and no unresolved items required")
		}
		if s.Kind == "change" && (len(s.Expected) == 0 || len(s.Requirements) == 0) {
			return domain.Err("SPEC_NOT_READY", "change requires expected behavior and revisioned requirements")
		}
	}
	return nil
}
func Digests(s domain.ChangeSpec) (string, string, error) {
	b, e := domain.Canonical(s)
	if e != nil {
		return "", "", e
	}
	behavior, e := domain.Canonical(map[string]any{"kind": s.Kind, "objective": s.Objective, "source_requirements": s.Requirements, "scope": s.Scope, "expected_behavior": s.Expected, "preserved_behavior": s.Preserved, "acceptance_criteria": s.Criteria, "unresolved": s.Unresolved})
	return domain.Digest(b), domain.Digest(behavior), e
}
func ValidatePlan(p domain.Plan, s domain.ChangeSpec) error {
	if p.Schema != "seai.plan/v1" || len(p.Steps) == 0 || len(p.Steps) > 12 || len(p.Checks) == 0 {
		return domain.Err("SCHEMA_INVALID", "plan needs 1..12 steps and required_checks")
	}
	scope := map[string]bool{}
	for _, f := range s.Scope.Files {
		scope[f] = true
	}
	criteria := map[string]bool{}
	for _, c := range s.Criteria {
		criteria[c.ID] = true
	}
	steps := map[string]bool{}
	for _, step := range p.Steps {
		if step.Files == nil || step.Criteria == nil || step.ID == "" || steps[step.ID] || step.Description == "" || len(step.Evidence) == 0 || len(step.Evidence) > 32 {
			return domain.Err("SCHEMA_INVALID", "step IDs/descriptions required and unique")
		}
		steps[step.ID] = true
		for _, f := range step.Files {
			if !scope[f] {
				return domain.Err("SCOPE_VIOLATION", "plan path %s not in scope", f)
			}
		}
		for _, id := range step.Criteria {
			if !criteria[id] {
				return domain.Err("SCHEMA_INVALID", "unknown criterion %s", id)
			}
		}
		for _, ev := range step.Evidence {
			if len(ev) != 64 {
				return domain.Err("SCHEMA_INVALID", "evidence digest required")
			}
		}
	}
	for _, c := range s.Criteria {
		found := false
		for _, step := range p.Steps {
			for _, id := range step.Criteria {
				if id == c.ID {
					found = true
				}
			}
		}
		if !found {
			return domain.Err("SPEC_NOT_READY", "criterion %s has no plan step", c.ID)
		}
	}
	for _, check := range p.Checks {
		if strings.TrimSpace(check) == "" {
			return fmt.Errorf("empty check profile")
		}
	}
	return nil
}
