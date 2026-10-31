package indexer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/types"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/tools/go/packages"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/storage"
)

func positionKey(file string, offset int) string {
	return filepath.Clean(file) + ":" + fmt.Sprint(offset)
}
func buildDetails(ctx context.Context, tx *sql.Tx, pkgs []*packages.Package, source string, m domain.Manifest, cas storage.CAS, defs map[string]string) error {
	files := map[string]domain.File{}
	for _, f := range m.Files {
		files[f.Path] = f
		fileEvidence := domain.Digest([]byte(f.Digest + ":" + f.Path))
		if _, e := tx.ExecContext(ctx, "INSERT INTO source_files VALUES(?,?,?,?)", f.Path, f.Digest, f.Mode, fileEvidence); e != nil {
			return e
		}
		b, e := cas.Get(f.Digest)
		if e != nil {
			return e
		}
		if !utf8.Valid(b) {
			continue
		}
		offset := 0
		for _, line := range bytes.SplitAfter(b, []byte("\n")) {
			if len(line) == 0 {
				continue
			}
			// Overlap preserves literal matches up to the maximum 512-byte query length.
			for start := 0; start < len(line); start += 7680 {
				end := min(start+8192, len(line))
				lo := start
				for lo > 0 && !utf8.RuneStart(line[lo]) {
					lo--
				}
				for end < len(line) && !utf8.RuneStart(line[end]) {
					end--
				}
				chunk := line[lo:end]
				if utf8.Valid(chunk) {
					if _, e = tx.ExecContext(ctx, "INSERT INTO chunks(path,blob,start,text,evidence) VALUES(?,?,?,?,?)", f.Path, f.Digest, offset+lo, string(chunk), domain.Digest([]byte(fmt.Sprintf("%s:%s:%d:%d", f.Digest, f.Path, offset+lo, offset+end)))); e != nil {
						return e
					}
				}
				if end == len(line) {
					break
				}
			}
			offset += len(line)
		}
	}
	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		targetID := func(obj types.Object) string {
			if fn, ok := obj.(*types.Func); ok {
				obj = fn.Origin()
			}
			if obj == nil {
				return ""
			}
			pos := pkg.Fset.Position(obj.Pos())
			return defs[positionKey(pos.Filename, pos.Offset)]
		}
		add := func(node ast.Node, target, caller, name, kind, certainty string) error {
			pos := pkg.Fset.Position(node.Pos())
			end := pkg.Fset.Position(node.End()).Offset
			rel, e := filepath.Rel(source, pos.Filename)
			if e != nil {
				return e
			}
			rel = filepath.ToSlash(rel)
			f, ok := files[rel]
			if !ok {
				return nil
			}
			ev := domain.Digest([]byte(fmt.Sprintf("%s:%s:%d:%d", f.Digest, rel, pos.Offset, end)))
			id := domain.Digest([]byte(fmt.Sprintf("%s:%d:%d:%s:%s:%s", rel, pos.Offset, end, target, caller, kind)))
			_, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO refs VALUES(?,?,?,?,?,?,?,?,?,?)", id, target, caller, name, rel, pos.Offset, end, kind, certainty, ev)
			return e
		}
		for ident, obj := range pkg.TypesInfo.Uses {
			id := targetID(obj)
			if id == "" {
				continue
			}
			if e := add(ident, id, "", obj.Name(), "REFERENCES", "exact"); e != nil {
				return e
			}
		}
		for _, file := range pkg.Syntax {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				caller := targetID(pkg.TypesInfo.Defs[fn.Name])
				if caller == "" {
					continue
				}
				var failure error
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					if failure != nil {
						return false
					}
					if _, ok := node.(*ast.FuncLit); ok {
						return false
					}
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					expr := call.Fun
					for {
						switch n := expr.(type) {
						case *ast.IndexExpr:
							expr = n.X
						case *ast.IndexListExpr:
							expr = n.X
						default:
							goto unwrapped
						}
					}
				unwrapped:
					var obj types.Object
					name := "<dynamic>"
					certainty := "exact"
					switch n := expr.(type) {
					case *ast.Ident:
						obj = pkg.TypesInfo.Uses[n]
						name = n.Name
					case *ast.SelectorExpr:
						obj = pkg.TypesInfo.Uses[n.Sel]
						name = n.Sel.Name
						if sel := pkg.TypesInfo.Selections[n]; sel != nil {
							if _, ok := sel.Recv().Underlying().(*types.Interface); ok {
								certainty = "possible"
							}
						}
					}
					if _, ok := obj.(*types.Builtin); ok {
						return true
					}
					if _, ok := obj.(*types.TypeName); ok {
						return true
					} // conversion, not a call
					id := targetID(obj)
					if _, ok := obj.(*types.Func); !ok {
						id = ""
						certainty = "unresolved"
					} else if id == "" {
						certainty = "unresolved"
					}
					failure = add(call, id, caller, name, "CALLS", certainty)
					return true
				})
				if failure != nil {
					return failure
				}
			}
		}
	}
	return nil
}

type TextCursor struct {
	Generation string `json:"generation"`
	Query      string `json:"query"`
	Offset     int    `json:"offset"`
}

