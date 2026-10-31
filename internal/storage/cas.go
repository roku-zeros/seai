package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"seai.local/seai/internal/domain"
)

type CAS struct{ Root string }

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (c CAS) path(id string) (string, error) {
	if !digestPattern.MatchString(id) {
		return "", domain.Err("INVALID_DIGEST", "expected SHA-256")
	}
	return filepath.Join(c.Root, "objects", "sha256", id[:2], id), nil
}
func SyncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func (c CAS) Put(b []byte) (string, error) {
	id := domain.Digest(b)
	path, _ := c.path(id)
	if existing, e := os.ReadFile(path); e == nil {
		if domain.Digest(existing) != id {
			return "", domain.Err("CAS_CORRUPT", "existing object differs")
		}
		return id, nil
	} else if !os.IsNotExist(e) {
		return "", e
	}
	_, statErr := os.Stat(filepath.Dir(path))
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return "", e
	}
	if os.IsNotExist(statErr) {
		for _, dir := range []string{c.Root, filepath.Join(c.Root, "objects"), filepath.Join(c.Root, "objects", "sha256"), filepath.Dir(path)} {
			if e := SyncDir(dir); e != nil {
				return "", e
			}
		}
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".staging-")
	if e != nil {
		return "", e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e != nil {
		f.Close()
		return "", e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return "", e
	}
	if e = f.Close(); e != nil {
		return "", e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return "", e
	}
	if e = SyncDir(filepath.Dir(path)); e != nil {
		return "", e
	}
	return id, nil
}
func (c CAS) Get(id string) ([]byte, error) {
	p, e := c.path(id)
	if e != nil {
		return nil, e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	if domain.Digest(b) != id {
		return nil, fmt.Errorf("CAS_CORRUPT: %s", id)
	}
	return b, nil
}
