// Package domain defines deterministic identities and public contracts.
package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

const API = "seai.api/v1"

func Digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Canonical normalizes struct objects through maps so all object keys are sorted.
// Inputs deliberately support integer values only; json.Number preserves precision.
func Canonical(v any) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e = d.Decode(&value); e != nil {
		return nil, e
	}
	value, e = normalizeNumbers(value)
	if e != nil {
		return nil, e
	}
	return json.Marshal(value)
}

func normalizeNumbers(v any) (any, error) {
	switch value := v.(type) {
	case json.Number:
		if strings.ContainsAny(string(value), ".eE") {
			return nil, fmt.Errorf("canonical manifests require integer numbers")
		}
		n, ok := new(big.Int).SetString(string(value), 10)
		if !ok {
			return nil, fmt.Errorf("invalid integer")
		}
		return json.Number(n.String()), nil
	case map[string]any:
		for key, item := range value {
			n, e := normalizeNumbers(item)
			if e != nil {
				return nil, e
			}
			value[key] = n
		}
	case []any:
		for i, item := range value {
			n, e := normalizeNumbers(item)
			if e != nil {
				return nil, e
			}
			value[i] = n
		}
	}
	return v, nil
}

type Error struct {
	Code            string `json:"code"`
	Message         string `json:"message"`
	Retryable       bool   `json:"retryable"`
	SuggestedAction string `json:"suggested_action,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }
func Err(code, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

type File struct {
	Path   string `json:"path"`
	Mode   string `json:"mode"`
	Digest string `json:"content_digest"`
}
type Build struct {
	GOOS      string   `json:"goos"`
	GOARCH    string   `json:"goarch"`
	Toolchain string   `json:"toolchain"`
	Tags      []string `json:"tags"`
	Profile   string   `json:"environment_profile"`
}
type Manifest struct {
	Schema     string `json:"schema_version"`
	Repository string `json:"repository_id"`
	Commit     string `json:"base_commit"`
	Build      Build  `json:"build"`
	Files      []File `json:"files"`
}

func (m Manifest) ID() (string, error) { b, e := Canonical(m); return Digest(b), e }

type Hit struct {
	ID       string `json:"symbol_id"`
	Name     string `json:"name"`
	Package  string `json:"package_path"`
	Kind     string `json:"kind"`
	Path     string `json:"file_path"`
	Start    int    `json:"start_byte"`
	End      int    `json:"end_byte"`
	Evidence string `json:"evidence_id"`
	Snippet  string `json:"snippet"`
}
type SearchResult struct {
	Snapshot    string   `json:"source_snapshot_id"`
	Generation  string   `json:"index_generation_id"`
	Coverage    string   `json:"coverage"`
	Hits        []Hit    `json:"hits"`
	Truncated   bool     `json:"truncated"`
	Limitations []string `json:"limitations"`
}
