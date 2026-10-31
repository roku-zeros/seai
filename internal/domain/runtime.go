package domain

import "encoding/json"

type Fixture struct {
	ID           string          `json:"id"`
	Input        string          `json:"input"`
	Expected     json.RawMessage `json:"expected"`
	Criteria     []string        `json:"criterion_ids"`
	AllowedPaths []string        `json:"allowed_paths"`
}
type CheckProfile struct {
	ID               string    `json:"id"`
	Kind             string    `json:"kind"` // compile, tests, comparison
	Program          string    `json:"program"`
	Args             []string  `json:"args"`
	Isolation        string    `json:"isolation"` // local-approved or docker
	AcknowledgeLocal bool      `json:"acknowledge_local_execution,omitempty"`
	DockerImage      string    `json:"docker_image,omitempty"`
	TimeoutSeconds   int       `json:"timeout_seconds"`
	MaxOutputBytes   int       `json:"max_output_bytes"`
	MemoryMB         int       `json:"memory_mb"`
	CPUs             int       `json:"cpus"`
	AssertionProfile string    `json:"assertion_profile,omitempty"`
	TestNames        []string  `json:"test_names,omitempty"`
	Fixtures         []Fixture `json:"fixtures,omitempty"`
}
type ProviderProfile struct {
	Endpoint          string `json:"endpoint"`
	Model             string `json:"model"`
	APIKeyEnv         string `json:"api_key_env"`
	AllowLoopbackHTTP bool   `json:"allow_loopback_http,omitempty"`
	ContextTokens     int    `json:"context_tokens"`
	MaxOutputTokens   int    `json:"max_output_tokens"`
	TimeoutSeconds    int    `json:"timeout_seconds"`
	MaxCalls          int    `json:"max_calls"`
	MaxTaskTokens     int    `json:"max_task_tokens"`
}
type RuntimeConfig struct {
	Schema          string           `json:"schema_version"`
	Checks          []CheckProfile   `json:"checks"`
	Provider        *ProviderProfile `json:"provider,omitempty"`
	MaxWorkers      int              `json:"max_workers"`
	MaxNodeAttempts int              `json:"max_node_attempts"`
	MaxRunSeconds   int              `json:"max_run_seconds"`
}
type CheckResult struct {
	ProfileID     string       `json:"profile_id"`
	Status        string       `json:"status"`
	ExitCode      int          `json:"exit_code"`
	StdoutDigest  string       `json:"stdout_digest,omitempty"`
	StderrDigest  string       `json:"stderr_digest,omitempty"`
	Tests         []string     `json:"passed_tests"`
	Criteria      []string     `json:"criterion_ids"`
	Differences   []Difference `json:"differences"`
	FixtureDigest string       `json:"fixture_digest,omitempty"`
	DurationMS    int64        `json:"duration_ms"`
	Isolation     string       `json:"isolation"`
	Error         string       `json:"error,omitempty"`
}
type Difference struct {
	Fixture        string          `json:"fixture_id"`
	Path           string          `json:"path"`
	Before         json.RawMessage `json:"before,omitempty"`
	After          json.RawMessage `json:"after,omitempty"`
	BeforePresent  bool            `json:"before_present"`
	AfterPresent   bool            `json:"after_present"`
	Classification string          `json:"classification"`
}
type WorkerResult struct {
	Role     string   `json:"role"`
	Status   string   `json:"status"`
	Summary  string   `json:"summary"`
	Evidence []string `json:"evidence_ids"`
	Gaps     []string `json:"gaps"`
	Patch    *Patch   `json:"patch,omitempty"`
	TestPlan []string `json:"test_plan,omitempty"`
	Verdict  string   `json:"verdict,omitempty"`
}
type ReleasePlan struct {
	ID             string   `json:"plan_id"`
	ReleaseID      string   `json:"release_id"`
	Ref            string   `json:"ref"`
	Commit         string   `json:"resolved_commit"`
	Source         string   `json:"source_snapshot_id"`
	FromSource     string   `json:"from_snapshot_id"`
	FromGeneration string   `json:"from_generation_id"`
	Epoch          int      `json:"from_project_epoch"`
	Extractor      string   `json:"extractor_version"`
	Paths          []string `json:"changed_paths"`
	Tasks          []string `json:"affected_tasks"`
}
type Conflict struct {
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	Base         *File  `json:"base"`
	Ours         *File  `json:"ours"`
	Theirs       *File  `json:"theirs"`
	MarkerDigest string `json:"marker_digest,omitempty"`
}
type BackupManifest struct {
	Schema        string `json:"schema_version"`
	Version       string `json:"product_version"`
	APIVersion    string `json:"api_version"`
	RuntimeSchema int    `json:"runtime_schema"`
	Project       string `json:"project_id"`
	Repository    string `json:"repository_root"`
	Files         []File `json:"files"`
}
type UpgradeBundle struct {
	Schema        string `json:"schema_version"`
	Version       string `json:"product_version"`
	API           string `json:"api_version"`
	RuntimeSchema int    `json:"runtime_schema"`
	Seai          string `json:"seai"`
	SeaiDigest    string `json:"seai_digest"`
	Seaid         string `json:"seaid"`
	SeaidDigest   string `json:"seaid_digest"`
}
