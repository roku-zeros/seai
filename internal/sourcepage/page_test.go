package sourcepage

import (
	"strings"
	"testing"
)

func TestTextPagesPreserveUTF8WithoutDuplicateEncoding(t *testing.T) {
	b := []byte("aЖяz")
	p, e := Read(b, 0, 2, "")
	if e != nil || p["text"] != "a" || p["next_offset"] != 1 {
		t.Fatal(p, e)
	}
	if _, ok := p["content_base64"]; ok {
		t.Fatal("text duplicated")
	}
	p, e = Read(b, 1, 4, "")
	if e != nil || p["text"] != "Жя" || p["next_offset"] != 5 {
		t.Fatal(p, e)
	}
	if _, e = Read(b, 2, 5, ""); e == nil {
		t.Fatal("split UTF-8 accepted")
	}
	if _, e = Read(b, 1, 1, ""); e == nil {
		t.Fatal("zero-progress page")
	}
	p, e = Read([]byte(strings.Repeat("x", 19333)), 0, 0, "")
	if e != nil || p["truncated"] != false {
		t.Fatal(p, e)
	}
	p, e = Read([]byte{255, 0}, 0, 0, "")
	if e != nil || p["encoding"] != "base64" {
		t.Fatal(p, e)
	}
	p, e = Read(b, 0, 0, "both")
	if e != nil || p["text"] != "aЖяz" || p["content_base64"] == nil {
		t.Fatal(p, e)
	}
}
