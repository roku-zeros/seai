package transports

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"seai.local/seai/internal/application"
	"testing"
)

func TestRPCRejectsMalformedInputs(t *testing.T) {
	h := Handler(&application.Service{})
	for _, body := range []string{`{"jsonrpc":"2.0","id":"1","method":"handshake","params":{"schema_version":"seai.api/v1","shell":"touch /tmp/forbidden"}}`, `{"jsonrpc":"1.0","id":"1"}`, `[]`, `{"jsonrpc":"2.0","id":"1","method":"handshake","params":{"schema_version":"seai.api/v1"}} {}`} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/rpc", bytes.NewBufferString(body)))
		var r Response
		if e := json.Unmarshal(w.Body.Bytes(), &r); e != nil {
			t.Fatal(e)
		}
		if r.Error == nil {
			t.Fatal("invalid request accepted:", body)
		}
	}
}
func TestHandshakeAndUnsupportedMethod(t *testing.T) {
	h := Handler(&application.Service{})
	for _, method := range []string{"handshake", "unknown.method"} {
		body := `{"jsonrpc":"2.0","id":"1","method":"` + method + `","params":{"schema_version":"seai.api/v1"}}`
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/rpc", bytes.NewBufferString(body)))
		var r Response
		if e := json.Unmarshal(w.Body.Bytes(), &r); e != nil {
			t.Fatal(e)
		}
		if r.Result == nil {
			t.Fatal("missing result")
		}
		if method == "handshake" && r.Result.Error != nil {
			t.Fatal(r.Result.Error)
		}
		if method == "unknown.method" && (r.Result.Error == nil || r.Result.Error.Code != "METHOD_NOT_FOUND") {
			t.Fatal("unsupported workflow not rejected")
		}
	}
}
