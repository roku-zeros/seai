package checks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"seai.local/seai/internal/contracts"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/storage"
)

func ParseJSON(b []byte) (any, error) {
	var raw json.RawMessage
	if e := contracts.StrictJSON(b, &raw); e != nil {
		return nil, e
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	e := d.Decode(&value)
	return value, e
}
func escape(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }
func Differences(a, b any) []domain.Difference {
	result := []domain.Difference{}
	var walk func(any, any, bool, bool, string)
	walk = func(x, y any, xok, yok bool, path string) {
		if xok && yok {
			switch left := x.(type) {
			case map[string]any:
				if right, ok := y.(map[string]any); ok {
					keys := map[string]bool{}
					for k := range left {
						keys[k] = true
					}
					for k := range right {
						keys[k] = true
					}
					sorted := []string{}
					for k := range keys {
						sorted = append(sorted, k)
					}
					sort.Strings(sorted)
					for _, k := range sorted {
						xx, xa := left[k]
						yy, ya := right[k]
						walk(xx, yy, xa, ya, path+"/"+escape(k))
					}
					return
				}
			case []any:
				if right, ok := y.([]any); ok {
					for i := 0; i < max(len(left), len(right)); i++ {
						var xx, yy any
						xa, ya := i < len(left), i < len(right)
						if xa {
							xx = left[i]
						}
						if ya {
							yy = right[i]
						}
						walk(xx, yy, xa, ya, fmt.Sprintf("%s/%d", path, i))
					}
					return
				}
			case json.Number:
				if right, ok := y.(json.Number); ok {
					l, lo := new(big.Rat).SetString(string(left))
					r, ro := new(big.Rat).SetString(string(right))
					if lo && ro && l.Cmp(r) == 0 {
						return
					}
				}
			default:
				xb, _ := json.Marshal(x)
				yb, _ := json.Marshal(y)
				if bytes.Equal(xb, yb) {
					return
				}
			}
		}
		d := domain.Difference{Path: path, BeforePresent: xok, AfterPresent: yok, Classification: "unexpected"}
		if xok {
			d.Before, _ = json.Marshal(x)
		}
		if yok {
			d.After, _ = json.Marshal(y)
		}
		result = append(result, d)
	}
	walk(a, b, true, true, "")
	return result
}
func compare(ctx context.Context, p domain.CheckProfile, base, candidate domain.Manifest, spec domain.ChangeSpec, cas storage.CAS, parent string) (r domain.CheckResult) {
	r = domain.CheckResult{ProfileID: p.ID, Status: "PASSED", ExitCode: 0, Tests: []string{}, Criteria: []string{}, Differences: []domain.Difference{}, FixtureDigest: FixtureDigest(p), Isolation: p.Isolation}
	logs := []map[string]any{}
	defer func() { encoded, _ := json.Marshal(logs); r.StdoutDigest, _ = cas.Put(encoded) }()
	coverage := map[string]bool{}
	for _, fixture := range p.Fixtures {
		values := []any{}
		for _, m := range []domain.Manifest{base, base, candidate, candidate} {
			pr := Execute(ctx, p, m, cas, parent, fixture.Input)
			out, oe := cas.Put(pr.Stdout)
			errlog, ee := cas.Put(pr.Stderr)
			if oe != nil || ee != nil {
				r.Status = "FAILED"
				r.Error = "cannot persist comparison logs"
				return r
			}
			logs = append(logs, map[string]any{"fixture_id": fixture.ID, "stdout_digest": out, "stderr_digest": errlog, "status": pr.Status, "exit_code": pr.Exit})
			if pr.Status != "PASSED" {
				r.Status = pr.Status
				r.ExitCode = pr.Exit
				r.Error = pr.Error
				return r
			}
			value, e := ParseJSON(pr.Stdout)
			if e != nil {
				r.Status = "FAILED"
				r.Error = "comparison output must be one valid exact JSON value: " + e.Error()
				return r
			}
			values = append(values, value)
		}
		if len(Differences(values[0], values[1])) > 0 || len(Differences(values[2], values[3])) > 0 {
			r.Status = "NON_REPRODUCIBLE"
			r.Error = "repeated baseline/candidate outputs differ"
			return r
		}
		expected, e := ParseJSON(fixture.Expected)
		if e != nil {
			r.Status = "FAILED"
			r.Error = e.Error()
			return r
		}
		if diff := Differences(expected, values[2]); len(diff) > 0 {
			for i := range diff {
				diff[i].Fixture = fixture.ID
				diff[i].Classification = "acceptance_failed"
			}
			r.Differences = append(r.Differences, diff...)
			r.Status = "FAILED"
		}
		delta := Differences(values[0], values[2])
		for i := range delta {
			delta[i].Fixture = fixture.ID
			allowed := false
			for _, path := range fixture.AllowedPaths {
				if delta[i].Path == path {
					allowed = true
				}
			}
			if spec.Kind == "refactor" {
				allowed = false
			}
			if allowed {
				delta[i].Classification = "expected"
			} else {
				r.Status = "FAILED"
			}
			r.Differences = append(r.Differences, delta[i])
		}
		for _, id := range fixture.Criteria {
			for _, c := range spec.Criteria {
				if c.ID == id && c.Profile == p.AssertionProfile && c.FixtureDigest == r.FixtureDigest {
					coverage[id] = true
				}
			}
		}
	}
	for _, criterion := range spec.Criteria {
		if coverage[criterion.ID] {
			r.Criteria = append(r.Criteria, criterion.ID)
		}
	}
	if r.Status == "PASSED" && len(r.Criteria) == 0 {
		r.Status = "FAILED"
		r.Error = "comparison has no contract-bound criterion fixtures"
	}
	return r
}
