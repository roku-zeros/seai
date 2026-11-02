package mcpbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"seai.local/seai/internal/application"
	"seai.local/seai/internal/contracts"
	"seai.local/seai/internal/domain"
)

func (s *Server) researchTools() []any {
	str := map[string]any{"type": "string", "minLength": 1}
	integer := map[string]any{"type": "integer", "minimum": 0}
	specs := []struct {
		name, description string
		schema            map[string]any
	}{
		{"seai_research_start", "Create read-only research board for external GigaCode workers (no separate Qwen endpoint). Returns run_id immediately. Poll research.get for jobs, launch up to max_workers background forks, collect reports through SEAI. Does not spawn agents itself. One active runtime run; no task/candidate/source writes.", object(map[string]any{"objective": str, "topics": map[string]any{"type": "array", "items": str, "minItems": 1, "maxItems": 6}, "operation_id": str, "expected_project_epoch": integer, "index_generation_id": str}, "objective", "topics", "operation_id", "expected_project_epoch")},
		{"seai_research_claim", "Claim one queued research job (specific job_id optional). Save lease_epoch and use input.index_generation_id explicitly for all seai_read calls; fork contexts must use this shared generation. At configured concurrency limit wait for an existing worker, do not launch more forks.", object(map[string]any{"run_id": str, "job_id": str, "operation_id": str}, "run_id", "operation_id")},
		{"seai_research_report", "Publish a report for the claimed job and lease. Each finding needs indexed evidence_ids; empty results need an explicit gap. No task changes, source writes or approvals. Report via this tool even when fork cannot return output to parent; coordinator polls research.get.", object(map[string]any{"run_id": str, "job_id": str, "lease_epoch": integer, "operation_id": str, "report": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"summary": str, "findings": map[string]any{"type": "array", "maxItems": 30, "items": object(map[string]any{"statement": str, "evidence_ids": map[string]any{"type": "array", "items": str, "minItems": 1, "maxItems": 20}}, "statement", "evidence_ids")}, "gaps": map[string]any{"type": "array", "items": str, "maxItems": 30}}, "required": []string{"summary", "findings", "gaps"}}}, "run_id", "job_id", "lease_epoch", "operation_id", "report")},
		{"seai_research_cancel", "Cancel only a research session when workers stall; cannot cancel other task/check/release runs. Completed reports remain readable.", object(map[string]any{"run_id": str}, "run_id")},
	}
	out := []any{}
	for _, spec := range specs {
		out = append(out, map[string]any{"name": spec.name, "description": spec.description, "inputSchema": spec.schema, "annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "openWorldHint": false}})
	}
	return out
}
func (s *Server) invokeResearch(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "seai_research_start":
		var a struct {
			Objective  string   `json:"objective"`
			Topics     []string `json:"topics"`
			Operation  string   `json:"operation_id"`
			Epoch      *int     `json:"expected_project_epoch"`
			Generation string   `json:"index_generation_id,omitempty"`
		}
		if e := contracts.StrictJSON(raw, &a); e != nil {
			return nil, e
		}
		if a.Generation == "" {
			a.Generation = s.pin()
		}
		return s.Call(ctx, "research.start", application.Params{Operation: a.Operation, Epoch: a.Epoch, Generation: a.Generation, Research: &domain.ResearchRequest{Objective: a.Objective, Topics: a.Topics}})
	case "seai_research_claim":
		var a struct {
			Run       string `json:"run_id"`
			Job       string `json:"job_id,omitempty"`
			Operation string `json:"operation_id"`
		}
		if e := contracts.StrictJSON(raw, &a); e != nil {
			return nil, e
		}
		return s.Call(ctx, "research.claim", application.Params{ID: a.Run, ResearchJob: a.Job, Operation: a.Operation})
	case "seai_research_report":
		var a struct {
			Run       string                 `json:"run_id"`
			Job       string                 `json:"job_id"`
			Operation string                 `json:"operation_id"`
			Lease     int                    `json:"lease_epoch"`
			Report    *domain.ResearchReport `json:"report"`
		}
		if e := contracts.StrictJSON(raw, &a); e != nil {
			return nil, e
		}
		return s.Call(ctx, "research.report", application.Params{ID: a.Run, ResearchJob: a.Job, Operation: a.Operation, Lease: a.Lease, Report: a.Report})
	case "seai_research_cancel":
		var a struct {
			Run string `json:"run_id"`
		}
		if e := contracts.StrictJSON(raw, &a); e != nil {
			return nil, e
		}
		return s.Call(ctx, "research.cancel", application.Params{ID: a.Run})
	}
	return nil, fmt.Errorf("unknown research tool")
}
