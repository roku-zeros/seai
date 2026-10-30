package storage

import (
	"os"
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
