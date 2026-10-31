// Package qwen provides a bounded OpenAI-compatible transport. It never logs credentials.
package qwen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"seai.local/seai/internal/contracts"
	"seai.local/seai/internal/domain"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}
type Function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}
type Reply struct {
	Message        Message
	Tokens         int
	UsageAvailable bool
}
type Client struct {
	Profile  domain.ProviderProfile
	url      string
	http     *http.Client
	verified atomic.Bool
}

func New(p domain.ProviderProfile) (*Client, error) {
	u, e := url.Parse(p.Endpoint)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, domain.Err("PROVIDER_CONFIG", "invalid endpoint")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	loop := host == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loop && p.AllowLoopbackHTTP) {
		return nil, domain.Err("PROVIDER_CONFIG", "HTTPS required; loopback HTTP needs explicit opt-in")
	}
	if p.Model == "" || p.ContextTokens < 2048 || p.MaxOutputTokens < 256 || p.MaxOutputTokens >= p.ContextTokens || p.MaxCalls < 1 || p.MaxCalls > 100 || p.MaxTaskTokens < p.ContextTokens || p.TimeoutSeconds < 1 || p.TimeoutSeconds > 600 {
		return nil, domain.Err("PROVIDER_CONFIG", "invalid model/token/time/call limits")
	}
	if p.APIKeyEnv != "" && strings.ContainsAny(p.APIKeyEnv, "=\x00\r\n") {
		return nil, domain.Err("PROVIDER_CONFIG", "invalid API key environment name")
	}
	return &Client{Profile: p, url: strings.TrimRight(p.Endpoint, "/") + "/chat/completions", http: &http.Client{Timeout: time.Duration(p.TimeoutSeconds) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("redirects disabled") }}}, nil
}
func (c *Client) Verified() bool { return c.verified.Load() }
func (c *Client) Status() map[string]any {
	return map[string]any{"configured": true, "verified": c.Verified(), "model": c.Profile.Model, "api_key_env": c.Profile.APIKeyEnv, "credentials_present": c.Profile.APIKeyEnv == "" || os.Getenv(c.Profile.APIKeyEnv) != "", "integration": "OpenAI-compatible; actual Qwen requires operator verification"}
}
func (c *Client) Chat(ctx context.Context, messages []Message, tools []map[string]any) (Reply, error) {
	var zero Reply
	payload := map[string]any{"model": c.Profile.Model, "messages": messages, "max_tokens": c.Profile.MaxOutputTokens, "temperature": 0, "stream": false}
	if len(tools) > 0 {
		payload["tools"] = tools
		payload["tool_choice"] = "auto"
	}
	b, e := json.Marshal(payload)
	if e != nil {
		return zero, e
	}
	if len(b)+c.Profile.MaxOutputTokens > c.Profile.ContextTokens {
		return zero, domain.Err("CONTEXT_BUDGET", "conservative byte-based context estimate exceeded")
	}
	token := ""
	if c.Profile.APIKeyEnv != "" {
		token = os.Getenv(c.Profile.APIKeyEnv)
		if token == "" {
			return zero, domain.Err("PROVIDER_UNAVAILABLE", "API key environment variable is empty")
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		req, e := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(b))
		if e != nil {
			return zero, e
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response, e := c.http.Do(req)
		if e != nil {
			if ctx.Err() != nil {
				return zero, ctx.Err()
			}
			return zero, domain.Err("PROVIDER_UNAVAILABLE", "HTTP transport failed")
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1048577))
		response.Body.Close()
		if response.StatusCode == 429 || response.StatusCode >= 500 {
			if attempt < 2 {
				select {
				case <-ctx.Done():
					return zero, ctx.Err()
				case <-time.After(time.Duration(attempt+1) * 200 * time.Millisecond):
					continue
				}
			}
		}
		if response.StatusCode != 200 {
			return zero, domain.Err("PROVIDER_HTTP", "HTTP status %d (response body withheld)", response.StatusCode)
		}
		if readErr != nil || len(raw) > 1048576 {
			return zero, domain.Err("PROVIDER_PROTOCOL", "response unreadable or too large")
		}
		if token != "" && bytes.Contains(raw, []byte(token)) {
			return zero, domain.Err("PROVIDER_PROTOCOL", "response contained credential material; withheld")
		}
		var checked json.RawMessage
		if contracts.StrictJSON(raw, &checked) != nil {
			return zero, domain.Err("PROVIDER_PROTOCOL", "invalid or duplicate response JSON")
		}
		var v struct {
			Model   string `json:"model"`
			Choices []struct {
				Message Message `json:"message"`
			} `json:"choices"`
			Usage *struct {
				Total int `json:"total_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(raw, &v) != nil || v.Model != c.Profile.Model || len(v.Choices) != 1 {
			return zero, domain.Err("PROVIDER_PROTOCOL", "expected configured model and exactly one choice")
		}
		reply := Reply{Message: v.Choices[0].Message}
		if v.Usage != nil {
			reply.Tokens = v.Usage.Total
			reply.UsageAvailable = true
		}
		return reply, nil
	}
	return zero, domain.Err("PROVIDER_UNAVAILABLE", "retry budget exhausted")
}
func (c *Client) Probe(ctx context.Context) error {
	c.verified.Store(false)
	tools := []map[string]any{{"type": "function", "function": map[string]any{"name": "seai_probe", "description": "Call once with value ok", "parameters": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string", "enum": []string{"ok"}}}, "required": []string{"value"}, "additionalProperties": false}}}}
	m := []Message{{Role: "user", Content: "Call seai_probe with value ok. After its response reply exactly SEAI_OK."}}
	r, e := c.Chat(ctx, m, tools)
	if e != nil {
		return e
	}
	if len(r.Message.ToolCalls) != 1 || r.Message.ToolCalls[0].Function.Name != "seai_probe" {
		return domain.Err("PROVIDER_PROTOCOL", "tool roundtrip unsupported")
	}
	call := r.Message.ToolCalls[0]
	var args map[string]string
	if json.Unmarshal([]byte(call.Function.Arguments), &args) != nil || len(args) != 1 || args["value"] != "ok" || call.ID == "" {
		return domain.Err("PROVIDER_PROTOCOL", "invalid probe tool arguments")
	}
	m = append(m, r.Message, Message{Role: "tool", ToolCallID: call.ID, Content: `{"ok":true}`})
	r, e = c.Chat(ctx, m, tools)
	if e != nil {
		return e
	}
	if strings.TrimSpace(r.Message.Content) != "SEAI_OK" || len(r.Message.ToolCalls) > 0 {
		return domain.Err("PROVIDER_PROTOCOL", "tool result not consumed")
	}
	c.verified.Store(true)
	return nil
}
