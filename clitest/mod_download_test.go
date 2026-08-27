package clitest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// stubGoModDownloadPATH returns a PATH directory containing a `go` stub that
// answers the two subcommands mod-download shells out to (`go mod download`
// and, when computing the cache dir via `go env`, `go env -json`) without
// touching the network or a real module cache. Every invocation's args are
// appended to logFile (one line each), so tests can assert on which
// subcommands actually ran. modCacheDir is reported as GOMODCACHE by the
// `env -json` stub; `go mod download` exits with downloadExit.
func stubGoModDownloadPATH(t *testing.T, logFile, modCacheDir string, downloadExit int) string {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %q
case "$1 $2" in
  "mod download")
    exit %d
    ;;
  "env -json")
    echo '{"GOMODCACHE":%q}'
    exit 0
    ;;
esac
exit 1
`, logFile, downloadExit, modCacheDir)
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0750)) //nolint:gosec
	return dir
}

// TestCLI_ModDownload_CacheRoundTrip verifies the full cache round-trip: the
// first invocation runs `go mod download` and saves GOMODCACHE (as reported
// by `go env -json`) to the cache; with GOMODCACHE cleared, a second
// invocation under the same checksum restores it before `go mod download`
// (rather than downloading again) and re-saves it.
func TestCLI_ModDownload_CacheRoundTrip(t *testing.T) {
	s, run := sandbox(t)

	modCacheDir := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(modCacheDir, "marker"), []byte("hit"), 0600))

	logFile := filepath.Join(t.TempDir(), "go-stub.log")
	env := map[string]string{"PATH": stubGoModDownloadPATH(t, logFile, modCacheDir, 0)}

	out, err := run(t, env, "mod-download", "--checksum", "checksum-a")
	assert.NilError(t, err, "first run failed: %s", out)

	// The save is registered to run at the end of the job, so it has not
	// happened yet when the command exits.
	assert.Equal(t, len(s.Cache.PendingCacheSaves()), 1)
	s.Cache.EndJob(t)

	assert.NilError(t, os.RemoveAll(modCacheDir))

	out, err = run(t, env, "mod-download", "--checksum", "checksum-a")
	assert.NilError(t, err, "second run failed: %s", out)

	got, err := os.ReadFile(filepath.Join(modCacheDir, "marker")) //nolint:gosec
	assert.NilError(t, err)
	assert.Equal(t, string(got), "hit", "expected go mod cache marker restored from a prior save")

	log, err := os.ReadFile(logFile) //nolint:gosec
	assert.NilError(t, err)
	assert.Equal(t, strings.Count(string(log), "mod download\n"), 2, "expected `go mod download` to run on both invocations, got log:\n%s", log)
}

// TestCLI_ModDownload is a table test over mod-download's simpler CLI
// contract: flag validation and error propagation.
func TestCLI_ModDownload(t *testing.T) {
	tests := []struct {
		name string
		// setup prepares any fixtures/env needed for the case and returns
		// the env overrides and full argv (including the "mod-download"
		// subcommand).
		setup    func(t *testing.T) (env map[string]string, args []string)
		wantErr  bool
		contains []string
	}{
		{
			// --cache=false must skip both the cache restore/save calls and
			// the `go env` lookup they'd otherwise require to find GOMODCACHE.
			name: "NoCache_SkipsGoEnvLookup",
			setup: func(t *testing.T) (map[string]string, []string) {
				logFile := filepath.Join(t.TempDir(), "go-stub.log")
				env := map[string]string{"PATH": stubGoModDownloadPATH(t, logFile, t.TempDir(), 0)}
				t.Cleanup(func() {
					log, err := os.ReadFile(logFile) //nolint:gosec
					assert.NilError(t, err)
					assert.Assert(t, !strings.Contains(string(log), "env -json"), "expected no `go env` call with --cache=false, got log:\n%s", log)
				})
				return env, []string{"mod-download", "--cache=false"}
			},
		},
		{
			name: "Path_OverridesGoEnvLookup",
			setup: func(t *testing.T) (map[string]string, []string) {
				logFile := filepath.Join(t.TempDir(), "go-stub.log")
				env := map[string]string{"PATH": stubGoModDownloadPATH(t, logFile, t.TempDir(), 0)}
				t.Cleanup(func() {
					log, err := os.ReadFile(logFile) //nolint:gosec
					assert.NilError(t, err)
					assert.Assert(t, !strings.Contains(string(log), "env -json"), "expected --path to skip the `go env` lookup, got log:\n%s", log)
				})
				return env, []string{"mod-download", "--path", t.TempDir()}
			},
		},
		{
			name: "ModPath_EmptyString_ReturnsError",
			setup: func(t *testing.T) (map[string]string, []string) {
				env := map[string]string{"PATH": stubGoModDownloadPATH(t, filepath.Join(t.TempDir(), "go-stub.log"), t.TempDir(), 0)}
				return env, []string{"mod-download", "--mod-path", ""}
			},
			wantErr:  true,
			contains: []string{"--mod-path"},
		},
		{
			// A `go mod download` failure (e.g. a checksum mismatch or an
			// unreachable proxy) must surface as a non-zero exit with the
			// underlying error visible, not be swallowed.
			name: "GoModDownloadFails_ReturnsError",
			setup: func(t *testing.T) (map[string]string, []string) {
				env := map[string]string{"PATH": stubGoModDownloadPATH(t, filepath.Join(t.TempDir(), "go-stub.log"), t.TempDir(), 1)}
				return env, []string{"mod-download", "--cache=false"}
			},
			wantErr:  true,
			contains: []string{"go mod download"},
		},
		{
			name: "NoGoOnPath_ReturnsError",
			setup: func(t *testing.T) (map[string]string, []string) {
				return map[string]string{"PATH": t.TempDir()}, []string{"mod-download", "--cache=false"}
			},
			wantErr: true,
		},
		{
			name: "UnknownFlag",
			setup: func(t *testing.T) (map[string]string, []string) {
				return nil, []string{"mod-download", "--no-such-flag"}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, run := sandbox(t)

			env, args := tt.setup(t)
			out, err := run(t, env, args...)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error, got: %s", out)
			} else {
				assert.NilError(t, err, "got: %s", out)
			}
			for _, s := range tt.contains {
				assert.Assert(t, strings.Contains(out, s), "expected %q in output, got: %s", s, out)
			}
		})
	}
}
