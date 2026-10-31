package checks

import (
	"context"
	"encoding/json"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/storage"
	"testing"
	"time"
)

func localProfile() domain.CheckProfile {
	return domain.CheckProfile{ID: "compare", Kind: "comparison", Program: "/bin/sh", Args: []string{"run.sh"}, Isolation: "local-approved", AcknowledgeLocal: true, TimeoutSeconds: 3, MaxOutputBytes: 4096, AssertionProfile: "exact-v1", Fixtures: []domain.Fixture{{ID: "one", Input: "x", Expected: json.RawMessage(`{"n":9007199254740993,"v":null}`), Criteria: []string{"AC1"}, AllowedPaths: []string{"/n"}}}}
}
func scriptManifest(t *testing.T, cas storage.CAS, script string) domain.Manifest {
	t.Helper()
	d, e := cas.Put([]byte(script))
	if e != nil {
		t.Fatal(e)
	}
	return domain.Manifest{Schema: "seai.source/v1", Files: []domain.File{{Path: "run.sh", Mode: "100644", Digest: d}}}
}
func TestComparisonExactCriteriaAndFailures(t *testing.T) {
	cas := storage.CAS{Root: t.TempDir()}
	p := localProfile()
	base := scriptManifest(t, cas, "printf '%s' '{\"n\":9007199254740992,\"v\":null}'\n")
	candidate := scriptManifest(t, cas, "printf '%s' '{\"n\":9007199254740993,\"v\":null}'\n")
	spec := domain.ChangeSpec{Kind: "change", Criteria: []domain.Criterion{{ID: "AC1", Profile: p.AssertionProfile, FixtureDigest: FixtureDigest(p)}}}
	r := Run(context.Background(), p, base, candidate, spec, cas, t.TempDir())
	if r.Status != "PASSED" || len(r.Criteria) != 1 || len(r.Differences) != 1 || r.Differences[0].Classification != "expected" {
		t.Fatalf("%+v", r)
	}
	spec.Kind = "refactor"
	r = Run(context.Background(), p, base, candidate, spec, cas, t.TempDir())
	if r.Status == "PASSED" {
		t.Fatal("refactor delta passed")
	}
	spec.Kind = "change"
	spec.Criteria[0].FixtureDigest = "wrong"
	r = Run(context.Background(), p, base, candidate, spec, cas, t.TempDir())
	if r.Status == "PASSED" {
		t.Fatal("unbound fixture passed")
	}
	p.Fixtures[0].Expected = json.RawMessage(`{"n":9007199254740993}`)
	spec.Criteria[0].FixtureDigest = FixtureDigest(p)
	r = Run(context.Background(), p, base, candidate, spec, cas, t.TempDir())
	if r.Status == "PASSED" {
		t.Fatal("null and absent treated equal")
	}
	for _, raw := range []string{`{"x":1,"x":2}`, `1 2`} {
		if _, e := ParseJSON([]byte(raw)); e == nil {
			t.Fatal("invalid JSON accepted")
		}
	}
}
func TestRunnerTimeoutCancellationAndOutput(t *testing.T) {
	cas := storage.CAS{Root: t.TempDir()}
	p := localProfile()
	p.Kind = "compile"
	p.TimeoutSeconds = 1
	m := scriptManifest(t, cas, "sleep 10\n")
	start := time.Now()
	r := Run(context.Background(), p, m, m, domain.ChangeSpec{}, cas, t.TempDir())
	if r.Status != "TIMED_OUT" || time.Since(start) > 4*time.Second {
		t.Fatalf("%+v", r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = Run(ctx, p, m, m, domain.ChangeSpec{}, cas, t.TempDir())
	if r.Status != "CANCELLED" {
		t.Fatalf("%+v", r)
	}
	m = scriptManifest(t, cas, "i=0; while [ $i -lt 2000 ]; do echo abcdefghijk; i=$((i+1)); done\n")
	r = Run(context.Background(), p, m, m, domain.ChangeSpec{}, cas, t.TempDir())
	if r.Status != "OUTPUT_LIMIT" {
		t.Fatalf("%+v", r)
	}
	b, e := cas.Get(r.StdoutDigest)
	if e != nil || len(b) > p.MaxOutputBytes {
		t.Fatal(len(b), e)
	}
}
func TestTestEvidenceRequiresActualRun(t *testing.T) {
	good := []byte("{\"Action\":\"run\",\"Test\":\"TestReal\"}\n{\"Action\":\"pass\",\"Test\":\"TestReal\"}\n{\"Action\":\"pass\",\"Package\":\"p\"}\n")
	if _, e := parseTests(good, []string{"TestReal"}); e != nil {
		t.Fatal(e)
	}
	for _, bad := range [][]byte{[]byte("{\"Action\":\"pass\",\"Test\":\"TestReal\"}\n{\"Action\":\"pass\"}\n"), good} {
		required := []string{"TestAbsent"}
		if _, e := parseTests(bad, required); e == nil {
			t.Fatal("missing test passed")
		}
	}
	p := localProfile()
	p.AcknowledgeLocal = false
	if Validate(p) == nil {
		t.Fatal("unapproved execution accepted")
	}
}
