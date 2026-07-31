package setupgo_test

import (
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

// TestCLI_Cache_NoTarget_ReturnsError verifies that omitting --golangci-lint
// (the only cache target implemented so far) is an error rather than a
// silent no-op.
func TestCLI_Cache_NoTarget_ReturnsError(t *testing.T) {
	_, err := run(t, nil, "cache", "load")
	assert.ErrorContains(t, err, "exit status 1")
}
