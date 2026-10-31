package maintenance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"seai.local/seai/internal/contracts"
	"seai.local/seai/internal/domain"
)

// Upgrade prepares a verified new runtime and binaries. Activation is explicit;
// source data and binaries remain untouched, so rollback is switching back before new work.
func Upgrade(ctx context.Context, data, bundle, destination string) (map[string]any, error) {
	absolute, e := filepath.Abs(destination)
	if e != nil {
		return nil, e
	}
	destination = absolute
	lock, e := Lock(data)
	if e != nil {
		return nil, e
	}
	defer lock.Close()
	raw, e := os.ReadFile(bundle)
	if e != nil {
		return nil, e
	}
	var m domain.UpgradeBundle
	if e = contracts.StrictJSON(raw, &m); e != nil {
		return nil, e
	}
	if m.Schema != "seai.upgrade/v1" || m.Version != Version || m.API != domain.API || m.RuntimeSchema != 3 {
		return nil, domain.Err("UNSUPPORTED_UPGRADE", "only this binary's API/version/runtime bundle supported")
	}
	resolve := func(path, digest string) (string, error) {
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(bundle), path)
		}
		info, e := os.Lstat(path)
		if e != nil {
			return "", e
		}
		if !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
			return "", fmt.Errorf("bundle executable must be regular and executable")
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return "", e
		}
		if domain.Digest(b) != digest {
			return "", domain.Err("BUNDLE_CORRUPT", "binary digest differs")
		}
		return path, nil
	}
	cli, e := resolve(m.Seai, m.SeaiDigest)
	if e != nil {
		return nil, e
	}
	daemon, e := resolve(m.Seaid, m.SeaidDigest)
	if e != nil {
		return nil, e
	}
	// The bundle itself is executable code supplied by the operator; hashes establish identity, not trust.
	version, e := exec.CommandContext(ctx, daemon, "--version").Output()
	if e != nil {
		return nil, e
	}
	var v struct {
		Version string `json:"product_version"`
		API     string `json:"api_version"`
		Schema  int    `json:"runtime_schema"`
	}
	if json.Unmarshal(version, &v) != nil || v.Version != m.Version || v.API != m.API || v.Schema != m.RuntimeSchema {
		return nil, fmt.Errorf("daemon manifest does not match bundle")
	}
	if _, e = os.Lstat(destination); !os.IsNotExist(e) {
		return nil, fmt.Errorf("upgrade destination must not exist")
	}
	if e = os.MkdirAll(filepath.Dir(destination), 0700); e != nil {
		return nil, e
	}
	stage, e := os.MkdirTemp(filepath.Dir(destination), ".upgrade-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(stage)
	uri := url.URL{Scheme: "file", Path: filepath.Join(data, "runtime.db")}
	source, e := sql.Open("sqlite3", uri.String()+"?mode=ro")
	if e != nil {
		return nil, e
	}
	backup := filepath.Join(stage, "backup")
	_, e = Backup(ctx, source, data, backup)
	source.Close()
	if e != nil {
		return nil, e
	}
	// Use final path for absolute immutable index references, then remove the whole new tree on failure.
	if e = os.Mkdir(destination, 0700); e != nil {
		return nil, e
	}
	success := false
	defer func() {
		if !success {
			os.RemoveAll(destination)
		}
	}()
	target := filepath.Join(destination, "data")
	_, e = Restore(ctx, backup, target)
	if e != nil {
		return nil, e
	}
	if e = copyFile(cli, filepath.Join(destination, "seai"), 0700); e != nil {
		return nil, e
	}
	if e = copyFile(daemon, filepath.Join(destination, "seaid"), 0700); e != nil {
		return nil, e
	}
	cmd := exec.CommandContext(ctx, filepath.Join(destination, "seaid"), "--data", target, "--check-data")
	if e = cmd.Run(); e != nil {
		return nil, fmt.Errorf("staged daemon smoke check failed: %w", e)
	}
	success = true
	return map[string]any{"installation": destination, "data": target, "version": m.Version, "activated": false, "previous_data": data, "rollback": "Before accepting new work: stop staged daemon and restart previous binaries with previous_data. After new work use a new verified backup/restore; never discard it."}, nil
}
