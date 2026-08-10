package setupgo_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk/sdktest"
)

// TestCLI_Cache_GolangCILint_RoundTrips verifies that `cache save
// --golangci-lint` saves golangci-lint's default cache directory
// (~/.cache/golangci-lint) and a subsequent `cache load --golangci-lint`
// restores it, round-tripping a marker file across two invocations against
// the same fake CircleCI plugin server.
func TestCLI_Cache_GolangCILint_RoundTrips(t *testing.T) {
	srv := sdktest.NewServer(t)
	srv.SetupEnv(t)

	home := t.TempDir()
	cacheDir := filepath.Join(home, ".cache", "golangci-lint")
	env := map[string]string{"HOME": home}

	assert.NilError(t, os.MkdirAll(cacheDir, 0750))
	assert.NilError(t, os.WriteFile(filepath.Join(cacheDir, "marker"), []byte("hit"), 0600))

	out, err := run(t, env, "cache", "save", "--golangci-lint", "--checksum", "checksum-a")
	assert.NilError(t, err, "save failed:\n%s", out)

	assert.NilError(t, os.RemoveAll(cacheDir))

	out, err = run(t, env, "cache", "load", "--golangci-lint", "--checksum", "checksum-a")
	assert.NilError(t, err, "load failed:\n%s", out)

	got, err := os.ReadFile(filepath.Join(cacheDir, "marker")) //nolint:gosec
	assert.NilError(t, err)
	assert.Equal(t, string(got), "hit", "expected lint cache marker restored from a prior save")
}

// stubGoEnvBinDir returns a PATH directory containing a `go` stub that answers
// `go env -json` with a JSON object reporting gocache as GOCACHE (and nothing
// else). `cache save --build` shells out to this to discover the build cache
// directory; `cache load --build` needs no `go` at all.
func stubGoEnvBinDir(t *testing.T, gocache string) string {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
case "$1 $2" in
  "env -json") echo '{"GOCACHE":%q}' ;;
  *) exit 1 ;;
esac
`, gocache)
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0750)) //nolint:gosec
	return dir
}

// TestCLI_Cache_Build_RoundTrips verifies the build-cache CLI end to end:
// `cache save --build` saves the directory reported by `go env GOCACHE` and a
// subsequent `cache load --build` under the same checksum and job restores it,
// round-tripping a marker file. This exercises the wiring the unit tests can't
// — flag parsing, the single go-env lookup on save, and selecting GOCACHE
// (not GOMODCACHE).
func TestCLI_Cache_Build_RoundTrips(t *testing.T) {
	srv := sdktest.NewServer(t)
	srv.SetupEnv(t)

	goCache := filepath.Join(t.TempDir(), "go-build")
	assert.NilError(t, os.MkdirAll(goCache, 0750))
	assert.NilError(t, os.WriteFile(filepath.Join(goCache, "marker"), []byte("hit"), 0600))

	// CIRCLE_JOB pins the cache key's job scope; the same value on both runs
	// makes the exact (tier 1) restore key match the save key.
	env := map[string]string{"PATH": stubGoEnvBinDir(t, goCache), "CIRCLE_JOB": "test"}

	out, err := run(t, env, "cache", "save", "--build", "--checksum", "sum-a")
	assert.NilError(t, err, "save failed:\n%s", out)

	assert.NilError(t, os.RemoveAll(goCache))

	out, err = run(t, env, "cache", "load", "--build", "--checksum", "sum-a")
	assert.NilError(t, err, "load failed:\n%s", out)

	got, err := os.ReadFile(filepath.Join(goCache, "marker")) //nolint:gosec
	assert.NilError(t, err)
	assert.Equal(t, string(got), "hit", "expected build cache marker restored from a prior save")
}

// TestCLI_Cache_NoTarget_ReturnsError verifies that omitting every cache target
// is an error rather than a silent no-op.
func TestCLI_Cache_NoTarget_ReturnsError(t *testing.T) {
	_, err := run(t, nil, "cache", "load")
	assert.ErrorContains(t, err, "exit status 1")
}
