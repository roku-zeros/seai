package application

import (
	"context"
	"seai.local/seai/internal/changespec"
	"seai.local/seai/internal/domain"
)

// submitWorkflow journals a receipt before live verification. Static rejection is
// cheap; workflow repeats all revision, approval and live guards before commit.
func (s *Service) submitWorkflow(ctx context.Context, p Params) (any, error) {
	method := p.WorkflowMethod
	switch method {
	case "task.create", "task.revise", "task.plan", "patch.apply":
	default:
		return nil, domain.Err("METHOD_NOT_FOUND", "unsupported async workflow method")
	}
	if p.Revision == nil {
		return nil, domain.Err("SCHEMA_INVALID", "expected_revision required")
	}
	if method == "task.create" || method == "task.revise" {
		if p.Spec == nil {
			return nil, domain.Err("SCHEMA_INVALID", "spec required")
		}
		if e := changespec.Validate(*p.Spec); e != nil {
			return nil, e
		}
	}
	if method == "task.create" && *p.Revision != 0 {
		return nil, domain.Err("REVISION_CONFLICT", "new task requires expected_revision=0")
	}
	if method != "task.create" && p.Task == "" {
		return nil, domain.Err("SCHEMA_INVALID", "task_id required")
	}
	if method == "task.plan" && p.Plan == nil {
		return nil, domain.Err("SCHEMA_INVALID", "plan required")
	}
	if method == "patch.apply" && p.Patch == nil {
		return nil, domain.Err("SCHEMA_INVALID", "patch required")
	}
	return s.startRun(ctx, "workflow.submit", p, func(c context.Context, id string, _ domain.Task) (any, error) {
		p.WorkflowMethod = ""
		p.Operation = "workflow:" + domain.Digest([]byte(id))
		c = context.WithValue(c, integratorKey{}, true)
		return s.workflow(c, method, p)
	})
}
