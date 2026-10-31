package storage

import (
	"database/sql"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"net/url"
	"path/filepath"
	"seai.local/seai/internal/domain"
	"seai.local/seai/migrations"
	"time"
)

var migration = migrations.Foundation

type Runtime struct{ DB *sql.DB }

func Open(root string) (*Runtime, error) {
	u := url.URL{Scheme: "file", Path: filepath.Join(root, "runtime.db")}
	db, e := sql.Open("sqlite3", u.String()+"?_foreign_keys=on&_journal_mode=WAL&_synchronous=FULL&_busy_timeout=5000")
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	fail := func(e error) (*Runtime, error) { db.Close(); return nil, e }
	if _, e = db.Exec("CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY,checksum TEXT NOT NULL)"); e != nil {
		return fail(e)
	}
	for _, m := range []struct {
		version int
		sql     string
	}{{1, migration}, {2, migrations.Workflow}} {
		var sum string
		e = db.QueryRow("SELECT checksum FROM schema_migrations WHERE version=?", m.version).Scan(&sum)
		if e == sql.ErrNoRows {
			tx, err := db.Begin()
			if err != nil {
				return fail(err)
			}
			if _, err = tx.Exec(m.sql); err != nil {
				tx.Rollback()
				return fail(err)
			}
			if _, err = tx.Exec("INSERT INTO schema_migrations VALUES(?,?)", m.version, domain.Digest([]byte(m.sql))); err != nil {
				tx.Rollback()
				return fail(err)
			}
			e = tx.Commit()
		} else if e == nil && sum != domain.Digest([]byte(m.sql)) {
			e = fmt.Errorf("MIGRATION_CHECKSUM_MISMATCH: %d", m.version)
		}
		if e != nil {
			return fail(e)
		}
	}
	if _, e = db.Exec("UPDATE jobs SET state='ABORTED',error='daemon restarted; submit a new operation_id' WHERE state IN ('QUEUED','RUNNING')"); e != nil {
		return fail(e)
	}
	return &Runtime{db}, nil
}
func (r *Runtime) Register(id, root string) error {
	var oldID, oldRoot string
	e := r.DB.QueryRow("SELECT id,root FROM projects LIMIT 1").Scan(&oldID, &oldRoot)
	if e == nil {
		if oldID != id || oldRoot != root {
			return domain.Err("PROJECT_MISMATCH", "data directory belongs to %s at %s", oldID, oldRoot)
		}
		return nil
	}
	if e != sql.ErrNoRows {
		return e
	}
	_, e = r.DB.Exec("INSERT INTO projects(id,root) VALUES(?,?)", id, root)
	return e
}
func (r *Runtime) Publish(project, id, snapshot, path, coverage string, epoch int, job string) error {
	tx, e := r.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("INSERT OR IGNORE INTO source_snapshots VALUES(?,?)", snapshot, snapshot); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT OR IGNORE INTO index_generations VALUES(?,?,?,?, 'READY')", id, snapshot, path, coverage); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE index_generations SET state='READY' WHERE id=?", id); e != nil {
		return e
	}
	result, e := tx.Exec("UPDATE projects SET current_generation_id=?,epoch=epoch+CASE WHEN current_generation_id=? THEN 0 ELSE 1 END WHERE id=? AND epoch=?", id, id, project, epoch)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return domain.Err("REVISION_CONFLICT", "project epoch changed")
	}
	payload, _ := domain.Canonical(map[string]string{"source_snapshot_id": snapshot, "index_generation_id": id, "coverage": coverage})
	jobUpdate, e := tx.Exec("UPDATE jobs SET state='SUCCEEDED',result=? WHERE id=? AND state='RUNNING'", string(payload), job)
	if e != nil {
		return e
	}
	n, e = jobUpdate.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return domain.Err("REVISION_CONFLICT", "job is not RUNNING")
	}

	if _, e = tx.Exec("INSERT INTO events(type,payload,occurred_at) VALUES('INDEX_PUBLISHED',?,?)", id, time.Now().UTC().Format(time.RFC3339Nano)); e != nil {
		return e
	}
	return tx.Commit()
}
