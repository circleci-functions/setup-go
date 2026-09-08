//go:build acceptance

package setupgo_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
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
	version := acceptanceVersion()

	installArgs := []string{
		"--version", version,
		"--install-dir", s.InstallDir,
		"--bin-dir", s.BinDir,
		// Isolates this run's caches, so the cold install below cannot hit an
		// entry left by an earlier run.
		"--cache-nonce", s.CacheKeyNonce,
		"--checksum", s.CacheKeyNonce,
	}

	if !t.Run("InstallGo_Cold", func(t *testing.T) {
		out, err := run(t, nil, installArgs...)
		assert.NilError(t, err, "got: %s", out)
		assert.Assert(t, !strings.Contains(out, "restored from cache"),
			"a per-run nonce must miss the cache, so this step downloads for real; got: %s", out)

		assertLinkedGoVersion(t, s.BinDir, s.InstallDir, version)
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
		assertLinkedGoVersion(t, s.BinDir, s.InstallDir, version)
	}) {
		t.Fatal("cache-hit install failed; aborting remaining steps")
	}

	t.Run("NothingIsExportedToAShell", func(t *testing.T) {
		_, err := os.Stat(s.BashEnv)
		assert.Assert(t, os.IsNotExist(err),
			"the toolchain goes on PATH by being linked there, so $BASH_ENV must be untouched; got %v", err)
	})

	t.Run("ResolvesByNameInALaterStep", func(t *testing.T) {
		assertResolvesFromPath(t, s.BinDir, version)
	})
}

// assertLinkedGoVersion checks the link points at the toolchain setup-go
// installed, not at a Go the executor shipped. --bin-dir keeps it in the
// sandbox, so discovery is left to TestAcceptance_LinksOntoTheRealPath.
func assertLinkedGoVersion(t *testing.T, binDir, installDir, want string) {
	t.Helper()

	linked := filepath.Join(binDir, "go")
	target, err := os.Readlink(linked)
	assert.NilError(t, err, "expected setup-go to link go into %s", binDir)
	assert.Assert(t, strings.HasPrefix(target, installDir),
		"expected the link to point inside %s, got %q", installDir, target)

	assertGoVersion(t, filepath.Dir(linked), want)
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

// assertResolvesFromPath runs `go version` with binDir on PATH and nothing
// sourced, the local proxy for what a later step gets.
func assertResolvesFromPath(t *testing.T, binDir, want string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "sh", "-c", "go version")
	cmd.Env = append(os.Environ(), "PATH="+binDir, "GOROOT=")

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, "resolving go from PATH alone failed: %s", out)
	if want != "stable" && want != "latest" {
		assert.Assert(t, strings.Contains(string(out), "go"+want),
			"expected a later step to resolve go%s, got: %s", want, out)
	}
}
