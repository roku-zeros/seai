package mcpbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"seai.local/seai/internal/application"
	"seai.local/seai/internal/transports"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryOfflineAndReadOnlyBoundary(t *testing.T) {
	called := false
	s := Server{Call: func(context.Context, string, application.Params) (transports.Envelope, error) {
		called = true
		return transports.Envelope{}, nil
	}}
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
`
	var out bytes.Buffer
	if e := s.Serve(context.Background(), strings.NewReader(input), &out); e != nil {
		t.Fatal(e)
	}
	if called {
		t.Fatal("discovery contacted daemon")
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatal(out.String())
	}
	var listed struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if e := json.Unmarshal([]byte(lines[1]), &listed); e != nil {
		t.Fatal(e)
	}
	if len(listed.Result.Tools) != 3 {
		t.Fatal(out.String())
	}
	if strings.Contains(out.String(), "seai_candidate") {
		t.Fatal("candidate writes exposed by default")
	}
}
func TestPinnedReadsAndProjectAttestation(t *testing.T) {
	repo := t.TempDir()
	generation := "generation-A"
	last := ""
	s := Server{Repo: repo, Call: func(_ context.Context, m string, p application.Params) (transports.Envelope, error) {
		switch m {
		case "project.get":
			return transports.Envelope{Data: map[string]any{"root": repo, "current_generation_id": generation}}, nil
		case "file.read":
			last = p.Generation
			if p.Stored {
				t.Fatal(p)
			}
		case "task.get":
			if !p.Stored {
				t.Fatal("task read would perform live scan")
			}
		default:
			if m != "capabilities.get" {
				t.Fatal("unexpected method", m)
			}
		}
		return transports.Envelope{Status: "ok"}, nil
	}}
	invoke := func(raw string) any {
		t.Helper()
		v, e := s.invoke(context.Background(), json.RawMessage(raw))
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	invoke(`{"name":"seai_connect","arguments":{}}`)
	generation = "generation-B"
	invoke(`{"name":"seai_read","arguments":{"method":"file.read","params":{"path":"main.go"}}}`)
	if last != "generation-A" {
		t.Fatal("pin drifted", last)
	}
	invoke(`{"name":"seai_read","arguments":{"method":"task.get","params":{"task_id":"t"}}}`)
	for _, raw := range []string{
		`{"name":"seai_read","arguments":{"method":"human.approve","params":{}}}`,
		`{"name":"seai_read","arguments":{"method":"file.read","params":{"operation_id":"hidden"}}}`,
		`{"name":"seai_candidate","arguments":{"method":"task.create","params":{}}}`,
	} {
		if _, e := s.invoke(context.Background(), json.RawMessage(raw)); e == nil {
			t.Fatal("boundary bypass", raw)
		}
	}
	s.Repo = t.TempDir()
	if _, e := s.invoke(context.Background(), json.RawMessage(`{"name":"seai_connect","arguments":{}}`)); e == nil {
		t.Fatal("wrong project accepted")
	}
}

func TestResearchToolsAreOptInAndCannotRouteMutations(t *testing.T) {
	repo := t.TempDir()
	var methods []string
	caller := func(_ context.Context, m string, p application.Params) (transports.Envelope, error) {
		if m == "project.get" {
			return transports.Envelope{Data: map[string]any{"root": repo, "current_generation_id": "shared-generation"}}, nil
		}
		methods = append(methods, m)
		if m == "research.start" && (p.Generation != "shared-generation" || p.Research.Objective != "question") {
			t.Fatal(p)
		}
		if m == "research.report" && (p.Report == nil || p.Lease != 2 || p.ResearchJob != "job") {
			t.Fatal(p)
		}
		return transports.Envelope{Status: "ok"}, nil
	}
	s := Server{Repo: repo, Call: caller}
	start := json.RawMessage(`{"name":"seai_research_start","arguments":{"objective":"question","topics":["rules"],"operation_id":"start","expected_project_epoch":1}}`)
	if _, e := s.invoke(context.Background(), start); e == nil {
		t.Fatal("research exposed by default")
	}
	s.Research = true
	if _, e := s.invoke(context.Background(), start); e != nil {
		t.Fatal(e)
	}
	report := json.RawMessage(`{"name":"seai_research_report","arguments":{"run_id":"r","job_id":"job","lease_epoch":2,"operation_id":"report","report":{"summary":"gap","findings":[],"gaps":["unknown"]}}}`)
	if _, e := s.invoke(context.Background(), report); e != nil {
		t.Fatal(e)
	}
	if len(methods) != 2 || methods[0] != "research.start" || methods[1] != "research.report" {
		t.Fatal(methods)
	}
	bad := json.RawMessage(`{"name":"seai_research_report","arguments":{"run_id":"r","job_id":"job","lease_epoch":2,"operation_id":"report","report":{"summary":"bad","findings":[],"gaps":[],"patch":{}}}}`)
	if _, e := s.invoke(context.Background(), bad); e == nil {
		t.Fatal("research report admitted patch")
	}
}

func TestConcurrentMCPToolCallsDoNotBlockReadsBehindPolling(t *testing.T) {
	repo := t.TempDir()
	entered := make(chan struct{})
	release := make(chan struct{})
	s := Server{Repo: repo, Call: func(c context.Context, m string, p application.Params) (transports.Envelope, error) {
		if m == "project.get" {
			return transports.Envelope{Data: map[string]any{"root": repo, "current_generation_id": "shared"}}, nil
		}
		if m == "research.get" {
			close(entered)
			select {
			case <-release:
			case <-c.Done():
				return transports.Envelope{}, c.Err()
			}
		}
		return transports.Envelope{Data: map[string]any{"method": m}}, nil
	}}
	in, inWriter := io.Pipe()
	outReader, out := io.Pipe()
	defer in.Close()
	defer inWriter.Close()
	defer out.Close()
	defer outReader.Close()
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), in, out) }()
	enc := json.NewEncoder(inWriter)
	dec := json.NewDecoder(outReader)
	send := func(v string) {
		t.Helper()
		if e := enc.Encode(json.RawMessage(v)); e != nil {
			t.Fatal(e)
		}
	}
	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)
	var init map[string]any
	if e := dec.Decode(&init); e != nil {
		t.Fatal(e)
	}
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"seai_read","arguments":{"method":"research.get","params":{"id":"r","wait_ms":10000}}}}`)
	<-entered
	send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"seai_read","arguments":{"method":"file.read","params":{"path":"main.go"}}}}`)
	read := make(chan map[string]any, 1)
	go func() { var response map[string]any; dec.Decode(&response); read <- response }()
	select {
	case response := <-read:
		if response["id"] != float64(4) {
			t.Fatal("read did not bypass pending poll", response)
		}
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("pending poll serialized all MCP reads")
	}
	close(release)
	var poll map[string]any
	if e := dec.Decode(&poll); e != nil || poll["id"] != float64(3) {
		t.Fatal(poll, e)
	}
	inWriter.Close()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
