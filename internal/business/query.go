package business

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"seai.local/seai/internal/domain"
	"sort"
	"strings"
)

func Open(path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path}
	return sql.Open("sqlite3", u.String()+"?mode=ro&immutable=1")
}
func Load(ctx context.Context, db *sql.DB) (domain.BusinessIndex, error) {
	var out domain.BusinessIndex
	var b []byte
	if e := db.QueryRowContext(ctx, "SELECT payload FROM business_index WHERE id=1").Scan(&b); e != nil {
		return out, domain.Err("INDEX_UPGRADE_REQUIRED", "rebuild index with structural-v4 business extractor")
	}
	if e := json.Unmarshal(b, &out); e != nil {
		return out, e
	}
	if out.Version != Version {
		return out, domain.Err("INDEX_UPGRADE_REQUIRED", "unsupported business extractor")
	}
	return out, nil
}

func BriefRule(r domain.BusinessRule) domain.BusinessRule {
	r.Requirements = append([]domain.RuleRequirement{}, r.Requirements...)
	for i := range r.Requirements {
		r.Requirements[i].Content = ""
	}
	return r
}

func Matches(r domain.BusinessRule, query string, scope map[string]string) bool {
	for k, v := range scope {
		if r.Context[k] != v {
			return false
		}
	}
	if query == "" {
		return true
	}
	b, _ := json.Marshal(r)
	return strings.Contains(strings.ToLower(string(b)), strings.ToLower(query))
}
func Rules(g domain.BusinessIndex, query string, scope map[string]string, offset, limit int) map[string]any {
	matches := []domain.BusinessRule{}
	for _, r := range g.Rules {
		if Matches(r, query, scope) {
			matches = append(matches, BriefRule(r))
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Key == query && matches[j].Key != query })
	lo := min(offset, len(matches))
	hi := min(lo+limit, len(matches))
	return map[string]any{"rules": matches[lo:hi], "total": len(matches), "next_offset": hi, "truncated": hi < len(matches), "configured": g.Configured, "config_digest": g.ConfigDigest, "extractor_version": g.Version, "coverage": "partial", "limitations": g.Limitations}
}
func ResolveRules(g domain.BusinessIndex, id string, scope map[string]string) ([]domain.BusinessRule, error) {
	out := []domain.BusinessRule{}
	for _, r := range g.Rules {
		if (r.ID == id || r.Key == id) && Matches(r, "", scope) {
			out = append(out, r)
		}
	}
	if len(out) > 1 {
		return nil, domain.Err("AMBIGUOUS_RULE", "rule key has %d registrations; use entity_id or full --context filters", len(out))
	}
	return out, nil
}

