package checks

import (
	"encoding/json"
	"fmt"
	"seai.local/seai/internal/domain"
	"sort"
	"strconv"
	"strings"
)

func validCategory(s string) bool {
	switch s {
	case "normal", "boundary", "exception", "combination", "regression":
		return true
	}
	return false
}
func pointer(v any, path string) (any, bool) {
	if path == "" {
		return v, true
	}
	if !strings.HasPrefix(path, "/") {
		return nil, false
	}
	for _, part := range strings.Split(path[1:], "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch x := v.(type) {
		case map[string]any:
			var ok bool
			v, ok = x[part]
			if !ok {
				return nil, false
			}
		case []any:
			i, e := strconv.Atoi(part)
			if e != nil || i < 0 || i >= len(x) || strconv.Itoa(i) != part {
				return nil, false
			}
			v = x[i]
		default:
			return nil, false
		}
	}
	return v, true
}
func traceRules(v any, path string) ([]string, error) {
	value, ok := pointer(v, path)
	if !ok {
		return nil, fmt.Errorf("rule trace path absent: %s", path)
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("rule trace must be an array of string IDs")
	}
	out := []string{}
	for _, item := range items {
		s, ok := item.(string)
		if !ok || s == "" {
			return nil, fmt.Errorf("rule trace contains invalid ID")
		}
		out = append(out, s)
	}
	return out, nil
}
func includes(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
func sameContext(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
func validateBusiness(p domain.CheckProfile) error {
	if p.Kind != "comparison" && (len(p.RequiredCases) > 0 || p.RuleTracePath != "") {
		return domain.Err("SCHEMA_INVALID", "business coverage needs comparison profile")
	}
	if len(p.RequiredCases) > 100 {
		return domain.Err("SCHEMA_INVALID", "business coverage limit")
	}
	if p.RuleTracePath != "" && !strings.HasPrefix(p.RuleTracePath, "/") {
		return domain.Err("SCHEMA_INVALID", "rule_trace_path must be JSON pointer")
	}
	for _, f := range p.Fixtures {
		if f.Category != "" && !validCategory(f.Category) {
			return domain.Err("SCHEMA_INVALID", "unknown business fixture category")
		}
		if len(f.RuleKeys) > 32 || len(f.Context) > 16 || len(f.Description) > 4096 {
			return domain.Err("SCHEMA_INVALID", "business fixture metadata limits")
		}
		if len(f.BaselineExpected) > 0 {
			if len(f.BaselineExpected) > 65536 {
				return domain.Err("SCHEMA_INVALID", "expected_baseline size")
			}
			if _, e := ParseJSON(f.BaselineExpected); e != nil {
				return e
			}
		}
		if p.RuleTracePath != "" {
			if len(f.RuleKeys) == 0 {
				return domain.Err("SCHEMA_INVALID", "trace verification requires fixture rule_keys")
			}
			expected, e := ParseJSON(f.Expected)
			if e != nil {
				return e
			}
			rules, e := traceRules(expected, p.RuleTracePath)
			if e != nil {
				return e
			}
			for _, key := range f.RuleKeys {
				if !includes(rules, key) {
					return domain.Err("SCHEMA_INVALID", "expected trace does not contain fixture rule %s", key)
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, req := range p.RequiredCases {
		raw, _ := json.Marshal(map[string]any{"key": req.RuleKey, "context": req.Context})
		key := string(raw)
		if req.RuleKey == "" || seen[key] || len(req.Categories) == 0 || len(req.Context) > 16 {
			return domain.Err("SCHEMA_INVALID", "unique rule/context with required categories needed")
		}
		seen[key] = true
		categories := map[string]bool{}
		for _, category := range req.Categories {
			if !validCategory(category) || categories[category] {
				return domain.Err("SCHEMA_INVALID", "invalid/duplicate required case category")
			}
			categories[category] = true
			found := false
			for _, f := range p.Fixtures {
				if includes(f.RuleKeys, req.RuleKey) && f.Category == category && sameContext(f.Context, req.Context) {
					found = true
				}
			}
			if !found {
				return domain.Err("BUSINESS_CASE_MISSING", "no %s fixture for rule %s/context", category, req.RuleKey)
			}
		}
	}
	return nil
}
func businessCoverage(p domain.CheckProfile, cases []domain.BusinessCaseResult) []domain.BusinessCaseCoverage {
	out := []domain.BusinessCaseCoverage{}
	for _, req := range p.RequiredCases {
		c := domain.BusinessCaseCoverage{RuleKey: req.RuleKey, Context: req.Context, Required: req.Categories, Passed: []string{}, Missing: []string{}, TraceVerified: p.RuleTracePath != ""}
		for _, category := range req.Categories {
			found := false
			for _, f := range cases {
				if f.Status == "PASSED" && f.Category == category && includes(f.RuleKeys, req.RuleKey) && sameContext(f.Context, req.Context) && (p.RuleTracePath == "" || f.TraceVerified) {
					found = true
				}
			}
			if found {
				c.Passed = append(c.Passed, category)
			} else {
				c.Missing = append(c.Missing, category)
			}
		}
		c.TraceVerified = p.RuleTracePath != "" && len(c.Missing) == 0
		sort.Strings(c.Passed)
		sort.Strings(c.Missing)
		out = append(out, c)
	}
	return out
}
