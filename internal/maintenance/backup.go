// Package maintenance implements verified offline restores and staged upgrades.
package maintenance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"seai.local/seai/internal/contracts"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/storage"
	"seai.local/seai/internal/workspace"
	"strings"
	"syscall"
)

const Version = "0.3.0-dev"

func Lock(data string) (*os.File, error) {
	info, e := os.Lstat(data)
	if e != nil {
		return nil, e
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("runtime must be a real directory")
	}
	fd, e := syscall.Open(filepath.Join(data, "daemon.lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), "daemon.lock")
	stat, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	owner, ok := stat.Sys().(*syscall.Stat_t)
	if !stat.Mode().IsRegular() || !ok || int(owner.Uid) != os.Getuid() || stat.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, fmt.Errorf("private current-user lock required")
	}
	if e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, domain.Err("DAEMON_RUNNING", "stop seaid before offline maintenance")
	}
	return f, nil
}
func copyFile(source, dest string, mode os.FileMode) error {
	b, e := os.ReadFile(source)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
		return e
	}
	f, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if e != nil {
		return e
	}
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	return f.Close()
}
func Backup(ctx context.Context, db *sql.DB, data, destination string) (domain.BackupManifest, error) {
	manifest := domain.BackupManifest{Schema: "seai.backup/v1", Version: Version, APIVersion: domain.API, RuntimeSchema: 3, Files: []domain.File{}}
	if e := db.QueryRowContext(ctx, "SELECT coalesce(max(version),0) FROM schema_migrations").Scan(&manifest.RuntimeSchema); e != nil {
		return manifest, e
	}
	if manifest.RuntimeSchema < 2 || manifest.RuntimeSchema > 3 {
		return manifest, domain.Err("UNSUPPORTED_RUNTIME_SCHEMA", "backup supports schema 2 or 3")
	}
	if e := db.QueryRowContext(ctx, "SELECT id,root FROM projects").Scan(&manifest.Project, &manifest.Repository); e != nil {
		return manifest, e
	}
	if _, e := os.Lstat(destination); !os.IsNotExist(e) {
		return manifest, fmt.Errorf("backup destination must not exist")
	}
	if e := os.MkdirAll(filepath.Dir(destination), 0700); e != nil {
		return manifest, e
	}
	temp, e := os.MkdirTemp(filepath.Dir(destination), ".backup-")
	if e != nil {
		return manifest, e
	}
	defer os.RemoveAll(temp)
	// VACUUM INTO takes a consistent SQLite snapshot, including committed WAL content.
	quoted := strings.ReplaceAll(filepath.Join(temp, "runtime.db"), "'", "''")
	if _, e = db.ExecContext(ctx, "VACUUM INTO '"+quoted+"'"); e != nil {
		return manifest, e
	}
	for _, dir := range []string{"objects", "indexes"} {
		base := filepath.Join(data, dir)
		e = filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if os.IsNotExist(err) && path == base {
				return nil
			}
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink in runtime")
			}
			if entry.IsDir() {
				if strings.HasPrefix(entry.Name(), ".staging") || strings.HasPrefix(entry.Name(), ".building") {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("special file in runtime")
			}
			if strings.HasPrefix(entry.Name(), ".staging") || strings.HasPrefix(entry.Name(), ".building") {
				return nil
			}
			rel, e := filepath.Rel(data, path)
			if e != nil {
				return e
			}
			return copyFile(path, filepath.Join(temp, rel), 0600)
		})
		if e != nil {
			return manifest, e
		}
	}
	e = filepath.WalkDir(temp, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(temp, path)
		if e != nil {
			return e
		}
		manifest.Files = append(manifest.Files, domain.File{Path: filepath.ToSlash(rel), Mode: "100600", Digest: domain.Digest(b)})
		return nil
	})
	if e != nil {
		return manifest, e
	}
	raw, e := domain.Canonical(manifest)
	if e != nil {
		return manifest, e
	}
	if e = os.WriteFile(filepath.Join(temp, "manifest.json"), raw, 0600); e != nil {
		return manifest, e
	}
	if e = storage.SyncDir(temp); e != nil {
		return manifest, e
	}
	if e = os.Rename(temp, destination); e != nil {
		return manifest, e
	}
	e = storage.SyncDir(filepath.Dir(destination))
	return manifest, e
}
func Restore(ctx context.Context, backup, destination string) (domain.BackupManifest, error) {
	var m domain.BackupManifest
	raw, e := os.ReadFile(filepath.Join(backup, "manifest.json"))
	if e != nil {
		return m, e
	}
	if len(raw) > 16777216 {
		return m, fmt.Errorf("manifest too large")
	}
	if e = contracts.StrictJSON(raw, &m); e != nil {
		return m, e
	}
	if m.Schema != "seai.backup/v1" || m.APIVersion != domain.API || m.RuntimeSchema < 2 || m.RuntimeSchema > 3 {
		return m, domain.Err("UNSUPPORTED_BACKUP", "backup API/runtime not supported")
	}
	dest, e := filepath.Abs(destination)
	if e != nil {
		return m, e
	}
	if _, e = os.Lstat(dest); !os.IsNotExist(e) {
		return m, fmt.Errorf("restore requires a new destination; existing data is never overwritten")
	}
	if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
		return m, e
	}
	temp, e := os.MkdirTemp(filepath.Dir(dest), ".restore-")
	if e != nil {
		return m, e
	}
	defer os.RemoveAll(temp)
	seen := map[string]bool{}
	for _, file := range m.Files {
		if !workspace.ValidPath(file.Path) || seen[file.Path] || !(file.Path == "runtime.db" || strings.HasPrefix(file.Path, "objects/sha256/") || strings.HasPrefix(file.Path, "indexes/")) {
			return m, fmt.Errorf("invalid/duplicate backup path")
		}
		seen[file.Path] = true
		source := backup
		for _, part := range strings.Split(file.Path, "/") {
			source = filepath.Join(source, part)
			info, e := os.Lstat(source)
			if e != nil {
				return m, e
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return m, fmt.Errorf("backup symlink rejected")
			}
		}
		b, e := os.ReadFile(source)
		if e != nil {
			return m, e
		}
		if strings.HasPrefix(file.Path, "objects/sha256/") && filepath.Base(file.Path) != domain.Digest(b) {
			return m, domain.Err("BACKUP_CORRUPT", "CAS object name/content mismatch")
		}
		if domain.Digest(b) != file.Digest {
			return m, domain.Err("BACKUP_CORRUPT", "digest mismatch: %s", file.Path)
		}
		if e = copyFile(source, filepath.Join(temp, file.Path), 0600); e != nil {
			return m, e
		}
	}
	if !seen["runtime.db"] {
		return m, fmt.Errorf("backup lacks runtime database")
	}
	store, e := storage.Open(temp)
	if e != nil {
		return m, e
	}
	rows, e := store.DB.QueryContext(ctx, "SELECT id,path FROM index_generations")
	if e != nil {
		store.DB.Close()
		return m, e
	}
	paths := map[string]string{}
	for rows.Next() {
		var id, path string
		if e = rows.Scan(&id, &path); e != nil {
			rows.Close()
			store.DB.Close()
			return m, e
		}
		if !workspace.ValidPath(id) || strings.Contains(id, "/") {
			rows.Close()
			store.DB.Close()
			return m, fmt.Errorf("invalid generation ID")
		}
		paths[id] = filepath.Join(dest, "indexes", id, "index.db")
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		store.DB.Close()
		return m, e
	}
	for id, path := range paths {
		if !seen["indexes/"+id+"/index.db"] {
			store.DB.Close()
			return m, fmt.Errorf("backup lacks registered index %s", id)
		}
		if _, e = store.DB.ExecContext(ctx, "UPDATE index_generations SET path=? WHERE id=?", path, id); e != nil {
			store.DB.Close()
			return m, e
		}
	}
	e = CheckFiles(ctx, temp, store.DB)
	store.DB.Close()
	if e != nil {
		return m, e
	}
	if e = os.Rename(temp, dest); e != nil {
		return m, e
	}
	e = storage.SyncDir(filepath.Dir(dest))
	return m, e
}
func Check(ctx context.Context, db *sql.DB) error {
	var integrity string
	if e := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); e != nil {
		return e
	}
	if integrity != "ok" {
		return fmt.Errorf("integrity check failed")
	}
	rows, e := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if e != nil {
		return e
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("foreign key check failed")
	}
	return rows.Err()
}
func VersionJSON() []byte {
	b, _ := json.Marshal(map[string]any{"product_version": Version, "api_version": domain.API, "runtime_schema": 3})
	return b
}

