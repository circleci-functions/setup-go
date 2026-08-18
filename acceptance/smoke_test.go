//go:build acceptance

package setupgo_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// acceptanceVersion returns the Go version to install: SETUP_GO_ACCEPTANCE_VERSION
// is set in CI from the pipeline's go-version parameter; "stable" (setup-go's
// own default) is used for local/dev runs where it's unset.
func acceptanceVersion() string {
	if v := os.Getenv("SETUP_GO_ACCEPTANCE_VERSION"); v != "" {
		return v
	}
	return "stable"
}

// fixtureDir resolves acceptance/test-project to an absolute path. The
// compiled setup-go binary is run with its own temp directory as its working
// directory (see runner.Runner.cmdDir), so a relative --project-path would
// resolve against the wrong directory.
func fixtureDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("test-project")
	assert.NilError(t, err)
	return abs
}

// TestAcceptance_Smoke runs the same end-to-end sequence the CircleCI
// smoke-test job used to run as bash steps, against a real CircleCI
// task-agent and a real go.dev download - no stubbing. Steps run in order;
// later steps depend on earlier ones succeeding, mirroring how a CircleCI job
// aborts once a step fails.
func TestAcceptance_Smoke(t *testing.T) {
	const checksum = "acceptance-suite"
	version := acceptanceVersion()

	home, err := os.UserHomeDir()
	assert.NilError(t, err)

	bashEnvFile := filepath.Join(t.TempDir(), "bash_env")
	installEnv := map[string]string{"BASH_ENV": bashEnvFile}

	if !t.Run("InstallGo_Cold", func(t *testing.T) {
		out, err := run(t, installEnv, "install", "--version", version, "--go-cache-checksum", checksum)
		assert.NilError(t, err, "got: %s", out)
	}) {
		t.Fatal("cold install failed; aborting remaining steps")
	}

	assert.NilError(t, os.RemoveAll(filepath.Join(home, ".cache", "setup-go")))

	var testEnv map[string]string
	if !t.Run("InstallGo_CacheHit", func(t *testing.T) {
		out, err := run(t, installEnv, "install", "--version", version, "--go-cache-checksum", checksum)
		assert.NilError(t, err, "got: %s", out)
		assert.Assert(t, strings.Contains(out, "restored from cache"), "expected a cache hit, got: %s", out)

		pathPrefix := parseBashEnvPATH(t, bashEnvFile)
		testEnv = map[string]string{"PATH": pathPrefix + string(os.PathListSeparator) + os.Getenv("PATH")}
	}) {
		t.Fatal("cache-hit install failed; aborting remaining steps")
	}

	t.Run("RunTestSubcommand", func(t *testing.T) {
		out, err := run(t, testEnv, "test", "--project-path", fixtureDir(t))
		assert.NilError(t, err, "got: %s", out)
	})

	t.Run("GolangCILintCache_RoundTrip", func(t *testing.T) {
		lintDir := filepath.Join(home, ".cache", "golangci-lint")
		assert.NilError(t, os.MkdirAll(lintDir, 0750))
		assert.NilError(t, os.WriteFile(filepath.Join(lintDir, "marker"), []byte("hit"), 0600))

		out, err := run(t, nil, "cache", "save", "--golangci-lint", "--checksum", checksum)
		assert.NilError(t, err, "save failed: %s", out)

		assert.NilError(t, os.RemoveAll(lintDir))

		out, err = run(t, nil, "cache", "load", "--golangci-lint", "--checksum", checksum)
		assert.NilError(t, err, "load failed: %s", out)

		entries, err := os.ReadDir(lintDir)
		assert.NilError(t, err)
		assert.Assert(t, len(entries) > 0, "expected non-empty golangci-lint cache dir after reload")
	})

	t.Run("BuildCache_RoundTrip", func(t *testing.T) {
		cacheDir := t.TempDir()
		assert.NilError(t, os.WriteFile(filepath.Join(cacheDir, "marker"), []byte("hit"), 0600))

		out, err := run(t, nil, "cache", "save", "--build", "--build-path", cacheDir, "--checksum", checksum)
		assert.NilError(t, err, "save failed: %s", out)

		assert.NilError(t, os.RemoveAll(cacheDir))

		out, err = run(t, nil, "cache", "load", "--build", "--checksum", checksum)
		assert.NilError(t, err, "load failed: %s", out)

		entries, err := os.ReadDir(cacheDir)
		assert.NilError(t, err)
		assert.Assert(t, len(entries) > 0, "expected non-empty build cache dir after reload")
	})
}
