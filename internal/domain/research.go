package domain

// ResearchRequest partitions a read-only investigation; it never creates a change task.
type ResearchRequest struct {
	Objective string   `json:"objective"`
	Topics    []string `json:"topics"`
}
type ResearchFinding struct {
	Statement string   `json:"statement"`
	Evidence  []string `json:"evidence_ids"`
}
type ResearchReport struct {
	Summary  string            `json:"summary"`
	Findings []ResearchFinding `json:"findings"`
	Gaps     []string          `json:"gaps"`
}
