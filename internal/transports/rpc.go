// Package transports provides JSON-RPC 2.0 over owner-only Unix sockets.
package transports

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"seai.local/seai/internal/application"
	"seai.local/seai/internal/contracts"
	"seai.local/seai/internal/domain"
	"time"
)

type Request struct {
	JSONRPC string             `json:"jsonrpc"`
	ID      string             `json:"id"`
	Method  string             `json:"method"`
	Params  application.Params `json:"params"`
}
type Envelope struct {
	Schema string        `json:"schema_version"`
	Status string        `json:"status"`
	Data   any           `json:"data,omitempty"`
	Error  *domain.Error `json:"error,omitempty"`
}
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type Response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      string    `json:"id"`
	Result  *Envelope `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

func Handler(s *application.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "POST" || (r.URL.Path != "/rpc" && r.URL.Path != "/human/approve") {
			http.Error(w, "POST /rpc required", 405)
			return
		}
		var req Request
		body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1048576))
		if e == nil {
			e = contracts.StrictJSON(body, &req)
		}

		if e != nil || req.JSONRPC != "2.0" || req.ID == "" {
			_ = json.NewEncoder(w).Encode(Response{JSONRPC: "2.0", ID: req.ID, Error: &RPCError{Code: -32600, Message: "invalid request"}})
			return
		}
		var data any
		if r.URL.Path == "/human/approve" {
			if req.Method != "human.approve" && req.Method != "human.review" && req.Method != "human.requirements" {
				e = domain.Err("METHOD_NOT_FOUND", "approval route only")
			} else {
				data, e = s.DecideHuman(r.Context(), req.Method, req.Params)
			}
		} else {
			data, e = s.Handle(r.Context(), req.Method, req.Params)
		}
		env := &Envelope{Schema: domain.API, Status: "ok", Data: data}
		if e != nil {
			var de *domain.Error
			if !errors.As(e, &de) {
				de = &domain.Error{Code: "RUNTIME_ERROR", Message: e.Error()}
			}
			env.Status = "error"
			env.Error = de
			env.Data = nil
		} else if result, ok := data.(map[string]any); ok && (result["truncated"] == true || result["coverage"] == "partial") {
			env.Status = "partial"
		} else if result, ok := data.(domain.SearchResult); ok {
			env.Status = "partial"
			if len(result.Hits) == 0 && len(result.BusinessRules) == 0 {
				env.Status = "empty"
			}
		}
		_ = json.NewEncoder(w).Encode(Response{JSONRPC: "2.0", ID: req.ID, Result: env})
	})
}
func Call(ctx context.Context, socket, method string, p application.Params) (Envelope, error) {
	return callRoute(ctx, socket, "/rpc", method, p)
}
func HumanDecision(ctx context.Context, socket, method string, p application.Params) (Envelope, error) {
	return callRoute(ctx, socket, "/human/approve", method, p)
}
func HumanApproval(ctx context.Context, socket string, p application.Params) (Envelope, error) {
	return callRoute(ctx, socket, "/human/approve", "human.approve", p)
}

const DefaultRPCTimeout = 2 * time.Minute

type timeoutKey struct{}

// WithTimeout sets a per-RPC timeout; index/job polling retains a fresh budget per call.
func WithTimeout(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, timeoutKey{}, d)
}
func callRoute(ctx context.Context, socket, route, method string, p application.Params) (Envelope, error) {
	p.Schema = domain.API
	b, e := json.Marshal(Request{JSONRPC: "2.0", ID: "cli", Method: method, Params: p})
	if e != nil {
		return Envelope{}, e
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	timeout := DefaultRPCTimeout
	if d, ok := ctx.Value(timeoutKey{}).(time.Duration); ok && d > 0 {
		timeout = d
	}
	client := &http.Client{Transport: transport, Timeout: timeout}
	req, e := http.NewRequestWithContext(ctx, "POST", "http://localhost"+route, bytes.NewReader(b))
	if e != nil {
		return Envelope{}, e
	}
	resp, e := client.Do(req)
	if e != nil {
		if errors.Is(e, context.Canceled) {
			return Envelope{}, fmt.Errorf("RPC %s cancelled: %w", method, e)
		}
		var networkError net.Error
		if errors.Is(e, context.DeadlineExceeded) || (errors.As(e, &networkError) && networkError.Timeout()) {
			return Envelope{}, fmt.Errorf("RPC %s timed out at %s: %w; daemon may still be running. Check project status; for live verification use workspace check and poll run status. Do not restart or kill seaid based on this timeout", method, socket, e)
		}
		if errors.Is(e, os.ErrNotExist) {
			return Envelope{}, fmt.Errorf("daemon socket is missing at %s: %w; start seaid explicitly", socket, e)
		}
		return Envelope{}, fmt.Errorf("RPC %s connection failed at %s: %w; check daemon health and socket", method, socket, e)
	}
	defer resp.Body.Close()
	var result Response
	e = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&result)
	if e != nil {
		return Envelope{}, e
	}
	if result.Error != nil {
		return Envelope{}, fmt.Errorf("RPC %d: %s", result.Error.Code, result.Error.Message)
	}
	if result.Result == nil {
		return Envelope{}, fmt.Errorf("missing JSON-RPC result")
	}
	return *result.Result, nil
}