// CheckFiles validates referenced CAS and immutable indexes under this runtime root.
func CheckFiles(ctx context.Context, data string, db *sql.DB) error {
	if e := Check(ctx, db); e != nil {
		return e
	}
	cas := storage.CAS{Root: data}
	rows, e := db.QueryContext(ctx, "SELECT id,manifest_digest FROM source_snapshots")
	if e != nil {
		return e
	}
	manifests := map[string]string{}
	for rows.Next() {
		var id, d string
		if e = rows.Scan(&id, &d); e != nil {
			rows.Close()
			return e
		}
		manifests[id] = d
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for id, d := range manifests {
		b, e := cas.Get(d)
		if e != nil {
			return e
		}
		var m domain.Manifest
		if e = json.Unmarshal(b, &m); e != nil {
			return e
		}
		actual, e := m.ID()
		if e != nil || actual != id {
			return domain.Err("STORAGE_CORRUPT", "snapshot manifest differs")
		}
		for _, f := range m.Files {
			if !workspace.ValidSourcePath(f.Path) {
				return domain.Err("STORAGE_CORRUPT", "invalid source path")
			}
			if _, e = cas.Get(f.Digest); e != nil {
				return e
			}
		}
	}
	rows, e = db.QueryContext(ctx, "SELECT id FROM index_generations")
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		if !workspace.ValidPath(id) || strings.Contains(id, "/") {
			return fmt.Errorf("invalid generation ID")
		}
		path := filepath.Join(data, "indexes", id, "index.db")
		info, e := os.Lstat(path)
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("index is not a regular file")
		}
		u := url.URL{Scheme: "file", Path: path}
		index, e := sql.Open("sqlite3", u.String()+"?mode=ro&immutable=1")
		if e != nil {
			return e
		}
		var status string
		e = index.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&status)
		index.Close()
		if e != nil {
			return e
		}
		if status != "ok" {
			return fmt.Errorf("index integrity check failed")
		}
	}
	return nil
}