type Node struct {
	ID       string `json:"node_id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Path     string `json:"file_path,omitempty"`
	Package  string `json:"package_path,omitempty"`
	Evidence string `json:"evidence_id,omitempty"`
	Depth    int    `json:"depth"`
}
type Edge struct {
	From      string `json:"from"`
	To        string `json:"to,omitempty"`
	Kind      string `json:"kind"`
	Certainty string `json:"certainty"`
	Evidence  string `json:"evidence_id"`
	Path      string `json:"file_path"`
	Start     int    `json:"start_byte"`
	End       int    `json:"end_byte"`
	Reason    string `json:"reason,omitempty"`
}

// Traverse is deliberately bounded and preserves uncertain edges without expanding them as exact.
func Traverse(ctx context.Context, db *sql.DB, g domain.BusinessIndex, query string, scope map[string]string, impact bool, depth, limit int) (map[string]any, error) {
	rules, e := ResolveRules(g, query, scope)
	if e != nil {
		return nil, e
	}
	seeds := []string{}
	selectedRuleIDs := map[string]bool{}
	if len(rules) == 1 {
		selectedRuleIDs[rules[0].ID] = true
		for _, h := range rules[0].Handlers {
			if h.Symbol != "" && h.Certainty == "exact" {
				seeds = append(seeds, h.Symbol)
			}
		}
	}
	if len(rules) == 0 {
		rows, e := db.QueryContext(ctx, "SELECT id FROM symbols WHERE id=? OR name=? OR package||'.'||name=? ORDER BY path,start LIMIT 3", query, query, query)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return nil, e
			}
			seeds = append(seeds, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		if len(seeds) > 1 {
			return nil, domain.Err("AMBIGUOUS_SYMBOL", "use an exact symbol ID or qualified name")
		}
		if len(seeds) == 0 {
			return nil, domain.Err("NOT_FOUND", "rule/symbol not found")
		}
	}
	confirmed := []Edge{}
	possible := []Edge{}
	unexamined := []map[string]any{}
	nodes := []Node{}
	tests := []Node{}
	facts := []domain.BusinessFact{}
	gaps := []string{}
	truncated := false
	visited := map[string]int{}
	type queued struct {
		id    string
		depth int
	}
	queue := []queued{}
	for _, id := range seeds {
		if len(visited) >= limit {
			truncated = true
			unexamined = append(unexamined, map[string]any{"node_id": id, "reason": "seed_node_limit"})
			continue
		}
		if _, ok := visited[id]; !ok {
			visited[id] = 0
			queue = append(queue, queued{id, 0})
		}
	}
	edgeLimitReported := false
	addEdge := func(edge Edge) {
		if len(confirmed)+len(possible) >= 2000 {
			truncated = true
			if !edgeLimitReported {
				edgeLimitReported = true
				unexamined = append(unexamined, map[string]any{"reason": "edge_limit", "limit": 2000})
			}
			return
		}
		if edge.Certainty == "exact" {
			confirmed = append(confirmed, edge)
		} else {
			possible = append(possible, edge)
		}
	}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		var n Node
		var symbolStart, symbolEnd int
		n.ID = item.id
		n.Depth = item.depth
		if e = db.QueryRowContext(ctx, "SELECT name,kind,path,package,evidence,start,end FROM symbols WHERE id=?", item.id).Scan(&n.Name, &n.Kind, &n.Path, &n.Package, &n.Evidence, &symbolStart, &symbolEnd); e != nil {
			return nil, e
		}
		nodes = append(nodes, n)
		if strings.HasSuffix(n.Path, "_test.go") && strings.HasPrefix(n.Name, "Test") {
			tests = append(tests, n)
		}
		// For impact, references to constants/types must lead back to their containing function too.
		condition := "(caller=? AND kind='CALLS') OR (kind='REFERENCES' AND path=? AND start>=? AND end<=?)"
		queryArgs := []any{item.id, n.Path, symbolStart, symbolEnd}
		if impact {
			condition = "target=?"
			queryArgs = []any{item.id}
		}
		rows, e := db.QueryContext(ctx, "SELECT target,caller,name,path,start,end,kind,certainty,evidence FROM refs WHERE "+condition+" ORDER BY path,start,id LIMIT 51", queryArgs...)
		if e != nil {
			return nil, e
		}
		edges := []struct {
			edge Edge
			next string
		}{}
		count := 0
		for rows.Next() {
			var target, caller, name, path, kind, certainty, ev string
			var start, end int
			if e = rows.Scan(&target, &caller, &name, &path, &start, &end, &kind, &certainty, &ev); e != nil {
				rows.Close()
				return nil, e
			}
			count++
			if count > 50 {
				truncated = true
				unexamined = append(unexamined, map[string]any{"node_id": item.id, "reason": "fanout_limit", "limit": 50})
				break
			}
			next := target
			if impact {
				next = caller

			}
			edge := Edge{From: item.id, To: next, Kind: kind, Certainty: certainty, Evidence: ev, Path: path, Start: start, End: end}
			if kind == "CALLS" && certainty != "exact" {
				edge.Reason = "static graph cannot prove concrete runtime dispatch"
			}
			edges = append(edges, struct {
				edge Edge
				next string
			}{edge, next})
		}
		scanErr := rows.Err()
		rows.Close()
		if scanErr != nil {
			return nil, scanErr
		}
		for _, entry := range edges {
			edge, next := entry.edge, entry.next
			if impact && next == "" { // refs table records exact use position; locate enclosing declaration.
				_ = db.QueryRowContext(ctx, "SELECT id FROM symbols WHERE path=? AND start<=? AND end>=? ORDER BY end-start,id LIMIT 1", edge.Path, edge.Start, edge.End).Scan(&next)
				edge.To = next
			}
			addEdge(edge)
			if next == "" {
				gaps = append(gaps, "unresolved target/consumer at "+edge.Path)
				continue
			}
			if edge.Certainty != "exact" {
				continue
			}
			if _, ok := visited[next]; ok {
				continue
			}
			if item.depth >= depth {
				truncated = true
				unexamined = append(unexamined, map[string]any{"node_id": next, "reason": "depth_limit", "from": item.id})
				continue
			}
			if len(visited) >= limit {
				truncated = true
				unexamined = append(unexamined, map[string]any{"node_id": next, "reason": "node_limit"})
				continue
			}
			visited[next] = item.depth + 1
			queue = append(queue, queued{next, item.depth + 1})
		}
	}
	// Forward flow also reports uncertain calls, which otherwise lack a concrete reverse target.
	if impact {
		rows, e := db.QueryContext(ctx, "SELECT caller,name,path,start,end,certainty,evidence FROM refs WHERE kind='CALLS' AND certainty!='exact' ORDER BY path,start LIMIT 101")
		if e != nil {
			return nil, e
		}
		dynamicCount := 0
		possibleLimited := false
		for rows.Next() {
			var caller, name, path, certainty, ev string
			var start, end int
			if e = rows.Scan(&caller, &name, &path, &start, &end, &certainty, &ev); e != nil {
				rows.Close()
				return nil, e
			}
			dynamicCount++
			if dynamicCount > 100 {
				truncated = true
				unexamined = append(unexamined, map[string]any{"reason": "dynamic_call_scan_limit"})
				break
			}
			if _, seen := visited[caller]; seen || len(possible) < 50 {
				possible = append(possible, Edge{From: caller, Kind: "DYNAMIC_CALL", Certainty: certainty, Evidence: ev, Path: path, Start: start, End: end, Reason: "possible additional consumer; target cannot be determined statically"})
			} else {
				possibleLimited = true
				truncated = true
			}
		}
		if possibleLimited {
			unexamined = append(unexamined, map[string]any{"reason": "possible_consumer_limit", "limit": 50})
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	affected := []domain.BusinessRule{}
	for _, r := range g.Rules {
		hit := selectedRuleIDs[r.ID]
		for _, h := range r.Handlers {
			if _, ok := visited[h.Symbol]; ok {
				hit = true
			}
		}
		if hit {
			affected = append(affected, r)
			for _, h := range r.Handlers {
				addEdge(Edge{From: r.ID, To: h.Symbol, Kind: "HANDLER:" + h.Role, Certainty: h.Certainty, Evidence: h.Evidence.ID, Path: h.Evidence.Path, Start: h.Evidence.Start, End: h.Evidence.End})
			}
		}
	}
	for _, f := range g.Facts {
		if _, ok := visited[f.Owner]; ok {
			if len(facts) < 200 {
				facts = append(facts, f)
			} else {
				truncated = true
			}
		}
	}
	packages := map[string]bool{}
	products := map[string]bool{}
	for _, n := range nodes {
		packages[n.Package] = true
	}
	for _, r := range affected {
		for k, v := range r.Context {
			products[k+"="+v] = true
		}
		if len(r.Requirements) == 0 {
			gaps = append(gaps, "no revisioned requirement linked to rule "+r.Key)
		}
	}
	if !g.Configured {
		gaps = append(gaps, "domain adapters are not configured")
	}
	if len(tests) == 0 {
		gaps = append(gaps, "no statically connected test found; configure business fixtures and broader tests")
	}
	if len(possible) > 0 {
		gaps = append(gaps, "dynamic/interface/external calls prevent complete impact guarantees")
	}
	diagnostics := []domain.BusinessDiagnostic{}
	for _, d := range g.Diagnostics {
		if d.Evidence.Path == "" {
			diagnostics = append(diagnostics, d)
			continue
		}
		relevant := false
		for _, n := range nodes {
			if n.Path == d.Evidence.Path {
				relevant = true
			}
		}
		for _, r := range affected {
			if r.Evidence.Path == d.Evidence.Path {
				relevant = true
			}
		}
		if relevant {
			diagnostics = append(diagnostics, d)
		}
	}
	list := func(set map[string]bool) []string {
		out := []string{}
		for s := range set {
			out = append(out, s)
		}
		sort.Strings(out)
		return out
	}
	affectedCount := len(affected)
	if len(affected) > min(limit, 50) {
		affected = affected[:min(limit, 50)]
		truncated = true
		unexamined = append(unexamined, map[string]any{"reason": "affected_rule_detail_limit", "total": affectedCount, "continue": "rules search with context filters"})
	}
	for i := range affected {
		affected[i] = BriefRule(affected[i])
	}
	if len(diagnostics) > 100 {
		diagnostics = diagnostics[:100]
		truncated = true
		unexamined = append(unexamined, map[string]any{"reason": "diagnostic_limit", "continue": "rules diagnostics --offset"})
	}
	return map[string]any{"schema_version": "seai.impact/v1", "direction": map[bool]string{true: "reverse_consumers", false: "forward_calls"}[impact], "nodes": nodes, "confirmed": confirmed, "possible": possible, "unexamined": unexamined, "affected_rules": affected, "affected_rule_count": affectedCount, "affected_contexts": list(products), "affected_packages": list(packages), "related_tests": tests, "source_facts": facts, "coverage_gaps": gaps, "diagnostics": diagnostics, "truncated": truncated, "coverage": "partial", "limits": map[string]int{"depth": depth, "nodes": limit, "fanout": 50}, "limitations": g.Limitations}, nil
}
