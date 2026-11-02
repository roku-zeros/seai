// Package sourcepage returns bounded source pages without splitting UTF-8 runes.
package sourcepage

import (
	"encoding/base64"
	"seai.local/seai/internal/domain"
	"unicode/utf8"
)

func Read(b []byte, offset, limit int, encoding string) (map[string]any, error) {
	if offset < 0 || offset > len(b) || limit < 0 || limit > 65536 {
		return nil, domain.Err("INVALID_RANGE", "offset must be within source; limit must be 0..65536")
	}
	if encoding == "" {
		encoding = "text"
	}
	if encoding != "text" && encoding != "base64" && encoding != "both" {
		return nil, domain.Err("SCHEMA_INVALID", "encoding must be text, base64 or both")
	}
	if limit == 0 {
		limit = 65536
	}
	end := min(offset+limit, len(b))
	valid := utf8.Valid(b)
	if valid && encoding != "base64" {
		if offset < len(b) && !utf8.RuneStart(b[offset]) {
			return nil, domain.Err("INVALID_RANGE", "offset splits a UTF-8 character; use next_offset from the preceding page")
		}
		for end < len(b) && !utf8.RuneStart(b[end]) {
			end--
		}
		if end == offset && end < len(b) {
			return nil, domain.Err("INVALID_RANGE", "limit cannot fit the next UTF-8 character")
		}
	}
	out := map[string]any{"start_byte": offset, "end_byte": end, "next_offset": end, "truncated": end < len(b)}
	if valid && encoding != "base64" {
		out["text"] = string(b[offset:end])
	}
	if encoding == "base64" || encoding == "both" || !valid {
		out["content_base64"] = base64.StdEncoding.EncodeToString(b[offset:end])
	}
	if !valid {
		out["encoding"] = "base64"
	} else {
		out["encoding"] = encoding
	}
	return out, nil
}
