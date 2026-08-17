package setupgo_test

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

// TestCLI_Cache_GolangCILint_RoundTrips verifies that `cache save
// --golangci-lint` saves golangci-lint's default cache directory
// (~/.cache/golangci-lint) and a subsequent `cache load --golangci-lint`
// restores it, round-tripping a marker file across two invocations against
// the real task agent (or its local fallback).
func TestCLI_Cache_GolangCILint_RoundTrips(t *testing.T) {
	taskAgent(t)

	home := t.TempDir()
	cacheDir := filepath.Join(home, ".cache", "golangci-lint")
	key := uniqueKey(t)
	env := map[string]string{"HOME": home}

	writeMarker(t, cacheDir, "hit")

	out, err := run(t, env, "cache", "save", "--golangci-lint", "--key", key, "--checksum", "checksum-a")
	assert.NilError(t, err, "save failed:\n%s", out)
	assertNoCacheErrors(t, out)

	assert.NilError(t, os.RemoveAll(cacheDir))

	out, err = run(t, env, "cache", "load", "--golangci-lint", "--key", key, "--checksum", "checksum-a")
	assert.NilError(t, err, "load failed:\n%s", out)
	assertNoCacheErrors(t, out)

	got, err := os.ReadFile(filepath.Join(cacheDir, "marker")) //nolint:gosec
	assert.NilError(t, err, "expected lint cache marker restored from a prior save; home now:\n%s", dirTree(t, home))
	assert.Equal(t, string(got), "hit", "expected lint cache marker restored from a prior save")
}

// TestCLI_Cache_Build_RoundTrips verifies that `cache save --build` saves the
// go build cache directory and a subsequent `cache load --build` restores it.
// --build-path points the save at a temp dir so the test never touches the
// executor's real GOCACHE.
func TestCLI_Cache_Build_RoundTrips(t *testing.T) {
	taskAgent(t)

	cacheDir := t.TempDir()
	key := uniqueKey(t)
	writeMarker(t, cacheDir, "hit")

	out, err := run(t, nil, "cache", "save", "--build", "--build-path", cacheDir, "--key", key, "--checksum", "checksum-a")
	assert.NilError(t, err, "save failed:\n%s", out)
	assertNoCacheErrors(t, out)

	assert.NilError(t, os.RemoveAll(cacheDir))

	out, err = run(t, nil, "cache", "load", "--build", "--key", key, "--checksum", "checksum-a")
	assert.NilError(t, err, "load failed:\n%s", out)
	assertNoCacheErrors(t, out)

	got, err := os.ReadFile(filepath.Join(cacheDir, "marker")) //nolint:gosec
	assert.NilError(t, err, "expected build cache marker restored from a prior save")
	assert.Equal(t, string(got), "hit", "expected build cache marker restored from a prior save")
}

// TestCLI_Cache_Build_SavesGoCacheNotModCache verifies that without
// --build-path, `cache save --build` saves the directory the real `go env`
// reports as GOCACHE — not GOMODCACHE, whose value it also has to hand.
// GOCACHE/GOMODCACHE are set directly (rather than via a fake `go`), so the
// real `go env -json` that cache save/load shells out to reports them.
func TestCLI_Cache_Build_SavesGoCacheNotModCache(t *testing.T) {
	taskAgent(t)

	goCache, modCache := t.TempDir(), t.TempDir()
	writeMarker(t, goCache, "build")
	writeMarker(t, modCache, "mod")

	key := uniqueKey(t)
	env := map[string]string{"GOCACHE": goCache, "GOMODCACHE": modCache}

	out, err := run(t, env, "cache", "save", "--build", "--key", key, "--checksum", "checksum-a")
	assert.NilError(t, err, "save failed:\n%s", out)
	assertNoCacheErrors(t, out)

	assert.NilError(t, os.RemoveAll(goCache))
	assert.NilError(t, os.RemoveAll(modCache))

	out, err = run(t, env, "cache", "load", "--build", "--key", key, "--checksum", "checksum-a")
	assert.NilError(t, err, "load failed:\n%s", out)
	assertNoCacheErrors(t, out)

	got, err := os.ReadFile(filepath.Join(goCache, "marker")) //nolint:gosec
	assert.NilError(t, err, "expected GOCACHE to be the saved directory")
	assert.Equal(t, string(got), "build")

	_, err = os.Stat(filepath.Join(modCache, "marker"))
	assert.Assert(t, os.IsNotExist(err), "expected GOMODCACHE not to be saved by --build")
}

// TestCLI_Cache_GoMod_RoundTrips verifies `cache save/load --go-mod` restores
// a real GOMODCACHE directory (redirected so the test never touches the
// executor's real module cache).
func TestCLI_Cache_GoMod_RoundTrips(t *testing.T) {
	taskAgent(t)

	modCache := t.TempDir()
	key := uniqueKey(t)
	writeMarker(t, modCache, "hit")

	env := map[string]string{"GOMODCACHE": modCache}

	out, err := run(t, env, "cache", "save", "--go-mod", "--key", key, "--checksum", "checksum-a")
	assert.NilError(t, err, "save failed:\n%s", out)
	assertNoCacheErrors(t, out)

	assert.NilError(t, os.RemoveAll(modCache))

	out, err = run(t, env, "cache", "load", "--go-mod", "--key", key, "--checksum", "checksum-a")
	assert.NilError(t, err, "load failed:\n%s", out)
	assertNoCacheErrors(t, out)

	got, err := os.ReadFile(filepath.Join(modCache, "marker")) //nolint:gosec
	assert.NilError(t, err, "expected go mod cache marker restored from a prior save")
	assert.Equal(t, string(got), "hit")
}

// TestCLI_Cache_NoTarget_ReturnsError verifies that omitting a target
// (--golangci-lint, --go-mod, --build) is an error rather than a silent
// no-op, for both save and load.
func TestCLI_Cache_NoTarget_ReturnsError(t *testing.T) {
	_, err := run(t, nil, "cache", "load")
	assert.ErrorContains(t, err, "exit status 1")

	_, err = run(t, nil, "cache", "save")
	assert.ErrorContains(t, err, "exit status 1")
}
