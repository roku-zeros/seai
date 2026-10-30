// Package indexer builds immutable AST/type indexes from CAS-backed source copies.
package indexer

import (
	"context"
	"database/sql"
	"fmt"
	"go/ast"
	"go/token"
	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/storage"
	"seai.local/seai/internal/workspace"
	"strings"
)

const Version = "structural-v1"
const schema = `CREATE TABLE symbols(id TEXT PRIMARY KEY,name TEXT NOT NULL,package TEXT NOT NULL,kind TEXT NOT NULL,path TEXT NOT NULL,start INTEGER NOT NULL,end INTEGER NOT NULL,evidence TEXT NOT NULL,blob TEXT NOT NULL); CREATE INDEX symbol_names ON symbols(name,package); CREATE TABLE diagnostics(message TEXT NOT NULL);`

var Limitations = []string{"domain extractors are not configured", "call graph and lexical FTS are not implemented", "external modules, cgo and multi-module workspaces are not indexed", "unsaved editor buffers are excluded", "policy excludes vendor/**, .seai/**, .env and *.key"}

func Environment() []string {
	env := []string{"GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly", "GOWORK=off", "CGO_ENABLED=0", "GOPACKAGESDRIVER=off"}
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "GOCACHE", "GOMODCACHE", "GOPATH", "GOROOT"} {
		if v := os.Getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	return env
}
func BuildProfile(ctx context.Context) (domain.Build, error) {
	c := exec.CommandContext(ctx, "go", "env", "GOVERSION", "GOOS", "GOARCH")
	c.Env = Environment()
	b, e := c.Output()
	if e != nil {
		return domain.Build{}, e
	}
	v := strings.Fields(string(b))
	if len(v) != 3 {
		return domain.Build{}, fmt.Errorf("invalid go environment")
	}
	return domain.Build{Toolchain: v[0], GOOS: v[1], GOARCH: v[2], Tags: []string{}, Profile: "offline-cgo-disabled-v1"}, nil
}
func Build(ctx context.Context, root string, cas storage.CAS, m domain.Manifest, snapshot string) (id, path, coverage string, err error) {
	// Reuse still verifies every source object referenced by the manifest.
	for _, f := range m.Files {
		if _, e := cas.Get(f.Digest); e != nil {
			return "", "", "", e
		}
	}
	identity, _ := domain.Canonical(map[string]string{"snapshot": snapshot, "extractor": Version})
	id = domain.Digest(identity)
	dir := filepath.Join(root, "indexes", id)
	path = filepath.Join(dir, "index.db")
	if _, e := os.Stat(path); e == nil {
		db, e := sql.Open("sqlite3", readDSN(path))
		if e != nil {
			return "", "", "", e
		}
		defer db.Close()
		var integrity string
		if e = db.QueryRow("PRAGMA integrity_check").Scan(&integrity); e != nil {
			return "", "", "", e
		}
		if integrity != "ok" {
			return "", "", "", fmt.Errorf("INDEX_CORRUPT: %s", integrity)
		}
		var n int
		e = db.QueryRow("SELECT count(*) FROM diagnostics").Scan(&n)
		if e != nil {
			return "", "", "", e
		}
		coverage = "partial"
		return id, path, coverage, nil
	}
	stage, e := os.MkdirTemp(filepath.Join(root, "indexes"), ".building-")
	if e != nil {
		return "", "", "", e
	}
	defer os.RemoveAll(stage)
	source := filepath.Join(stage, "source")
	if e = workspace.Materialize(m, cas, source); e != nil {
		return "", "", "", e
	}
	if _, e = os.Stat(filepath.Join(source, "go.work")); e == nil {
		return "", "", "", domain.Err("UNSUPPORTED_SOURCE", "go.work requires a verified multi-root profile")
	}
	b, e := os.ReadFile(filepath.Join(source, "go.mod"))
	if e != nil {
		return "", "", "", domain.Err("UNSUPPORTED_SOURCE", "single-module go.mod is required")
	}
	mf, e := modfile.Parse("go.mod", b, nil)
	if e != nil {
		return "", "", "", e
	}
	for _, r := range mf.Replace {
		if r.New.Version == "" {
			return "", "", "", domain.Err("UNSUPPORTED_SOURCE", "local replace %s requires an immutable multi-root profile", r.New.Path)
		}
	}
	cfg := &packages.Config{Context: ctx, Dir: source, Env: append(Environment(), "GOOS="+m.Build.GOOS, "GOARCH="+m.Build.GOARCH), Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports | packages.NeedDeps | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedModule, Tests: true}
	pkgs, e := packages.Load(cfg, "./...")
	if e != nil {
		return "", "", "", e
	}
	if ctx.Err() != nil {
		return "", "", "", ctx.Err()
	}
	db, e := sql.Open("sqlite3", filepath.Join(stage, "index.db"))
	if e != nil {
		return "", "", "", e
	}
	defer db.Close()
	if _, e = db.Exec(schema); e != nil {
		return "", "", "", e
	}
	tx, e := db.Begin()
	if e != nil {
		return "", "", "", e
	}
	defer tx.Rollback()
	coverage = "partial" // Structural coverage never implies full domain coverage.
	diagnostics := append([]string{}, Limitations...)
	if len(pkgs) == 0 {
		diagnostics = append(diagnostics, "no Go packages found")
	}
	files := map[string]domain.File{}
	for _, f := range m.Files {
		files[f.Path] = f
	}
	for _, pkg := range pkgs {
		for _, d := range pkg.Errors {
			diagnostics = append(diagnostics, d.Error())
		}
		for _, file := range pkg.Syntax {
			rel, e := filepath.Rel(source, pkg.Fset.Position(file.Pos()).Filename)
			if e != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			f, ok := files[rel]
			if !ok {
				continue
			}
			add := func(name, kind string, node ast.Node) error {
				start := pkg.Fset.Position(node.Pos()).Offset
				end := pkg.Fset.Position(node.End()).Offset
				ev := domain.Digest([]byte(fmt.Sprintf("%s:%s:%d:%d", f.Digest, rel, start, end)))
				sid := domain.Digest([]byte(pkg.ID + ":" + rel + ":" + name + fmt.Sprint(start)))
				_, e := tx.Exec("INSERT OR IGNORE INTO symbols VALUES(?,?,?,?,?,?,?,?,?)", sid, name, pkg.PkgPath, kind, rel, start, end, ev, f.Digest)
				return e
			}
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					name := d.Name.Name
					if pkg.TypesInfo != nil {
						if obj := pkg.TypesInfo.Defs[d.Name]; obj != nil {
							name = obj.Name()
						}
					}
					if d.Recv != nil {
						name = receiver(d.Recv.List[0].Type) + "." + name
					}
					if e = add(name, "function", d); e != nil {
						return "", "", "", e
					}
				case *ast.GenDecl:
					for _, s := range d.Specs {
						switch v := s.(type) {
						case *ast.TypeSpec:
							e = add(v.Name.Name, "type", v)
						case *ast.ValueSpec:
							kind := "variable"
							if d.Tok == token.CONST {
								kind = "constant"
							}
							for _, n := range v.Names {
								if e = add(n.Name, kind, v); e != nil {
									break
								}
							}
						}
						if e != nil {
							return "", "", "", e
						}
					}
				}
			}
		}
	}
	for _, d := range diagnostics {
		if _, e = tx.Exec("INSERT INTO diagnostics VALUES(?)", d); e != nil {
			return "", "", "", e
		}
	}
	if e = tx.Commit(); e != nil {
		return "", "", "", e
	}
	var check string
	if e = db.QueryRow("PRAGMA integrity_check").Scan(&check); e != nil {
		return "", "", "", e
	}
	if check != "ok" {
		return "", "", "", fmt.Errorf("INDEX_CORRUPT: %s", check)
	}
	if e = db.Close(); e != nil {
		return "", "", "", e
	}
	if e = os.RemoveAll(source); e != nil {
		return "", "", "", e
	}
	indexFile, e := os.Open(filepath.Join(stage, "index.db"))
	if e != nil {
		return "", "", "", e
	}
	e = indexFile.Sync()
	indexFile.Close()
	if e != nil {
		return "", "", "", e
	}
	if e = storage.SyncDir(stage); e != nil {
		return "", "", "", e
	}
	if e = os.Rename(stage, dir); e != nil {
		return "", "", "", e
	}
	if e = storage.SyncDir(filepath.Dir(dir)); e != nil {
		return "", "", "", e
	}
	return id, path, coverage, nil
}
func receiver(e ast.Expr) string {
	switch n := e.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.StarExpr:
		return receiver(n.X)
	case *ast.IndexExpr:
		return receiver(n.X)
	case *ast.IndexListExpr:
		return receiver(n.X)
	}
	return "?"
}
func readDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	return u.String() + "?mode=ro&immutable=1"
}
func Search(ctx context.Context, path, query string, limit int, cas storage.CAS) ([]domain.Hit, bool, error) {
	db, e := sql.Open("sqlite3", readDSN(path))
	if e != nil {
		return nil, false, e
	}
	defer db.Close()
	rows, e := db.QueryContext(ctx, `SELECT id,name,package,kind,path,start,end,evidence,blob FROM symbols WHERE name=? OR package||'.'||name=? OR instr(lower(name),lower(?))>0 ORDER BY CASE WHEN name=? OR package||'.'||name=? THEN 0 ELSE 1 END,path,start,id LIMIT ?`, query, query, query, query, query, limit+1)
	if e != nil {
		return nil, false, e
	}
	defer rows.Close()
	hits := []domain.Hit{}
	for rows.Next() {
		var h domain.Hit
		var blob string
		if e = rows.Scan(&h.ID, &h.Name, &h.Package, &h.Kind, &h.Path, &h.Start, &h.End, &h.Evidence, &blob); e != nil {
			return nil, false, e
		}
		b, e := cas.Get(blob)
		if e != nil {
			return nil, false, e
		}
		if h.Start < 0 || h.End < h.Start || h.End > len(b) {
			return nil, false, fmt.Errorf("invalid evidence range")
		}
		end := h.End
		if end-h.Start > 8192 {
			end = h.Start + 8192
		}
		h.Snippet = string(b[h.Start:end])
		hits = append(hits, h)
	}
	if e = rows.Err(); e != nil {
		return nil, false, e
	}
	truncated := len(hits) > limit
	if truncated {
		hits = hits[:limit]
	}
	return hits, truncated, nil
}

func Diagnostics(ctx context.Context, path string) ([]string, error) {
	db, e := sql.Open("sqlite3", readDSN(path))
	if e != nil {
		return nil, e
	}
	defer db.Close()
	rows, e := db.QueryContext(ctx, "SELECT message FROM diagnostics ORDER BY rowid")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var d string
		if e = rows.Scan(&d); e != nil {
			return nil, e
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
