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

// TestCLI_Cache_Build_RoundTrips verifies that `cache save --build` saves the
// go build cache directory and a subsequent `cache load --build` restores it.
// --build-path points the save at a temp dir so the test never touches the
// executor's real GOCACHE.
func TestCLI_Cache_Build_RoundTrips(t *testing.T) {
	srv := sdktest.NewServer(t)
	srv.SetupEnv(t)

	cacheDir := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(cacheDir, "marker"), []byte("hit"), 0600))

	out, err := run(t, nil, "cache", "save", "--build", "--build-path", cacheDir, "--checksum", "checksum-a")
	assert.NilError(t, err, "save failed:\n%s", out)

	assert.NilError(t, os.RemoveAll(cacheDir))

	out, err = run(t, nil, "cache", "load", "--build", "--checksum", "checksum-a")
	assert.NilError(t, err, "load failed:\n%s", out)

	got, err := os.ReadFile(filepath.Join(cacheDir, "marker")) //nolint:gosec
	assert.NilError(t, err)
	assert.Equal(t, string(got), "hit", "expected build cache marker restored from a prior save")
}

// TestCLI_Cache_Build_SavesGoCacheNotModCache verifies that without
// --build-path, `cache save --build` saves the directory `go env` reports as
// GOCACHE — not GOMODCACHE, whose value it also has to hand.
func TestCLI_Cache_Build_SavesGoCacheNotModCache(t *testing.T) {
	srv := sdktest.NewServer(t)
	srv.SetupEnv(t)

	goCache := t.TempDir()
	modCache := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(goCache, "marker"), []byte("build"), 0600))
	assert.NilError(t, os.WriteFile(filepath.Join(modCache, "marker"), []byte("mod"), 0600))

	env := map[string]string{"PATH": stubGoEnvPATH(t, goCache, modCache)}

	out, err := run(t, env, "cache", "save", "--build", "--checksum", "checksum-a")
	assert.NilError(t, err, "save failed:\n%s", out)

	assert.NilError(t, os.RemoveAll(goCache))
	assert.NilError(t, os.RemoveAll(modCache))

	out, err = run(t, env, "cache", "load", "--build", "--checksum", "checksum-a")
	assert.NilError(t, err, "load failed:\n%s", out)

	got, err := os.ReadFile(filepath.Join(goCache, "marker")) //nolint:gosec
	assert.NilError(t, err, "expected GOCACHE to be the saved directory")
	assert.Equal(t, string(got), "build")

	_, err = os.Stat(filepath.Join(modCache, "marker"))
	assert.Assert(t, os.IsNotExist(err), "expected GOMODCACHE not to be saved by --build")
}

// stubGoEnvPATH returns a PATH directory containing a `go` stub that answers
// `go env -json` with distinct GOCACHE and GOMODCACHE values, so a test can
// tell which one the CLI picked.
func stubGoEnvPATH(t *testing.T, goCache, modCache string) string {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
case "$1 $2" in
  "env -json") echo '{"GOCACHE":%q,"GOMODCACHE":%q}' ;;
  *) exit 1 ;;
esac
`, goCache, modCache)
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0750)) //nolint:gosec
	return dir
}

// TestCLI_Cache_NoTarget_ReturnsError verifies that omitting --golangci-lint
// (the only cache target implemented so far) is an error rather than a
// silent no-op.
func TestCLI_Cache_NoTarget_ReturnsError(t *testing.T) {
	_, err := run(t, nil, "cache", "load")
	assert.ErrorContains(t, err, "exit status 1")
}
