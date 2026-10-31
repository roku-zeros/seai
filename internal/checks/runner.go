// Package checks executes only registered argv profiles in private snapshot copies.
package checks

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/storage"
	"seai.local/seai/internal/workspace"
)

func Validate(p domain.CheckProfile) error {
	if !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`).MatchString(p.ID) || p.TimeoutSeconds < 1 || p.TimeoutSeconds > 900 || p.MaxOutputBytes < 1024 || p.MaxOutputBytes > 1048576 {
		return domain.Err("SCHEMA_INVALID", "check ID/time/output bounds invalid")
	}
	if p.Kind != "compile" && p.Kind != "tests" && p.Kind != "comparison" {
		return domain.Err("SCHEMA_INVALID", "check kind")
	}
	if len(p.Args) == 0 || len(p.Args) > 64 {
		return domain.Err("SCHEMA_INVALID", "registered argv required")
	}
	if p.Isolation == "local-approved" {
		if !p.AcknowledgeLocal || !filepath.IsAbs(p.Program) {
			return domain.Err("ISOLATION_NOT_APPROVED", "local profile requires absolute executable and explicit local-execution acknowledgement")
		}
		info, e := os.Stat(p.Program)
		if e != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
			return domain.Err("PROFILE_UNAVAILABLE", "program is not executable")
		}
	} else if p.Isolation == "docker" {
		if !regexp.MustCompile(`^[a-zA-Z0-9./:_-]+@sha256:[a-f0-9]{64}$`).MatchString(p.DockerImage) || p.CPUs < 1 || p.CPUs > 8 || p.MemoryMB < 256 || p.MemoryMB > 8192 {
			return domain.Err("SCHEMA_INVALID", "Docker requires pinned image and CPU/RAM limits")
		}
		if p.Program == "" || strings.HasPrefix(p.Program, "-") {
			return domain.Err("SCHEMA_INVALID", "container program")
		}
	} else {
		return domain.Err("ISOLATION_NOT_APPROVED", "supported isolation: docker or explicit local-approved")
	}
	if p.Kind == "tests" {
		if len(p.TestNames) == 0 || len(p.TestNames) > 100 {
			return domain.Err("SCHEMA_INVALID", "tests profile requires exact expected test names")
		}
		seen := map[string]bool{}
		for _, n := range p.TestNames {
			if n == "" || seen[n] {
				return domain.Err("SCHEMA_INVALID", "unique test names required")
			}
			seen[n] = true
		}
	}
	if p.Kind == "comparison" {
		if len(p.Fixtures) == 0 || len(p.Fixtures) > 100 || p.AssertionProfile == "" {
			return domain.Err("SCHEMA_INVALID", "comparison needs fixtures and assertion_profile")
		}
		seen := map[string]bool{}
		for _, f := range p.Fixtures {
			if f.ID == "" || seen[f.ID] || len(f.Input) > 65536 || len(f.Expected) == 0 || len(f.Expected) > 65536 || len(f.Criteria) == 0 {
				return domain.Err("SCHEMA_INVALID", "invalid comparison fixture")
			}
			seen[f.ID] = true
			if _, e := ParseJSON(f.Expected); e != nil {
				return e
			}
			for _, path := range f.AllowedPaths {
				if path != "" && !strings.HasPrefix(path, "/") {
					return domain.Err("SCHEMA_INVALID", "allowed paths must be JSON pointers")
				}
			}
		}
	}
	return nil
}
func ProfileSummaries(profiles map[string]domain.CheckProfile) []map[string]any {
	out := []map[string]any{}
	for _, p := range profiles {
		d, _ := json.Marshal(p)
		f, _ := json.Marshal(p.Fixtures)
		out = append(out, map[string]any{"id": p.ID, "kind": p.Kind, "isolation": p.Isolation, "profile_digest": domain.Digest(d), "fixture_digest": domain.Digest(f), "assertion_profile": p.AssertionProfile, "test_names": p.TestNames, "fixtures": p.Fixtures, "limits": map[string]any{"timeout_seconds": p.TimeoutSeconds, "cpus": p.CPUs, "memory_mb": p.MemoryMB}, "local_execution_is_not_sandboxed": p.Isolation == "local-approved"})
	}
	return out
}
func FixtureDigest(p domain.CheckProfile) string {
	b, _ := json.Marshal(p.Fixtures)
	return domain.Digest(b)
}

type capped struct {
	mu        sync.Mutex
	b         bytes.Buffer
	limit     int
	truncated bool
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	remaining := c.limit - c.b.Len()
	if len(p) > remaining {
		c.truncated = true
	}
	if remaining > 0 {
		c.b.Write(p[:min(remaining, len(p))])
	}
	return len(p), nil
}

type ProcessResult struct {
	Stdout, Stderr []byte
	Status         string
	Exit           int
	Duration       time.Duration
	Error          string
}

func Execute(ctx context.Context, p domain.CheckProfile, m domain.Manifest, cas storage.CAS, parent, input string) ProcessResult {
	started := time.Now()
	result := ProcessResult{Status: "FAILED", Exit: -1}
	root, e := os.MkdirTemp(parent, "check-")
	if e != nil {
		result.Error = e.Error()
		return result
	}
	defer os.RemoveAll(root)
	tree := filepath.Join(root, "source")
	if e = os.MkdirAll(tree, 0700); e != nil {
		result.Error = e.Error()
		return result
	}
	if e = workspace.Materialize(m, cas, tree); e != nil {
		result.Error = e.Error()
		return result
	}
	// Copies are disposable; generated files never enter the snapshot or user's checkout.
	for _, f := range m.Files {
		mode := os.FileMode(0600)
		if f.Mode == "100755" {
			mode = 0700
		}
		if e = os.Chmod(filepath.Join(tree, f.Path), mode); e != nil {
			result.Error = e.Error()
			return result
		}
	}
	tmp := filepath.Join(root, "tmp")
	cache := filepath.Join(root, "cache")
	_ = os.MkdirAll(tmp, 0700)
	_ = os.MkdirAll(cache, 0700)
	command, args := p.Program, append([]string{}, p.Args...)
	if p.Isolation == "docker" {
		docker, e := exec.LookPath("docker")
		if e != nil {
			result.Status = "UNSUPPORTED"
			result.Error = "Docker not available"
			return result
		}
		// No host credentials, network, user Docker socket, writable root or implicit image pull.
		args = append([]string{"run", "-i", "--name", "seai-" + filepath.Base(root), "--rm", "--pull=never", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=128", "--memory", fmt.Sprintf("%dm", p.MemoryMB), "--cpus", fmt.Sprint(p.CPUs), "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--mount", "type=bind,src=" + tree + ",dst=/work", "--tmpfs", "/tmp:rw,nosuid,nodev,size=256m", "--workdir", "/work", "-e", "GOPROXY=off", "-e", "GOSUMDB=off", "-e", "GOTOOLCHAIN=local", "-e", "GOWORK=off", "-e", "GOENV=off", "-e", "GOCACHE=/tmp/go-build", "-e", "GOMODCACHE=/tmp/go-mod", p.DockerImage, p.Program}, p.Args...)
		command = docker
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = exec.CommandContext(cleanup, docker, "rm", "-f", "seai-"+filepath.Base(root)).Run()
		}()
	}
	deadline, cancel := context.WithTimeout(ctx, time.Duration(p.TimeoutSeconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(deadline, command, args...)
	cmd.Dir = tree
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TMPDIR=" + tmp, "GOCACHE=" + cache, "GOENV=off", "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "GOWORK=off", "GOFLAGS=-mod=readonly", "CGO_ENABLED=0", "TZ=UTC", "SEAI_TEST_SEED=1", "SOURCE_DATE_EPOCH=0"}
	for _, k := range []string{"GOMODCACHE", "GOROOT", "GOPATH"} {
		if v := os.Getenv(k); v != "" {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	out, errout := &capped{limit: p.MaxOutputBytes}, &capped{limit: p.MaxOutputBytes}
	cmd.Stdout = out
	cmd.Stderr = errout
	cmd.Stdin = strings.NewReader(input)
	e = cmd.Run()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	result.Stdout = out.b.Bytes()
	result.Stderr = errout.b.Bytes()
	result.Duration = time.Since(started)
	if cmd.ProcessState != nil {
		result.Exit = cmd.ProcessState.ExitCode()
	}
	switch {
	case ctx.Err() != nil:
		result.Status = "CANCELLED"
		result.Error = ctx.Err().Error()
	case deadline.Err() != nil:
		result.Status = "TIMED_OUT"
		result.Error = deadline.Err().Error()
	case out.truncated || errout.truncated:
		result.Status = "OUTPUT_LIMIT"
		result.Error = "bounded output exceeded"
	case e != nil:
		result.Error = e.Error()
	default:
		result.Status = "PASSED"
	}
	return result
}
func Run(ctx context.Context, p domain.CheckProfile, base, candidate domain.Manifest, spec domain.ChangeSpec, cas storage.CAS, parent string) domain.CheckResult {
	started := time.Now()
	r := domain.CheckResult{ProfileID: p.ID, Status: "FAILED", ExitCode: -1, Tests: []string{}, Criteria: []string{}, Differences: []domain.Difference{}, Isolation: p.Isolation}
	if e := Validate(p); e != nil {
		r.Error = e.Error()
		return r
	}
	if p.Kind == "comparison" {
		r = compare(ctx, p, base, candidate, spec, cas, parent)
	} else {
		process := Execute(ctx, p, candidate, cas, parent, "")
		r.Status = process.Status
		r.ExitCode = process.Exit
		r.Error = process.Error
		r.StdoutDigest, _ = cas.Put(process.Stdout)
		r.StderrDigest, _ = cas.Put(process.Stderr)
		if r.Status == "PASSED" && p.Kind == "tests" {
			passed, e := parseTests(process.Stdout, p.TestNames)
			if e != nil {
				r.Status = "FAILED"
				r.Error = e.Error()
			} else {
				r.Tests = passed
			}
		}
	}
	r.DurationMS = time.Since(started).Milliseconds()
	return r
}
func parseTests(b []byte, required []string) ([]string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Buffer(make([]byte, 4096), 1048576)
	passed := map[string]bool{}
	started := map[string]bool{}
	failed := false
	terminalPackage := false
	for scanner.Scan() {
		var event struct{ Action, Package, Test string }
		if e := json.Unmarshal(scanner.Bytes(), &event); e != nil {
			return nil, fmt.Errorf("invalid go test -json event")
		}
		if event.Action == "run" && event.Test != "" {
			started[event.Test] = true
		}
		if event.Action == "fail" {
			failed = true
		}
		if event.Action == "pass" {
			if event.Test == "" {
				terminalPackage = true
			} else if started[event.Test] {
				passed[event.Test] = true
			}
		}
	}
	if e := scanner.Err(); e != nil {
		return nil, e
	}
	if failed || !terminalPackage {
		return nil, fmt.Errorf("test run failed or lacks terminal package pass")
	}
	result := []string{}
	for _, name := range required {
		if !passed[name] {
			return nil, fmt.Errorf("required test did not execute and pass: %s (use -count=1)", name)
		}
		result = append(result, name)
	}
	return result, nil
}

var _ io.Writer = (*capped)(nil)
