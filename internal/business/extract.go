package business

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"golang.org/x/tools/go/packages"
	"path/filepath"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/storage"
	"sort"
	"strings"
	"unicode/utf8"
)

func object(pkg *packages.Package, expr ast.Expr) types.Object {
	for {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.X
		case *ast.IndexExpr:
			expr = n.X
		case *ast.IndexListExpr:
			expr = n.X
		default:
			goto done
		}
	}
done:
	switch n := expr.(type) {
	case *ast.Ident:
		return pkg.TypesInfo.ObjectOf(n)
	case *ast.SelectorExpr:
		return pkg.TypesInfo.ObjectOf(n.Sel)
	}
	return nil
}
func named(t types.Type) *types.Named {
	if t == nil {
		return nil
	}
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	n, _ := t.(*types.Named)
	return n
}
func identity(obj types.Object) (string, string) {
	if obj == nil || obj.Pkg() == nil {
		return "", ""
	}
	name := obj.Name()
	if f, ok := obj.(*types.Func); ok {
		f = f.Origin()
		if sig, ok := f.Type().(*types.Signature); ok && sig.Recv() != nil {
			if n := named(sig.Recv().Type()); n != nil {
				name = n.Obj().Name() + "." + name
			}
		}
	}
	return obj.Pkg().Path(), name
}
func literal(pkg *packages.Package, e ast.Expr) (string, bool) {
	if e == nil {
		return "", false
	}
	v := pkg.TypesInfo.Types[e].Value
	if v == nil || v.Kind() == constant.Unknown {
		return "", false
	}
	if v.Kind() == constant.String {
		return constant.StringVal(v), true
	}
	return v.ExactString(), true
}
func Extract(pkgs []*packages.Package, source string, m domain.Manifest, cas storage.CAS, defs map[string]string, config *domain.BusinessConfig) (domain.BusinessIndex, error) {
	out := domain.BusinessIndex{Schema: "seai.business-index/v1", Version: Version, ConfigDigest: Digest(config), Configured: config != nil && len(config.Adapters) > 0, Rules: []domain.BusinessRule{}, Facts: []domain.BusinessFact{}, Diagnostics: []domain.BusinessDiagnostic{}, Discovery: []domain.BusinessDiscovery{}, Limitations: []string{"source registrations do not prove execution, route reachability or runtime order", "conditions/returns/assignments are source facts, not symbolic execution", "reflection, closures, indirect handlers and runtime-built registries require diagnostics/manual coverage", "typed call graph is limited to the selected module/build profile"}}
	blobs := map[string][]byte{}
	files := map[string]domain.File{}
	for _, f := range m.Files {
		files[f.Path] = f
	}
	evidence := func(pkg *packages.Package, node ast.Node) domain.BusinessEvidence {
		if node == nil {
			return domain.BusinessEvidence{}
		}
		pos := pkg.Fset.Position(node.Pos())
		rel, e := filepath.Rel(source, pos.Filename)
		if e != nil {
			return domain.BusinessEvidence{}
		}
		rel = filepath.ToSlash(rel)
		f, ok := files[rel]
		if !ok {
			return domain.BusinessEvidence{}
		}
		start, end := pos.Offset, pkg.Fset.Position(node.End()).Offset
		b, ok := blobs[rel]
		if !ok {
			b, _ = cas.Get(f.Digest)
			blobs[rel] = b
		}
		if start < 0 || end < start || end > len(b) {
			return domain.BusinessEvidence{}
		}
		hi := min(end, start+2048)
		for hi > start && hi < len(b) && !utf8.RuneStart(b[hi]) {
			hi--
		}
		snippet := string(b[start:hi])
		return domain.BusinessEvidence{ID: domain.Digest([]byte(fmt.Sprintf("%s:%s:%d:%d", f.Digest, rel, start, end))), Path: rel, Blob: f.Digest, Start: start, End: end, Snippet: snippet, SnippetTruncated: hi < end}
	}
	target := func(pkg *packages.Package, obj types.Object) string {
		if f, ok := obj.(*types.Func); ok {
			obj = f.Origin()
		}
		if obj == nil {
			return ""
		}
		p := pkg.Fset.Position(obj.Pos())
		return defs[filepath.Clean(p.Filename)+":"+fmt.Sprint(p.Offset)]
	}
	diagnostic := func(code, message string, ev domain.BusinessEvidence) {
		if len(out.Diagnostics) >= 2000 {
			return
		}
		out.Diagnostics = append(out.Diagnostics, domain.BusinessDiagnostic{Code: code, Message: message, Blocking: true, Evidence: ev})
	}
	handlers := func(pkg *packages.Package, role string, expr ast.Expr) domain.BusinessHandler {
		ev := evidence(pkg, expr)
		obj := object(pkg, expr)
		p, n := identity(obj)
		h := domain.BusinessHandler{Role: role, Name: p + "." + n, Certainty: "unresolved", Evidence: ev}
		if _, ok := obj.(*types.Func); ok {
			h.Symbol = target(pkg, obj)
			if h.Symbol != "" {
				h.Certainty = "exact"
			}
			if sel, ok := expr.(*ast.SelectorExpr); ok {
				if selection := pkg.TypesInfo.Selections[sel]; selection != nil {
					if _, isInterface := selection.Recv().Underlying().(*types.Interface); isInterface {
						h.Certainty = "possible"
					}
				}
			}
		}
		if h.Certainty != "exact" {
			diagnostic("HANDLER_UNRESOLVED", "handler is indirect, external, closure or interface dispatch; concrete execution target not proven", ev)
		}
		return h
	}
	seenNodes := map[string]bool{}
	seenFacts := map[string]bool{}
	seenDiscovery := map[string]bool{}
	factLimitReported := false
	var adapters []domain.RuleAdapter
	if config != nil {
		adapters = config.Adapters
	}
	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			fileEv := evidence(pkg, file)
			if fileEv.Path == "" || strings.HasSuffix(fileEv.Path, "_test.go") {
				continue
			}
			for _, decl := range file.Decls {
				if gen, ok := decl.(*ast.GenDecl); ok {
					for _, spec := range gen.Specs {
						ts, ok := spec.(*ast.TypeSpec)
						if !ok {
							continue
						}
						typ := pkg.TypesInfo.TypeOf(ts.Type)
						if typ == nil {
							continue
						}
						st, ok := typ.Underlying().(*types.Struct)
						if !ok {
							continue
						}
						candidate := domain.BusinessDiscovery{Package: pkg.PkgPath, Type: ts.Name.Name, IDFields: []string{}, HandlerFields: []string{}, Evidence: evidence(pkg, ts)}
						for i := 0; i < st.NumFields(); i++ {
							f := st.Field(i)
							switch u := f.Type().Underlying().(type) {
							case *types.Basic:
								if u.Info()&types.IsString != 0 {
									candidate.IDFields = append(candidate.IDFields, f.Name())
								}
							case *types.Signature:
								candidate.HandlerFields = append(candidate.HandlerFields, f.Name())
							}
						}
						key := candidate.Package + ":" + candidate.Type
						if len(candidate.IDFields) > 0 && len(candidate.HandlerFields) > 0 && !seenDiscovery[key] {
							out.Discovery = append(out.Discovery, candidate)
							seenDiscovery[key] = true
						}
					}
				}
			}
			ast.Inspect(file, func(node ast.Node) bool {
				if node == nil {
					return true
				}
				var matched *domain.RuleAdapter
				values := map[string]ast.Expr{}
				contexts := map[string]ast.Expr{}
				handlerExpr := map[string]ast.Expr{}
				var idExpr, priority ast.Expr
				var directFunc *ast.FuncDecl
				switch n := node.(type) {
				case *ast.FuncDecl:
					p, name := identity(pkg.TypesInfo.Defs[n.Name])
					for _, a := range adapters {
						if a.Kind == "function" && a.Package == p && a.Symbol == name {
							copy := a
							matched = &copy
							directFunc = n
							break
						}
					}
					if matched == nil {
						return true
					}
					handlerExpr["handler"] = n.Name
				case *ast.CompositeLit:
					typ := named(pkg.TypesInfo.TypeOf(n))
					if typ == nil {
						return true
					}
					p, name := identity(typ.Obj())
					for _, a := range adapters {
						if a.Kind == "struct" && a.Package == p && a.Symbol == name {
							copy := a
							matched = &copy
							break
						}
					}
					if matched == nil {
						return true
					}
					st, _ := typ.Underlying().(*types.Struct)
					if st == nil {
						return true
					}
					for i, item := range n.Elts {
						if kv, ok := item.(*ast.KeyValueExpr); ok {
							if key, ok := kv.Key.(*ast.Ident); ok {
								values[key.Name] = kv.Value
							}
						} else if e, ok := item.(ast.Expr); ok && i < st.NumFields() {
							values[st.Field(i).Name()] = e
						}
					}
					idExpr = values[matched.IDField]
					for role, field := range matched.Handlers {
						handlerExpr[role] = values[field]
					}
					for _, field := range matched.ContextFields {
						contexts[field] = values[field]
					}
					priority = values[matched.PriorityField]
				case *ast.CallExpr:
					obj := object(pkg, n.Fun)
					p, name := identity(obj)
					for _, a := range adapters {
						if a.Kind == "call" && a.Package == p && a.Symbol == name {
							copy := a
							matched = &copy
							break
						}
					}
					if matched == nil {
						if _, builtin := obj.(*types.Builtin); builtin {
							return true
						}
						typ := pkg.TypesInfo.TypeOf(n)
						if typ != nil {
							if slice, ok := typ.Underlying().(*types.Slice); ok {
								typ = slice.Elem()
							}
							if regType := named(typ); regType != nil {
								p, name := identity(regType.Obj())
								for _, a := range adapters {
									if a.Kind == "struct" && a.Package == p && a.Symbol == name {
										diagnostic("RULE_CONSTRUCTION_UNRESOLVED", "factory returns configured rule type; registration/values need an explicit call adapter", evidence(pkg, n))
										break
									}
								}
							}
						}
						return true
					}
					if _, ok := obj.(*types.Func); !ok {
						diagnostic("REGISTRATION_UNRESOLVED", "configured registration invoked indirectly", evidence(pkg, n))
						return true
					}
					arg := func(i int) ast.Expr {
						if i >= 0 && i < len(n.Args) {
							return n.Args[i]
						}
						return nil
					}
					idExpr = arg(matched.IDArg)
					for role, i := range matched.HandlerArgs {
						handlerExpr[role] = arg(i)
					}
					for field, i := range matched.ContextArgs {
						contexts[field] = arg(i)
					}
					if matched.PriorityArg != nil {
						priority = arg(*matched.PriorityArg)
					}
				default:
					return true
				}
				ev := evidence(pkg, node)
				if seenNodes[ev.ID] || ev.ID == "" {
					return true
				}
				seenNodes[ev.ID] = true
				if len(out.Rules) >= 5000 {
					diagnostic("DOMAIN_LIMIT", "rule limit 5000 reached; narrow project scope", ev)
					return false
				}
				key, known := literal(pkg, idExpr)
				if directFunc != nil {
					key = matched.Package + "." + matched.Symbol
					known = true
				}
				certainty := "exact"
				if !known || key == "" || len(key) > 1024 {
					key = "<unresolved:" + ev.ID[:12] + ">"
					certainty = "unresolved"
					diagnostic("RULE_ID_UNRESOLVED", "rule ID is not a compile-time constant", ev)
				}
				rule := domain.BusinessRule{ID: domain.Digest([]byte(matched.Name + ":" + ev.ID)), Key: key, Adapter: matched.Name, Context: map[string]string{}, Certainty: certainty, Handlers: []domain.BusinessHandler{}, Evidence: ev, Requirements: []domain.RuleRequirement{}}
				names := []string{}
				for role := range handlerExpr {
					names = append(names, role)
				}
				sort.Strings(names)
				for _, role := range names {
					h := handlers(pkg, role, handlerExpr[role])
					if h.Evidence.ID == "" {
						h.Evidence = ev
						diagnostic("HANDLER_MISSING", "configured handler field/argument is absent", ev)
					}
					if h.Certainty != "exact" && rule.Certainty == "exact" {
						rule.Certainty = "possible"
					}
					rule.Handlers = append(rule.Handlers, h)
				}
				for field, expr := range contexts {
					value, ok := literal(pkg, expr)
					if ok && len(value) <= 2048 {
						rule.Context[field] = value
					} else {
						rule.Context[field] = "<dynamic>"
						rule.Certainty = "possible"
						diagnostic("CONTEXT_UNRESOLVED", "context "+field+" is dynamic or missing", ev)
					}
				}
				if priority == nil && (matched.PriorityField != "" || matched.PriorityArg != nil) {
					diagnostic("PRIORITY_UNRESOLVED", "configured priority field/argument is absent", ev)
					rule.Certainty = "possible"
				}
				if priority != nil {
					if value, ok := literal(pkg, priority); ok {
						rule.Priority = value
					} else {
						rule.Certainty = "possible"
						diagnostic("PRIORITY_UNRESOLVED", "priority is not a constant", evidence(pkg, priority))
					}
				}
				if config != nil {
					for _, req := range config.Requirements {
						if req.RuleKey != rule.Key {
							continue
						}
						matches := true
						for k, v := range req.Context {
							if rule.Context[k] != v {
								matches = false
							}
						}
						if matches {
							rule.Requirements = append(rule.Requirements, req)
						}
					}
				}
				out.Rules = append(out.Rules, rule)
				return true
			})
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				owner := target(pkg, pkg.TypesInfo.Defs[fn.Name])
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					if _, ok := node.(*ast.FuncLit); ok {
						return false
					}
					var fact ast.Node
					kind := ""
					switch n := node.(type) {
					case *ast.IfStmt:
						kind = "condition"
						fact = n.Cond
					case *ast.SwitchStmt:
						if n.Tag != nil {
							kind = "condition"
							fact = n.Tag
						}
					case *ast.CaseClause:
						kind = "branch"
						fact = n
					case *ast.ReturnStmt:
						kind = "return"
						fact = n
					case *ast.AssignStmt:
						kind = "assignment"
						fact = n
					case *ast.SelectorExpr:
						selection := pkg.TypesInfo.Selections[n]
						if selection != nil && selection.Kind() == types.FieldVal {
							kind = "field_access"
							fact = n
						}
					}
					if kind != "" {
						ev := evidence(pkg, fact)
						key := owner + ":" + kind + ":" + ev.ID
						if !seenFacts[key] {
							seenFacts[key] = true
							if len(out.Facts) < 20000 {
								out.Facts = append(out.Facts, domain.BusinessFact{Owner: owner, Kind: kind, Certainty: "exact", Evidence: ev})
							} else if !factLimitReported {
								factLimitReported = true
								diagnostic("FACT_LIMIT", "source fact limit reached; flow facts are incomplete", ev)
							}
						}
					}
					return true
				})
			}
		}
	}
	if len(out.Diagnostics) >= 2000 {
		out.Diagnostics = append(out.Diagnostics, domain.BusinessDiagnostic{Code: "DIAGNOSTIC_LIMIT", Message: "diagnostic limit reached; domain coverage incomplete", Blocking: true})
	}
	for _, a := range adapters {
		found := false
		for _, r := range out.Rules {
			if r.Adapter == a.Name {
				found = true
			}
		}
		if !found {
			diagnostic("ADAPTER_NO_MATCH", "no source registration matched adapter "+a.Name+"; check package/type/function identity", domain.BusinessEvidence{})
		}
	}
	if !out.Configured {
		out.Diagnostics = append(out.Diagnostics, domain.BusinessDiagnostic{Code: "DOMAIN_UNCONFIGURED", Message: "configure typed adapters using rules discover; candidates are suggestions, not business facts", Blocking: true})
	}
	sort.Slice(out.Rules, func(i, j int) bool {
		a, b := out.Rules[i], out.Rules[j]
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if a.Evidence.Path != b.Evidence.Path {
			return a.Evidence.Path < b.Evidence.Path
		}
		return a.Evidence.Start < b.Evidence.Start
	})
	sort.Slice(out.Facts, func(i, j int) bool {
		a, b := out.Facts[i], out.Facts[j]
		if a.Evidence.Path != b.Evidence.Path {
			return a.Evidence.Path < b.Evidence.Path
		}
		if a.Evidence.Start != b.Evidence.Start {
			return a.Evidence.Start < b.Evidence.Start
		}
		return a.Kind < b.Kind
	})
	sort.Slice(out.Diagnostics, func(i, j int) bool {
		a, b := out.Diagnostics[i], out.Diagnostics[j]
		return a.Evidence.Path+fmt.Sprint(a.Evidence.Start)+a.Code+a.Message < b.Evidence.Path+fmt.Sprint(b.Evidence.Start)+b.Code+b.Message
	})
	return out, nil
}
