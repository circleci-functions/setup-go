//go:build acceptance

package setupgo_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk/sdktest"
	"github.com/circleci-functions/toolkit/testing/runner"
)

// TestAcceptance_LinksOntoTheRealPath is the one test that lets setup-go choose
// where the toolchain goes, rather than telling it with --bin-dir. That choice
// depends on the executor, and a sandboxed suite never reaches it — which is
// how a link into ~/.pyenv/shims got as far as CI.
//
// It changes the machine it runs on, up to taking over the `go` already there,
// so it runs only where that is disposable: a CircleCI job, or a container the
// caller vouches for with $SETUP_GO_DISPOSABLE (see `task smoke-local`).
func TestAcceptance_LinksOntoTheRealPath(t *testing.T) {
	if os.Getenv("SETUP_GO_DISPOSABLE") == "" {
		sdktest.SkipUnlessInJob(t)
	}

	s := sdktest.NewSandbox(t, sdktest.WithLiveCache())
	version := acceptanceVersion()

	// Only $BASH_ENV is redirected, since CI writes to the job's own. $HOME
	// stays real: the choice of directory is made from it.
	bashEnv := filepath.Join(t.TempDir(), "bash_env")
	r := runner.New(t, binaryPath, runner.WithUnsafeAdditionalEnvVars(map[string]string{
		"HOME":        homeDir(t),
		"USERPROFILE": homeDir(t),
		"BASH_ENV":    bashEnv,
		"GOROOT":      "",
	}))

	_, err := r.Run(
		"--version", version,
		"--install-dir", s.InstallDir,
		"--cache-nonce", s.CacheKeyNonce,
		"--checksum", s.CacheKeyNonce,
	)
	out := r.StdoutString() + r.StderrString()
	assert.NilError(t, err, "got: %s", out)

	// The install dir goes away with the sandbox, so the links must too, or a
	// dangling `go` follows this test into the rest of the job.
	removeLinks(t, reportedLinkDir(t, out))

	t.Run("ResolvesByNameInALaterStep", func(t *testing.T) {
		got, err := sdktest.LaterStep(t, "go", "version")
		assert.NilError(t, err, "a later step could not resolve go: %s", got)
		assertReportsVersion(t, got, version)
	})

	// The shell $BASH_ENV never reached.
	if runtime.GOOS == "windows" {
		t.Run("ResolvesInPowerShellToo", func(t *testing.T) {
			got, err := sdktest.LaterStep(t, "powershell.exe", "-Command", "go version")
			assert.NilError(t, err, "PowerShell could not resolve go: %s", got)
			assertReportsVersion(t, got, version)
		})
	}

	t.Run("NothingIsExportedToAShell", func(t *testing.T) {
		content, err := os.ReadFile(bashEnv) //nolint:gosec // a path this test chose
		if os.IsNotExist(err) {
			return
		}
		assert.NilError(t, err)
		assert.Assert(t, !strings.Contains(string(content), "export PATH="),
			"the toolchain goes on PATH by being linked there, so nothing should be exported: %s", content)
	})

	t.Run("NotIntoADirectoryOwnedByAnotherTool", func(t *testing.T) {
		dir := reportedLinkDir(t, out)
		for _, part := range strings.Split(filepath.ToSlash(dir), "/") {
			assert.Assert(t, part != "shims",
				"a version manager rewrites its shims directory, taking the link with it: %s", dir)
		}
	})
}

// removeLinks takes the toolchain back off PATH at the end of the test.
func removeLinks(t *testing.T, dir string) {
	t.Helper()
	t.Cleanup(func() {
		for _, name := range []string{"go", "gofmt"} {
			for _, ext := range []string{"", ".exe", ".bat"} {
				_ = os.Remove(filepath.Join(dir, name+ext))
			}
		}
	})
}

// homeDir returns the real home directory, which a job step has and the runner
// does not pass through.
func homeDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	assert.NilError(t, err)
	return home
}

func assertReportsVersion(t *testing.T, out, want string) {
	t.Helper()
	if want == "stable" || want == "latest" {
		return
	}
	assert.Assert(t, strings.Contains(out, "go"+want),
		"expected a later step to resolve go%s, got: %s", want, out)
}

// reportedLinkDir reads back the directory setup-go says it chose.
func reportedLinkDir(t *testing.T, out string) string {
	t.Helper()
	const marker = "resolve from "
	for line := range strings.SplitSeq(out, "\n") {
		if _, dir, found := strings.Cut(line, marker); found {
			return strings.TrimSpace(dir)
		}
	}
	t.Fatalf("setup-go did not report where it linked the toolchain:\n%s", out)
	return ""
}
