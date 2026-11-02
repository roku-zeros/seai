package domain

// BusinessConfig maps project-specific Go identities to domain registrations.
// A configured adapter describes source syntax, never proof that a rule ran.
type BusinessConfig struct {
	Schema            string            `json:"schema_version"`
	Adapters          []RuleAdapter     `json:"adapters"`
	Requirements      []RuleRequirement `json:"requirements,omitempty"`
	RequireForChanges bool              `json:"require_for_changes,omitempty"`
}
type RuleAdapter struct {
	Name          string            `json:"name"`
	Kind          string            `json:"kind"` // struct or call
	Package       string            `json:"package"`
	Symbol        string            `json:"symbol"` // named type, function or Receiver.Method
	IDField       string            `json:"id_field,omitempty"`
	Handlers      map[string]string `json:"handler_fields,omitempty"`
	ContextFields []string          `json:"context_fields,omitempty"`
	PriorityField string            `json:"priority_field,omitempty"`
	IDArg         int               `json:"id_arg,omitempty"`
	HandlerArgs   map[string]int    `json:"handler_args,omitempty"`
	ContextArgs   map[string]int    `json:"context_args,omitempty"`
	PriorityArg   *int              `json:"priority_arg,omitempty"`
}
type RuleRequirement struct {
	RuleKey       string            `json:"rule_key"`
	Context       map[string]string `json:"context,omitempty"`
	ExternalID    string            `json:"external_id"`
	Revision      string            `json:"revision"`
	ContentDigest string            `json:"content_digest"`
	Content       string            `json:"content,omitempty"`
}
type BusinessEvidence struct {
	SnippetTruncated bool   `json:"snippet_truncated,omitempty"`
	ID               string `json:"evidence_id"`
	Path             string `json:"file_path"`
	Blob             string `json:"blob_digest"`
	Start            int    `json:"start_byte"`
	End              int    `json:"end_byte"`
	Snippet          string `json:"snippet"`
}
type BusinessHandler struct {
	Role      string           `json:"role"`
	Symbol    string           `json:"symbol_id,omitempty"`
	Name      string           `json:"qualified_name,omitempty"`
	Certainty string           `json:"certainty"`
	Evidence  BusinessEvidence `json:"evidence"`
}
type BusinessRule struct {
	ID           string            `json:"entity_id"`
	Key          string            `json:"rule_key"`
	Adapter      string            `json:"adapter"`
	Context      map[string]string `json:"context"`
	Priority     string            `json:"declared_priority,omitempty"`
	Certainty    string            `json:"certainty"`
	Handlers     []BusinessHandler `json:"handlers"`
	Evidence     BusinessEvidence  `json:"evidence"`
	Requirements []RuleRequirement `json:"requirements"`
}
type BusinessFact struct {
	Owner     string           `json:"symbol_id"`
	Kind      string           `json:"kind"` // condition, return, assignment, input_read
	Certainty string           `json:"certainty"`
	Evidence  BusinessEvidence `json:"evidence"`
}
type BusinessDiagnostic struct {
	Code     string           `json:"code"`
	Message  string           `json:"message"`
	Blocking bool             `json:"blocking"`
	Evidence BusinessEvidence `json:"evidence"`
}
type BusinessDiscovery struct {
	Package       string           `json:"package"`
	Type          string           `json:"type"`
	IDFields      []string         `json:"possible_id_fields"`
	HandlerFields []string         `json:"possible_handler_fields"`
	Evidence      BusinessEvidence `json:"evidence"`
}
type BusinessIndex struct {
	Schema       string               `json:"schema_version"`
	Version      string               `json:"extractor_version"`
	ConfigDigest string               `json:"config_digest"`
	Configured   bool                 `json:"configured"`
	Rules        []BusinessRule       `json:"rules"`
	Facts        []BusinessFact       `json:"facts"`
	Diagnostics  []BusinessDiagnostic `json:"diagnostics"`
	Discovery    []BusinessDiscovery  `json:"discovery"`
	Limitations  []string             `json:"limitations"`
}
