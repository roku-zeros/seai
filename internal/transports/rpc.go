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
	"seai.local/seai/internal/application"
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
		if r.Method != "POST" || r.URL.Path != "/rpc" {
			http.Error(w, "POST /rpc required", 405)
			return
		}
		var req Request
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
		d.DisallowUnknownFields()
		e := d.Decode(&req)
		if e == nil {
			var extra any
			if d.Decode(&extra) != io.EOF {
				e = fmt.Errorf("one request required")
			}
		}
		if e != nil || req.JSONRPC != "2.0" || req.ID == "" {
			_ = json.NewEncoder(w).Encode(Response{JSONRPC: "2.0", ID: req.ID, Error: &RPCError{Code: -32600, Message: "invalid request"}})
			return
		}
		data, e := s.Handle(r.Context(), req.Method, req.Params)
		env := &Envelope{Schema: domain.API, Status: "ok", Data: data}
		if e != nil {
			var de *domain.Error
			if !errors.As(e, &de) {
				de = &domain.Error{Code: "RUNTIME_ERROR", Message: e.Error()}
			}
			env.Status = "error"
			env.Error = de
			env.Data = nil
		} else if result, ok := data.(domain.SearchResult); ok {
			env.Status = "partial"
			if len(result.Hits) == 0 {
				env.Status = "empty"
			}
		}
		_ = json.NewEncoder(w).Encode(Response{JSONRPC: "2.0", ID: req.ID, Result: env})
	})
}
func Call(ctx context.Context, socket, method string, p application.Params) (Envelope, error) {
	p.Schema = domain.API
	b, e := json.Marshal(Request{JSONRPC: "2.0", ID: "cli", Method: method, Params: p})
	if e != nil {
		return Envelope{}, e
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 35 * time.Second}
	req, e := http.NewRequestWithContext(ctx, "POST", "http://localhost/rpc", bytes.NewReader(b))
	if e != nil {
		return Envelope{}, e
	}
	resp, e := client.Do(req)
	if e != nil {
		return Envelope{}, fmt.Errorf("daemon unavailable at %s: %w; start seaid explicitly", socket, e)
	}
	defer resp.Body.Close()
	var result Response
	e = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result)
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
