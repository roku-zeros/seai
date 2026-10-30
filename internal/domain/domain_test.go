package domain

import (
	"encoding/json"
	"testing"
)

func TestCanonicalIdentity(t *testing.T) {
	a, _ := Canonical(struct {
		Z int            `json:"z"`
		A map[string]int `json:"a"`
	}{7, map[string]int{"y": 2, "x": 1}})
	b, _ := Canonical(map[string]any{"a": map[string]int{"x": 1, "y": 2}, "z": 7})
	if string(a) != string(b) {
		t.Fatalf("noncanonical %s != %s", a, b)
	}
	v, _ := Canonical(map[string]any{"n": json.Number("9007199254740993")})
	if string(v) != "{\"n\":9007199254740993}" {
		t.Fatal(string(v))
	}
}

func TestCanonicalRejectsFractionalNumbers(t *testing.T) {
	if _, e := Canonical(map[string]any{"nested": []any{1.5}}); e == nil {
		t.Fatal("noninteger accepted")
	}
}
