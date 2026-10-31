package changespec

import (
	"seai.local/seai/internal/domain"
	"testing"
)

func valid() domain.ChangeSpec {
	content := "minimum=21"
	return domain.ChangeSpec{Schema: "seai.change/v1", Alias: "CHG-1", Revision: 1, Kind: "change", Objective: "Change minimum", Requirements: []domain.Requirement{{ID: "R1", Revision: "1", Digest: domain.Digest([]byte(content)), Content: content}}, Scope: domain.Scope{Files: []string{"rule.go"}}, Expected: []domain.Behavior{{ID: "B1", Statement: "21 minimum"}}, Preserved: []domain.Behavior{{ID: "I1", Statement: "Inclusive comparison"}}, Criteria: []domain.Criterion{{ID: "AC1", BehaviorIDs: []string{"B1", "I1"}, Expected: "20 false; 21 true", Profile: "exact"}}, Unresolved: []string{}}
}
func TestReadinessAndBehaviorDigest(t *testing.T) {
	s := valid()
	if e := Validate(s); e != nil {
		t.Fatal(e)
	}
	spec, behavior, _ := Digests(s)
	s.Revision = 2
	newSpec, newBehavior, _ := Digests(s)
	if spec == newSpec || behavior != newBehavior {
		t.Fatal("technical revision changed behavior identity")
	}
	s.Requirements[0].Revision = "2"
	_, newBehavior, _ = Digests(s)
	if behavior == newBehavior {
		t.Fatal("requirement revision did not invalidate approval")
	}
	s.Requirements[0].Content = "tampered"
	if e := Validate(s); e == nil {
		t.Fatal("requirement digest mismatch accepted")
	}
}
func TestNoPlaceholdersForImplementation(t *testing.T) {
	s := valid()
	s.Unresolved = []string{"unknown threshold"}
	if e := Validate(s); e == nil {
		t.Fatal("unresolved contract accepted")
	}
	s = valid()
	s.Scope.Files = []string{".git/config"}
	if e := Validate(s); e == nil {
		t.Fatal("unsafe scope")
	}
	s = valid()
	s.Criteria[0].BehaviorIDs = []string{"missing"}
	if e := Validate(s); e == nil {
		t.Fatal("unknown behavior accepted")
	}
}
