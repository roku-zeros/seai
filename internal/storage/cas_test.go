package storage

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"seai.local/seai/internal/domain"
	"testing"
)

func TestCASIntegrityAndTraversal(t *testing.T) {
	c := CAS{Root: t.TempDir()}
	id, e := c.Put([]byte("source"))
	if e != nil {
		t.Fatal(e)
	}
	b, e := c.Get(id)
	if e != nil || string(b) != "source" {
		t.Fatal(string(b), e)
	}
	if _, e = c.Get("../../secret"); e == nil {
		t.Fatal("invalid digest accepted")
	}
	p, _ := c.path(id)
	if e = os.WriteFile(p, []byte("tampered"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Get(id); e == nil {
		t.Fatal("corruption accepted")
	}
}
func TestMigrationChecksumAndRecovery(t *testing.T) {
	root := t.TempDir()
	r, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.DB.Exec("INSERT INTO jobs VALUES('op','digest','RUNNING',NULL,NULL)"); e != nil {
		t.Fatal(e)
	}
	r.DB.Close()
	r, e = Open(root)
	if e != nil {
		t.Fatal(e)
	}
	var state string
	if e = r.DB.QueryRow("SELECT state FROM jobs WHERE id='op'").Scan(&state); e != nil || state != "ABORTED" {
		t.Fatal(state, e)
	}
	if _, e = r.DB.Exec("UPDATE schema_migrations SET checksum='corrupt'"); e != nil {
		t.Fatal(e)
	}
	r.DB.Close()
	if r, e = Open(root); e == nil {
		r.DB.Close()
		t.Fatal("migration drift accepted")
	}
}
func TestPublicationRollback(t *testing.T) {
	r, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer r.DB.Close()
	if e = r.Register("p", "/repo"); e != nil {
		t.Fatal(e)
	}
	if _, e = r.DB.Exec("INSERT INTO jobs VALUES('build','digest','RUNNING',NULL,NULL)"); e != nil {
		t.Fatal(e)
	}
	if e = r.Publish("p", "gen", "source", "/index.db", "partial", 9, "build"); e == nil {
		t.Fatal("stale epoch published")
	}
	var count int
	if e = r.DB.QueryRow("SELECT count(*) FROM index_generations").Scan(&count); e != nil || count != 0 {
		t.Fatal("partial transaction persisted", count, e)
	}
	if e = r.Publish("p", "gen", "source", "/index.db", "partial", 0, "build"); e != nil {
		t.Fatal(e)
	}
	var state string
	if e = r.DB.QueryRow("SELECT state FROM jobs WHERE id='build'").Scan(&state); e != nil || state != "SUCCEEDED" {
		t.Fatal(state, e)
	}
}
func TestMigrationFromFoundation(t *testing.T) {
	root := t.TempDir()
	u := url.URL{Scheme: "file", Path: filepath.Join(root, "runtime.db")}
	db, e := sql.Open("sqlite3", u.String())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(migration); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO schema_migrations VALUES(1,?)", domain.Digest([]byte(migration))); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO projects(id,root) VALUES('existing','/repo')"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	r, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer r.DB.Close()
	var id string
	if e = r.DB.QueryRow("SELECT id FROM projects").Scan(&id); e != nil || id != "existing" {
		t.Fatal("project lost", id, e)
	}
	var count int
	if e = r.DB.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&count); e != nil || count != 3 {
		t.Fatal(count, e)
	}
}

func TestResearchRestartFencesQueuedAndRunningJobs(t *testing.T) {
	root := t.TempDir()
	r, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.DB.Exec("INSERT INTO runs(id,kind,state,fingerprint,task_revision,epoch,created_at) VALUES('research','research.session','RUNNING','fingerprint',0,0,'2026-11-02T00:00:00Z')"); e != nil {
		t.Fatal(e)
	}
	for _, state := range []string{"QUEUED", "RUNNING", "SUCCEEDED"} {
		if _, e = r.DB.Exec("INSERT INTO execution_nodes(id,run_id,name,role,state,input_digest,attempt,lease_epoch) VALUES(?,'research',?,'researcher',?,'input',0,1)", state, state, state); e != nil {
			t.Fatal(e)
		}
	}
	r.DB.Close()
	r, e = Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer r.DB.Close()
	var run string
	if e = r.DB.QueryRow("SELECT state FROM runs WHERE id='research'").Scan(&run); e != nil || run != "ABORTED" {
		t.Fatal(run, e)
	}
	for _, id := range []string{"QUEUED", "RUNNING", "SUCCEEDED"} {
		var state string
		var lease int
		if e = r.DB.QueryRow("SELECT state,lease_epoch FROM execution_nodes WHERE id=?", id).Scan(&state, &lease); e != nil {
			t.Fatal(e)
		}
		if id == "SUCCEEDED" {
			if state != id || lease != 1 {
				t.Fatal(state, lease)
			}
		} else if state != "ABORTED" || lease != 2 {
			t.Fatal(state, lease)
		}
	}
}