func TextSearch(ctx context.Context, path, generation, query, cursor string, limit int, cas storage.CAS) (map[string]any, error) {
	if query == "" || len(query) > 512 || strings.ContainsAny(query, "\r\n") || !utf8.ValidString(query) {
		return nil, domain.Err("SCHEMA_INVALID", "literal single-line UTF-8 query required (1..512 bytes)")
	}
	offset := 0
	if cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil {
			return nil, domain.Err("CURSOR_SCOPE_MISMATCH", "invalid cursor")
		}
		var c TextCursor
		if json.Unmarshal(b, &c) != nil || c.Generation != generation || c.Query != domain.Digest([]byte(query)) || c.Offset < 0 {
			return nil, domain.Err("CURSOR_SCOPE_MISMATCH", "cursor belongs to another query/generation")
		}
		offset = c.Offset
	}
	db, e := sql.Open("sqlite3", readDSN(path))
	if e != nil {
		return nil, e
	}
	defer db.Close()
	rows, e := db.QueryContext(ctx, "SELECT path,blob,start,text FROM chunks WHERE instr(text,?)>0 ORDER BY path,start LIMIT ? OFFSET ?", query, limit+1, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	hits := []map[string]any{}
	for rows.Next() {
		var p, blob, text string
		var start int
		if e = rows.Scan(&p, &blob, &start, &text); e != nil {
			return nil, e
		}
		b, e := cas.Get(blob)
		if e != nil {
			return nil, e
		}
		if start < 0 || start+len(text) > len(b) || string(b[start:start+len(text)]) != text {
			return nil, domain.Err("INDEX_CORRUPT", "chunk evidence differs from source")
		}
		match := start + strings.Index(text, query)
		hits = append(hits, map[string]any{"file_path": p, "blob_digest": blob, "start_byte": start, "end_byte": start + len(text), "match_start_byte": match, "snippet": text, "evidence_id": domain.Digest([]byte(fmt.Sprintf("%s:%s:%d:%d", blob, p, start, start+len(text))))})
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	next := ""
	if len(hits) > limit {
		hits = hits[:limit]
		b, _ := json.Marshal(TextCursor{generation, domain.Digest([]byte(query)), offset + limit})
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	return map[string]any{"hits": hits, "next_cursor": next, "truncated": next != "", "mode": "literal-case-sensitive-lines"}, nil
}
func Symbol(ctx context.Context, path, id string, offset, limit int, cas storage.CAS) (map[string]any, error) {
	db, e := sql.Open("sqlite3", readDSN(path))
	if e != nil {
		return nil, e
	}
	defer db.Close()
	var h domain.Hit
	var blob string
	e = db.QueryRowContext(ctx, "SELECT id,name,package,kind,path,start,end,evidence,blob FROM symbols WHERE id=?", id).Scan(&h.ID, &h.Name, &h.Package, &h.Kind, &h.Path, &h.Start, &h.End, &h.Evidence, &blob)
	if e == sql.ErrNoRows {
		return nil, domain.Err("NOT_FOUND", "symbol not found in this generation")
	}
	if e != nil {
		return nil, e
	}
	b, e := cas.Get(blob)
	if e != nil {
		return nil, e
	}
	if offset < 0 || h.Start < 0 || h.End < h.Start || h.End > len(b) || offset > h.End-h.Start {
		return nil, domain.Err("INVALID_RANGE", "symbol byte range")
	}
	start := h.Start + offset
	end := min(start+limit, h.End)
	return map[string]any{"symbol": h, "blob_digest": blob, "start_byte": start, "end_byte": end, "content_base64": base64.StdEncoding.EncodeToString(b[start:end]), "text": safeText(b[start:end]), "truncated": end < h.End, "next_offset": end - h.Start}, nil
}
func References(ctx context.Context, path, id string, outgoing, tests bool, offset, limit int) (map[string]any, error) {
	db, e := sql.Open("sqlite3", readDSN(path))
	if e != nil {
		return nil, e
	}
	defer db.Close()
	var exists int
	if e = db.QueryRowContext(ctx, "SELECT count(*) FROM symbols WHERE id=?", id).Scan(&exists); e != nil {
		return nil, e
	}
	if exists == 0 {
		return nil, domain.Err("NOT_FOUND", "symbol not in selected generation")
	}
	condition := "r.target=?"
	if outgoing {
		condition = "r.caller=? AND r.kind='CALLS'"
	}
	if tests {
		condition = "r.target=? AND r.kind='CALLS' AND r.caller IN (SELECT id FROM symbols WHERE substr(path,-8)='_test.go' AND (name LIKE 'Test%' OR name LIKE 'Benchmark%' OR name LIKE 'Fuzz%'))"
	}
	rows, e := db.QueryContext(ctx, "SELECT r.target,r.caller,r.name,r.path,r.start,r.end,r.kind,r.certainty,r.evidence FROM refs r WHERE "+condition+" ORDER BY r.path,r.start,r.kind,r.target LIMIT ? OFFSET ?", id, limit+1, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	refs := []domain.Reference{}
	for rows.Next() {
		var r domain.Reference
		if e = rows.Scan(&r.Target, &r.Caller, &r.Name, &r.Path, &r.Start, &r.End, &r.Kind, &r.Certainty, &r.Evidence); e != nil {
			return nil, e
		}
		refs = append(refs, r)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	more := len(refs) > limit
	if more {
		refs = refs[:limit]
	}
	return map[string]any{"references": refs, "truncated": more, "next_offset": offset + len(refs), "scope": "local typed definitions; tests are direct calls, not executed test runs"}, nil
}

func safeText(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return ""
}
