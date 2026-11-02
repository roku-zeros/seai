// Package mcpbridge exposes a deliberately bounded MCP stdio surface over SEAI RPC.
// No tool can approve behavior, invoke a shell, or write the product working tree.
package mcpbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"seai.local/seai/internal/application"
	"seai.local/seai/internal/contracts"
	"seai.local/seai/internal/transports"
	"seai.local/seai/schemas"
)

type Caller func(context.Context, string, application.Params) (transports.Envelope, error)
type Server struct {
	Repo            string
	CandidateWrites bool
	Call            Caller
	generation      string
	initialized     bool
	ready           bool
}
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

var readMethods = []string{"project.get", "capabilities.get", "files.list", "file.read", "search.entities", "search.text", "symbol.get", "references.find", "calls.list", "tests.related", "rules.search", "rules.get", "rules.discover", "rules.diagnostics", "flow.get", "impact.get", "task.get", "task.diff", "checks.profiles", "jobs.get", "runs.get", "runs.logs", "provider.status"}
var writeMethods = []string{"task.create", "task.revise", "task.plan", "patch.apply"}

func member(method string, list []string) bool {
	for _, m := range list {
		if m == method {
			return true
		}
	}
	return false
}
func object(properties map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": properties, "additionalProperties": false, "required": required}
}
func (s *Server) Tools() []any {
	str := map[string]any{"type": "string"}
	integer := map[string]any{"type": "integer", "minimum": 0}
	props := map[string]any{"path": str, "query": str, "limit": integer, "offset": integer, "cursor": str, "index_generation_id": str, "source_snapshot_id": str, "symbol_id": str, "task_id": str, "job_id": str, "id": str, "profile_id": str, "depth": integer, "context": map[string]any{"type": "object", "additionalProperties": str}, "encoding": map[string]any{"enum": []string{"text", "base64", "both"}}}
	reads := append([]string{}, readMethods...)
	out := []any{
		map[string]any{"name": "seai_connect", "description": "Start here: attest project root, inspect capabilities and pin the current immutable index. No live scan. Reconnect after index rebuild. No index means ask operator to build it once.", "inputSchema": object(map[string]any{}), "annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false}},
		map[string]any{"name": "seai_read", "description": "Read/search code exclusively through SEAI. Reads automatically use the generation pinned by seai_connect; no repeated live scans. task.get/diff return stored metadata with freshness UNKNOWN. Use file_evidence_id in plans. Search is literal, case sensitive; empty hits do not prove absence. Text defaults to 64 KiB without base64 duplication.", "inputSchema": object(map[string]any{"method": map[string]any{"enum": reads}, "params": object(props)}, "method"), "annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false}},
	}
	out = append(out, map[string]any{"name": "seai_contract", "description": "Get exact JSON schema before constructing a change, plan, patch or runtime configuration. Local bundled contract; no daemon or source scan.", "inputSchema": object(map[string]any{"name": map[string]any{"enum": []string{"change", "plan", "patch", "runtime"}}}, "name"), "annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false}})
	if s.CandidateWrites {
		out = append(out, map[string]any{"name": "seai_candidate", "description": "Submit task.create/revise/plan or patch.apply asynchronously to a private candidate. Poll runs.get using returned run_id. Requires operation_id and expected_project_epoch; revision required. Human approval remains mandatory for behavior changes; no approval/export/release/shell tool is exposed.", "inputSchema": object(map[string]any{"method": map[string]any{"enum": writeMethods}, "params": map[string]any{"type": "object", "description": "SEAI RPC params: operation_id, expected_project_epoch, expected_revision, task_id and spec/plan/patch. See contracts."}}, "method", "params"), "annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false}})
	}
	return out
}
func (s *Server) attest(ctx context.Context) (transports.Envelope, error) {
	env, e := s.Call(ctx, "project.get", application.Params{})
	if e != nil || env.Error != nil {
		return env, e
	}
	b, _ := json.Marshal(env.Data)
	var project struct {
		Root       string `json:"root"`
		Generation string `json:"current_generation_id"`
	}
	if e = json.Unmarshal(b, &project); e != nil {
		return env, e
	}
	actual, e := filepath.EvalSymlinks(project.Root)
	if e != nil {
		return env, e
	}
	expected, e := filepath.EvalSymlinks(s.Repo)
	if e != nil {
		return env, e
	}
	if actual != expected {
		return env, fmt.Errorf("PROJECT_MISMATCH: daemon root differs from --repo; select the matching runtime")
	}
	if s.generation == "" {
		s.generation = project.Generation
	}
	return env, nil
}
func (s *Server) invoke(ctx context.Context, raw json.RawMessage) (any, error) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
		Meta      json.RawMessage `json:"_meta,omitempty"`
	}
	if e := contracts.StrictJSON(raw, &call); e != nil {
		return nil, e
	}
	if call.Name == "seai_contract" {
		var a struct {
			Name string `json:"name"`
		}
		if e := contracts.StrictJSON(call.Arguments, &a); e != nil {
			return nil, e
		}
		if !member(a.Name, []string{"change", "plan", "patch", "runtime"}) {
			return nil, fmt.Errorf("unknown contract")
		}
		b, e := schemas.Contract(a.Name)
		if e != nil {
			return nil, e
		}
		return json.RawMessage(b), nil
	}
	env, e := s.attest(ctx)
	if e != nil {
		return nil, e
	}
	if env.Error != nil {
		return env, nil
	}
	if call.Name == "seai_connect" {
		if len(call.Arguments) > 0 {
			var empty struct{}
			if e = contracts.StrictJSON(call.Arguments, &empty); e != nil {
				return nil, e
			}
		}
		b, _ := json.Marshal(env.Data)
		var project struct {
			Generation string `json:"current_generation_id"`
		}
		_ = json.Unmarshal(b, &project)
		s.generation = project.Generation
		caps, e := s.Call(ctx, "capabilities.get", application.Params{})
		if e != nil {
			return nil, e
		}
		return map[string]any{"project": env, "capabilities": caps, "pinned_generation": s.generation, "freshness": "NOT_CHECKED", "next": "Use seai_read; reconnect after rebuilding. Missing product checks/business adapters require operator configuration."}, nil
	}
	var args struct {
		Method string             `json:"method"`
		Params application.Params `json:"params,omitempty"`
	}
	if e = contracts.StrictJSON(call.Arguments, &args); e != nil {
		return nil, e
	}
	p := args.Params
	if call.Name == "seai_read" {
		if !member(args.Method, readMethods) {
			return nil, fmt.Errorf("method not allowed on read-only MCP")
		}
		// Reject hidden mutation fields even for read methods.
		clean := p
		clean.Schema = ""
		clean.Path = ""
		clean.Query = ""
		clean.Limit = 0
		clean.Offset = 0
		clean.Cursor = ""
		clean.Generation = ""
		clean.Snapshot = ""
		clean.Symbol = ""
		clean.Task = ""
		clean.Job = ""
		clean.ID = ""
		clean.Profile = ""
		clean.Depth = 0
		clean.Context = nil
		clean.Encoding = ""
		b, _ := json.Marshal(clean)
		if string(b) != "{\"schema_version\":\"\"}" {
			return nil, fmt.Errorf("unsupported read parameters")
		}
		if args.Method == "task.get" || args.Method == "task.diff" {
			p.Stored = true
		}
		if p.Generation == "" || p.Generation == "current" {
			p.Generation = s.generation
		}
		if p.Snapshot != "" {
			p.Generation = ""
		} // candidate snapshots are independently attested by the daemon
		if member(args.Method, []string{"files.list", "file.read", "search.entities", "search.text", "symbol.get", "references.find", "calls.list", "tests.related", "rules.search", "rules.get", "rules.discover", "rules.diagnostics", "flow.get", "impact.get"}) && p.Generation == "" && p.Snapshot == "" {
			return nil, fmt.Errorf("NOT_READY: build an index first, then call seai_connect")
		}
		return s.Call(ctx, args.Method, p)
	}
	if call.Name == "seai_candidate" && s.CandidateWrites && member(args.Method, writeMethods) {
		p.WorkflowMethod = args.Method
		return s.Call(ctx, "workflow.submit", p)
	}
	return nil, fmt.Errorf("unknown or disabled tool")
}
func toolResult(value any, e error) any {
	failed := e != nil
	if env, ok := value.(transports.Envelope); ok && env.Error != nil {
		failed = true
	}
	if e != nil {
		value = map[string]any{"error": e.Error()}
	}
	b, err := json.Marshal(value)
	if err != nil {
		b = []byte(`{"error":"cannot encode tool result"}`)
		failed = true
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}, "isError": failed}
}

// Serve writes only newline-delimited JSON-RPC messages to stdout. Discovery never contacts the daemon.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scan := bufio.NewScanner(in)
	scan.Buffer(make([]byte, 4096), 1<<20)
	enc := json.NewEncoder(out)
	for scan.Scan() {
		var r request
		e := contracts.StrictJSON(scan.Bytes(), &r)
		response := map[string]any{"jsonrpc": "2.0", "id": r.ID}
		rpcError := func(code int, msg string) { response["error"] = map[string]any{"code": code, "message": msg} }
		if e != nil || r.JSONRPC != "2.0" {
			rpcError(-32600, "invalid request")
		} else if len(r.ID) == 0 {
			if r.Method == "notifications/initialized" && s.initialized {
				s.ready = true
			}
			continue
		} else if r.Method == "initialize" {
			if s.initialized {
				rpcError(-32600, "already initialized")
			} else {
				var p struct {
					Version string `json:"protocolVersion"`
				}
				if json.Unmarshal(r.Params, &p) != nil || p.Version == "" {
					rpcError(-32602, "protocolVersion required")
				} else {
					version := p.Version
					if version != "2024-11-05" && version != "2025-03-26" {
						version = "2025-03-26"
					}
					response["result"] = map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "seai", "version": "0.4.2-dev"}, "instructions": "Use seai_connect first, then seai_read for source access. No shell or human approval tools. Read-only server is safe to trust only when operator trusts this executable and selected repository."}
					s.initialized = true
				}
			}
		} else if r.Method == "ping" {
			response["result"] = map[string]any{}
		} else if !s.ready {
			rpcError(-32000, "initialize and send notifications/initialized first")
		} else {
			switch r.Method {
			case "tools/list":
				response["result"] = map[string]any{"tools": s.Tools()}
			case "tools/call":
				v, err := s.invoke(ctx, r.Params)
				response["result"] = toolResult(v, err)
			default:
				rpcError(-32601, "method not found")
			}
		}
		if e = enc.Encode(response); e != nil {
			return e
		}
	}
	return scan.Err()
}
