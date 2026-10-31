package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ChangeSpec is the versioned task contract, separate from source snapshots.
type Requirement struct {
	ID       string `json:"external_id"`
	Revision string `json:"revision"`
	Digest   string `json:"content_digest"`
	Content  string `json:"content"`
}
type Behavior struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
}
type Criterion struct {
	ID            string   `json:"id"`
	BehaviorIDs   []string `json:"behavior_ids"`
	Expected      string   `json:"expected"`
	Profile       string   `json:"assertion_profile"`
	FixtureDigest string   `json:"fixture_digest,omitempty"`
}
type Scope struct {
	Files []string `json:"files"`
}
type ChangeSpec struct {
	Schema       string        `json:"schema_version"`
	Alias        string        `json:"change_id"`
	Revision     int           `json:"revision"`
	Kind         string        `json:"kind"`
	Objective    string        `json:"objective"`
	Requirements []Requirement `json:"source_requirements"`
	Scope        Scope         `json:"scope"`
	Expected     []Behavior    `json:"expected_behavior"`
	Preserved    []Behavior    `json:"preserved_behavior"`
	Criteria     []Criterion   `json:"acceptance_criteria"`
	Unresolved   []string      `json:"unresolved"`
}
type PlanStep struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Files       []string `json:"files"`
	Criteria    []string `json:"criterion_ids"`
	Evidence    []string `json:"evidence_ids"`
}
type Plan struct {
	Schema string     `json:"schema_version"`
	Steps  []PlanStep `json:"steps"`
	Checks []string   `json:"required_checks"`
}
type FileOperation struct {
	Path        string  `json:"path"`
	Before      *string `json:"before_digest"` // nil means a new file; never means "skip the guard".
	BeforeMode  string  `json:"before_mode,omitempty"`
	AfterDigest *string `json:"after_digest,omitempty"`
	Delete      bool    `json:"delete,omitempty"`
	Content     *string `json:"content,omitempty"`
	Base64      *string `json:"content_base64,omitempty"`
	Mode        string  `json:"mode,omitempty"`
}
type Patch struct {
	Schema     string          `json:"schema_version"`
	Base       string          `json:"base_snapshot_id"`
	SpecDigest string          `json:"spec_digest"`
	Files      []FileOperation `json:"files"`
}
type Task struct {
	ID                    string         `json:"task_id"`
	Alias                 string         `json:"alias"`
	Kind                  string         `json:"kind"`
	State                 string         `json:"state"`
	Revision              int            `json:"revision"`
	Baseline              string         `json:"baseline_snapshot_id"`
	Candidate             string         `json:"candidate_snapshot_id"`
	Epoch                 int            `json:"baseline_epoch"`
	SpecDigest            string         `json:"spec_digest"`
	BehaviorDigest        string         `json:"behavior_digest"`
	PlanDigest            string         `json:"plan_digest,omitempty"`
	Approved              bool           `json:"approved"`
	Checkpoint            string         `json:"checkpoint,omitempty"`
	Spec                  ChangeSpec     `json:"spec"`
	Plan                  *Plan          `json:"plan,omitempty"`
	Freshness             string         `json:"freshness"`
	Readiness             string         `json:"release_readiness"`
	CheckSummary          map[string]any `json:"check_summary,omitempty"`
	Reconciliation        bool           `json:"reconciliation_required,omitempty"`
	RequirementsFreshness string         `json:"requirements_freshness,omitempty"`
}
type Reference struct {
	Target    string `json:"target_id"`
	Caller    string `json:"caller_id,omitempty"`
	Name      string `json:"target_name"`
	Path      string `json:"file_path"`
	Start     int    `json:"start_byte"`
	End       int    `json:"end_byte"`
	Kind      string `json:"kind"`
	Certainty string `json:"certainty"`
	Evidence  string `json:"evidence_id"`
}

// UnmarshalJSON requires an explicit before_digest, including null for creation.
func (o *FileOperation) UnmarshalJSON(b []byte) error {
	type plain FileOperation
	var keys map[string]json.RawMessage
	if e := json.Unmarshal(b, &keys); e != nil {
		return e
	}
	if _, ok := keys["before_digest"]; !ok {
		return fmt.Errorf("before_digest is required (null for a new path)")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var value plain
	if e := d.Decode(&value); e != nil {
		return e
	}
	*o = FileOperation(value)
	if o.Delete {
		for _, key := range []string{"content", "content_base64", "mode", "after_digest"} {
			if _, ok := keys[key]; ok {
				return fmt.Errorf("delete operation cannot contain %s", key)
			}
		}
	}
	for key, value := range keys {
		if key != "before_digest" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("null is not allowed for %s", key)
		}
	}
	return nil
}
