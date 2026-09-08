package clitest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/setup-go/internal/testing/fakegodev"
)

// TestCLI_RegistersJobCacheSaves verifies that setup-go restores the module and
// build caches and registers their saves for the end of the job, rather than
// saving during the command.
func TestCLI_RegistersJobCacheSaves(t *testing.T) {
	s, run := sandbox(t)
	ts := fakegodev.New(t, []fakegodev.Release{fakegodev.PlatformRelease("go1.22.3", "")}, nil)

	goCache := t.TempDir()
	modCache := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(goCache, "marker"), []byte("build"), 0600))
	assert.NilError(t, os.WriteFile(filepath.Join(modCache, "marker"), []byte("mod"), 0600))

	env := map[string]string{
		// --detect-only resolves GOROOT as the parent of the go binary's dir,
		// so the stub has to sit in a bin/ subdirectory.
		"PATH":     stubGoCachesBinDir(t, goCache, modCache),
		"GOPATH":   "",
		"BASH_ENV": filepath.Join(t.TempDir(), "bash_env"),
	}
	args := []string{"--detect-only", "--api-url", ts.URL, "--checksum", "checksum-a"}

	out, err := run(t, env, args...)
	assert.NilError(t, err, "first run failed:\n%s", out)

	// Both registered, neither saved yet.
	assert.Equal(t, len(s.Cache.PendingCacheSaves()), 2)
	s.Cache.EndJob(t)

	assert.NilError(t, os.RemoveAll(goCache))
	assert.NilError(t, os.RemoveAll(modCache))

	out, err = run(t, env, args...)
	assert.NilError(t, err, "second run failed:\n%s", out)

	got, err := os.ReadFile(filepath.Join(goCache, "marker")) //nolint:gosec
	assert.NilError(t, err, "expected the build cache to be restored")
	assert.Equal(t, string(got), "build")

	got, err = os.ReadFile(filepath.Join(modCache, "marker")) //nolint:gosec
	assert.NilError(t, err, "expected the module cache to be restored")
	assert.Equal(t, string(got), "mod")
}

// TestCLI_CacheDisabled_RegistersNothing verifies --cache=false is a master
// switch: no cache is touched even though the per-cache flags default on.
func TestCLI_CacheDisabled_RegistersNothing(t *testing.T) {
	s, run := sandbox(t)
	ts := fakegodev.New(t, []fakegodev.Release{fakegodev.PlatformRelease("go1.22.3", "")}, nil)

	env := map[string]string{
		"PATH":     stubGoCachesBinDir(t, t.TempDir(), t.TempDir()),
		"GOPATH":   "",
		"BASH_ENV": filepath.Join(t.TempDir(), "bash_env"),
	}

	out, err := run(t, env, "--detect-only", "--api-url", ts.URL, "--cache=false")
	assert.NilError(t, err, "run failed:\n%s", out)

	assert.Equal(t, len(s.Cache.PendingCacheSaves()), 0)
	assert.Equal(t, len(s.Cache.SavedKeys()), 0)
}

// stubGoCachesBinDir writes a `go` stub in a bin/ subdirectory that answers
// `go env` with the given cache dirs and a `go version`, so --detect-only can
// resolve a GOROOT.
func stubGoCachesBinDir(t *testing.T, goCache, modCache string) string {
	t.Helper()
	binDir := filepath.Join(t.TempDir(), "bin")
	assert.NilError(t, os.MkdirAll(binDir, 0750))
	script := fmt.Sprintf(`#!/bin/sh
case "$1 $2" in
  "env -json") echo '{"GOCACHE":%q,"GOMODCACHE":%q,"GOPATH":"/fake/go"}' ;;
  *)
    case "$1" in
      version) echo 'go version go1.22.3' ;;
      env) shift; for v in "$@"; do case "$v" in GOPATH) echo /fake/go ;; GOROOT) echo "$(cd "${0%%/*}/.." && pwd)" ;; *) echo "" ;; esac; done ;;
      *) exit 1 ;;
    esac ;;
esac
`, goCache, modCache)
	assert.NilError(t, os.WriteFile(filepath.Join(binDir, "go"), []byte(script), 0750)) //nolint:gosec
	return binDir
}
