package mcpbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"seai.local/seai/internal/application"
	"seai.local/seai/internal/transports"
	"strings"
	"testing"
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
