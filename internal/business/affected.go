package business

import (
	"context"
	"database/sql"
	"seai.local/seai/internal/domain"
)

// AffectedRules walks reverse source dependencies from scoped declarations.
// It does not filter by product: a shared helper can affect multiple contexts.
func AffectedRules(ctx context.Context, db *sql.DB, g domain.BusinessIndex, paths []string) ([]domain.BusinessRule, []string, error) {
	scope := map[string]bool{}
	for _, p := range paths {
		scope[p] = true
	}
	seen := map[string]int{}
	queue := []string{}
	gaps := []string{}
	limited := false
	for _, path := range paths {
		rows, e := db.QueryContext(ctx, "SELECT id FROM symbols WHERE path=? ORDER BY start,id LIMIT 501", path)
		if e != nil {
			return nil, nil, e
		}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return nil, nil, e
			}
			if len(seen) >= 500 {
				limited = true
				break
			}
			if _, ok := seen[id]; !ok {
				seen[id] = 0
				queue = append(queue, id)
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, nil, e
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		rows, e := db.QueryContext(ctx, "SELECT caller,path,start,end FROM refs WHERE target=? AND certainty='exact' ORDER BY path,start LIMIT 51", id)
		if e != nil {
			return nil, nil, e
		}
		type use struct {
			caller, path string
			start, end   int
		}
		uses := []use{}
		count := 0
		for rows.Next() {
			var u use
			if e = rows.Scan(&u.caller, &u.path, &u.start, &u.end); e != nil {
				rows.Close()
				return nil, nil, e
			}
			count++
			if count > 50 {
				limited = true
				break
			}
			uses = append(uses, u)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, nil, e
		}
		for _, u := range uses {
			next := u.caller
			if next == "" {
				err := db.QueryRowContext(ctx, "SELECT id FROM symbols WHERE path=? AND start<=? AND end>=? ORDER BY end-start,id LIMIT 1", u.path, u.start, u.end).Scan(&next)
				if err != nil && err != sql.ErrNoRows {
					return nil, nil, err
				}
			}
			if next == "" {
				continue
			}
			if _, ok := seen[next]; ok {
				continue
			}
			if len(seen) >= 500 || seen[id] >= 8 {
				limited = true
				continue
			}
			seen[next] = seen[id] + 1
			queue = append(queue, next)
		}
	}
	out := []domain.BusinessRule{}
	for _, r := range g.Rules {
		affected := scope[r.Evidence.Path]
		for _, h := range r.Handlers {
			if _, ok := seen[h.Symbol]; ok {
				affected = true
			}
		}
		if affected {
			out = append(out, r)
		}
	}
	if limited {
		gaps = append(gaps, "affected-rule traversal hit node/depth/fanout limits; broaden analysis before accepting changes")
	}
	return out, gaps, nil
}
