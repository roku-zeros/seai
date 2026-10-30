package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildProfileRequiresCompatibleGo(t *testing.T) {
	for _, tc := range []struct {
		version   string
		wantError bool
	}{
		{"go1.22.1", true},
		{"go1.26.5", true},
		{"unknown", true},
		{"go1.26.6", false},
		{"go1.27.0", false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			dir := t.TempDir()
			script := "#!/bin/sh\nprintf '%s\\n' '" + tc.version + "' 'darwin' 'arm64'\n"
			if e := os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0700); e != nil {
				t.Fatal(e)
			}
			t.Setenv("PATH", dir)
			profile, e := BuildProfile(context.Background())
			if (e != nil) != tc.wantError {
				t.Fatalf("version=%s profile=%+v error=%v", tc.version, profile, e)
			}
			if !tc.wantError && profile.Toolchain != tc.version {
				t.Fatalf("wrong toolchain: %+v", profile)
			}
		})
	}
}
