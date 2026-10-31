package contracts

import (
	"seai.local/seai/internal/domain"
	"testing"
)

func TestStrictContractDecoding(t *testing.T) {
	for _, data := range []string{`{"schema_version":"seai.change/v1","approval":{"status":"approved"}}`, `{"revision":1,"revision":2}`, "revision: 1.5\n", "revision: 1\n---\nrevision: 2\n", "objective: &x hi\nchange_id: *x\n"} {
		var s domain.ChangeSpec
		if e := Decode([]byte(data), &s); e == nil {
			t.Fatal("invalid contract accepted:", data)
		}
	}
	var s domain.ChangeSpec
	if e := Decode([]byte("schema_version: seai.change/v1\nchange_id: CHG-1\nrevision: 1\n"), &s); e != nil || s.Revision != 1 {
		t.Fatal(s, e)
	}
}
func TestRPCDuplicateAndPatchPreconditions(t *testing.T) {
	var value struct {
		Operation string `json:"operation_id"`
	}
	if e := StrictJSON([]byte(`{"operation_id":"a","operation_id":"b"}`), &value); e == nil {
		t.Fatal("duplicate operation accepted")
	}
	var op domain.FileOperation
	if e := StrictJSON([]byte(`{"path":"new.go","content":"package p","mode":"100644"}`), &op); e == nil {
		t.Fatal("missing before accepted")
	}
	if e := StrictJSON([]byte(`{"path":"new.go","before_digest":null,"content":"package p","mode":"100644"}`), &op); e != nil {
		t.Fatal(e)
	}
}
