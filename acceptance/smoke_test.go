//go:build acceptance

package setupgo_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/bashenv"
)

// acceptanceVersion returns the Go version to install: SETUP_GO_ACCEPTANCE_VERSION
// is set in CI from the pipeline's go-version parameter, "stable" locally.
func acceptanceVersion() string {
	if v := os.Getenv("SETUP_GO_ACCEPTANCE_VERSION"); v != "" {
		return v
	}
	return "stable"
}

// TestAcceptance_Smoke runs the sequence the CircleCI smoke-test job used to run
// as bash steps, against a real go.dev download and a real task-agent. Steps run
// in order and later ones depend on earlier ones, mirroring how a job aborts once
// a step fails.
func TestAcceptance_Smoke(t *testing.T) {
	s, run := sandbox(t)
	checksum := s.CacheKeyNonce
	version := acceptanceVersion()

	installArgs := []string{
		"install",
		"--version", version,
		"--install-dir", s.InstallDir,
		"--bin-dir", s.BinDir,
		"--go-cache-checksum", checksum,
	}

	var goBinDir string
	if !t.Run("InstallGo_Cold", func(t *testing.T) {
		out, err := run(t, nil, installArgs...)
		assert.NilError(t, err, "got: %s", out)
		assert.Assert(t, !strings.Contains(out, "restored from cache"),
			"a per-run checksum must miss the cache, so this step downloads for real; got: %s", out)

		goBinDir = installedGoBinDir(t, s.BashEnv, s.InstallDir)
		assertGoVersion(t, goBinDir, version)
	}) {
		t.Fatal("cold install failed; aborting remaining steps")
	}

	// Simulate a fresh executor: the toolchain is gone locally, so the next
	// install has to come back from the cache saved above.
	assert.NilError(t, os.RemoveAll(s.InstallDir))

	if !t.Run("InstallGo_CacheHit", func(t *testing.T) {
		out, err := run(t, nil, installArgs...)
		assert.NilError(t, err, "got: %s", out)
		assert.Assert(t, strings.Contains(out, "restored from cache"), "expected a cache hit, got: %s", out)
		assertGoVersion(t, installedGoBinDir(t, s.BashEnv, s.InstallDir), version)
	}) {
		t.Fatal("cache-hit install failed; aborting remaining steps")
	}

	t.Run("BashEnv_SourcesIntoALaterStep", func(t *testing.T) {
		assertBashEnvPutsGoOnPath(t, s.BashEnv, goBinDir)
	})

	// The installed toolchain, not whatever `go` the executor came with.
	goPath := map[string]string{"PATH": goBinDir + string(os.PathListSeparator) + os.Getenv("PATH")}

	t.Run("RunTestSubcommand", func(t *testing.T) {
		out, err := run(t, goPath, "test", "--project-path", fixtureDir(t))
		assert.NilError(t, err, "got: %s", out)
	})

	t.Run("GolangCILintCache_RoundTrip", func(t *testing.T) {
		lintDir := filepath.Join(s.Home, ".cache", "golangci-lint")
		assert.NilError(t, os.MkdirAll(lintDir, 0750))
		assert.NilError(t, os.WriteFile(filepath.Join(lintDir, "marker"), []byte("hit"), 0600))

		out, err := run(t, nil, "cache", "save", "--golangci-lint", "--checksum", checksum)
		assert.NilError(t, err, "save failed: %s", out)

		assert.NilError(t, os.RemoveAll(lintDir))

		out, err = run(t, nil, "cache", "load", "--golangci-lint", "--checksum", checksum)
		assert.NilError(t, err, "load failed: %s", out)

		assertMarker(t, lintDir, "hit")
	})

	t.Run("BuildCache_RoundTrip", func(t *testing.T) {
		cacheDir := t.TempDir()
		assert.NilError(t, os.WriteFile(filepath.Join(cacheDir, "marker"), []byte("hit"), 0600))

		out, err := run(t, goPath, "cache", "save", "--build", "--build-path", cacheDir, "--checksum", checksum)
		assert.NilError(t, err, "save failed: %s", out)

		assert.NilError(t, os.RemoveAll(cacheDir))

		out, err = run(t, goPath, "cache", "load", "--build", "--checksum", checksum)
		assert.NilError(t, err, "load failed: %s", out)

		assertMarker(t, cacheDir, "hit")
	})
}

// installedGoBinDir returns the bin directory setup-go prepended to PATH,
// asserting it points inside the sandbox rather than at a preinstalled Go.
func installedGoBinDir(t *testing.T, bashEnvFile, installDir string) string {
	t.Helper()
	path, err := bashenv.Path(bashEnvFile)
	assert.NilError(t, err)

	first, _, _ := strings.Cut(path, string(os.PathListSeparator))
	assert.Assert(t, strings.HasPrefix(first, installDir),
		"expected $BASH_ENV to prepend a bin dir under %s, got %q", installDir, first)
	return first
}

func assertGoVersion(t *testing.T, goBinDir, want string) {
	t.Helper()
	out, err := exec.Command(filepath.Join(goBinDir, "go"), "version").CombinedOutput() //nolint:gosec
	assert.NilError(t, err, "go version failed: %s", out)
	if want != "stable" && want != "latest" {
		assert.Assert(t, strings.Contains(string(out), "go"+want),
			"expected the installed toolchain to be go%s, got: %s", want, out)
	}
}

func assertMarker(t *testing.T, dir, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "marker")) //nolint:gosec
	assert.NilError(t, err, "expected the cache to restore %s", dir)
	assert.Equal(t, string(got), want, "expected the marker saved earlier in this run")
}

// assertBashEnvPutsGoOnPath sources $BASH_ENV in a real shell and resolves `go`
// from it, which is what a later job step does. bashenv.Path parses the file;
// this checks a shell agrees, so a quoting or ordering mistake that the parser
// tolerates still fails here.
//
// The bare-smoke CI job covers the same contract end to end, with a real job
// runner sourcing the file between two steps. This is the local proxy for it.
func assertBashEnvPutsGoOnPath(t *testing.T, bashEnvFile, wantGoBinDir string) {
	t.Helper()

	script := `set -e; . "$BASH_ENV"; command -v go`
	cmd := exec.CommandContext(t.Context(), "bash", "-c", script) //nolint:gosec
	cmd.Env = append(os.Environ(), "BASH_ENV="+bashEnvFile)

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, "sourcing $BASH_ENV and resolving go failed: %s", out)

	resolved := strings.TrimSpace(string(out))
	assert.Equal(t, filepath.Dir(resolved), wantGoBinDir,
		"expected a later step to resolve go to the toolchain just installed, got %q", resolved)
}
