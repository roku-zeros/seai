package transports

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"seai.local/seai/internal/application"
	"seai.local/seai/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestTimeoutDoesNotClaimDaemonIsUnavailable(t *testing.T) {
	dir, e := os.MkdirTemp("/tmp", "seai-rpc-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "rpc.sock")
	listener, e := net.Listen("unix", socket)
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req Request
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			return
		}
		if req.Method == "workspace.status" {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Second):
			}
		}
		_ = json.NewEncoder(w).Encode(Response{JSONRPC: "2.0", ID: req.ID, Result: &Envelope{Schema: domain.API, Status: "ok", Data: map[string]any{"healthy": true}}})
	})}
	go server.Serve(listener)
	defer server.Close()
	_, e = Call(WithTimeout(context.Background(), 30*time.Millisecond), socket, "workspace.status", application.Params{})
	if e == nil || !strings.Contains(e.Error(), "may still be running") || strings.Contains(e.Error(), "start seaid explicitly") {
		t.Fatalf("misleading timeout: %v", e)
	}
	env, e := Call(context.Background(), socket, "project.get", application.Params{})
	if e != nil || env.Error != nil {
		t.Fatal("health check blocked by previous timed-out request", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = Call(ctx, socket, "project.get", application.Params{}); e == nil || !strings.Contains(e.Error(), "cancelled") {
		t.Fatal("cancellation misreported", e)
	}
	if _, e = Call(context.Background(), filepath.Join(dir, "absent.sock"), "project.get", application.Params{}); e == nil || !strings.Contains(e.Error(), "socket is missing") {
		t.Fatal("missing socket not distinguished", e)
	}
}
