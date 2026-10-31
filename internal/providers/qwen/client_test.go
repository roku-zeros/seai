package qwen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"seai.local/seai/internal/domain"
	"strings"
	"testing"
)

func profile(url string) domain.ProviderProfile {
	return domain.ProviderProfile{Endpoint: url, Model: "fixture-qwen", AllowLoopbackHTTP: true, ContextTokens: 16000, MaxOutputTokens: 512, TimeoutSeconds: 3, MaxCalls: 20, MaxTaskTokens: 64000}
}
func TestProbeProtocolAndSecretHandling(t *testing.T) {
	t.Setenv("SEAI_TEST_KEY", "secret-never-return")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer secret-never-return" {
			t.Error("incorrect request")
		}
		var request struct {
			Messages []Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&request)
		message := Message{Role: "assistant", Content: "SEAI_OK"}
		if len(request.Messages) == 1 {
			message.Content = ""
			message.ToolCalls = []ToolCall{{ID: "probe-1", Type: "function", Function: Function{Name: "seai_probe", Arguments: `{"value":"ok"}`}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "fixture-qwen", "choices": []any{map[string]any{"message": message}}})
	}))
	defer server.Close()
	p := profile(server.URL + "/v1")
	p.APIKeyEnv = "SEAI_TEST_KEY"
	client, e := New(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = client.Probe(context.Background()); e != nil || !client.Verified() || calls != 2 {
		t.Fatal(e, calls)
	}
	status, _ := json.Marshal(client.Status())
	if strings.Contains(string(status), "secret-never-return") {
		t.Fatal("secret leaked")
	}
	p = profile("http://example.com/v1")
	if _, e = New(p); e == nil {
		t.Fatal("insecure remote endpoint accepted")
	}
}
func TestModelMismatchAndRetryBound(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "secret-never-return", 503) }))
	defer server.Close()
	c, e := New(profile(server.URL))
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if e == nil || calls != 3 || strings.Contains(e.Error(), "secret-never-return") {
		t.Fatal(e, calls)
	}
	mismatch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"model":"wrong","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer mismatch.Close()
	c, _ = New(profile(mismatch.URL))
	if _, e = c.Chat(context.Background(), nil, nil); e == nil {
		t.Fatal("model mismatch accepted")
	}
}
