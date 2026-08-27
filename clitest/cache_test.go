package clitest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/setup-go/internal/testing/fakegodev"
)

// TestCLI_Cache_Build_SavesGoCacheNotModCache verifies that without
// --build-path, `cache save --build` saves the directory `go env` reports as
// GOCACHE — not GOMODCACHE, whose value it also has to hand.
func TestCLI_Cache_Build_SavesGoCacheNotModCache(t *testing.T) {
	_, run := sandbox(t)

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
	_, run := sandbox(t)
	_, err := run(t, nil, "cache", "load")
	assert.ErrorContains(t, err, "exit status 1")
}

// TestCLI_Install_RegistersJobCacheSaves verifies that `install` restores the
// module and build caches and registers their saves for the end of the job,
// rather than saving during the command.
func TestCLI_Install_RegistersJobCacheSaves(t *testing.T) {
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

	out, err := run(t, env, "install", "--detect-only", "--api-url", ts.URL, "--checksum", "checksum-a")
	assert.NilError(t, err, "install failed:\n%s", out)

	// Both registered, neither saved yet.
	assert.Equal(t, len(s.Cache.PendingCacheSaves()), 2)
	s.Cache.EndJob(t)

	assert.NilError(t, os.RemoveAll(goCache))
	assert.NilError(t, os.RemoveAll(modCache))

	out, err = run(t, env, "cache", "load", "--build", "--go-mod", "--checksum", "checksum-a")
	assert.NilError(t, err, "load failed:\n%s", out)

	got, err := os.ReadFile(filepath.Join(goCache, "marker")) //nolint:gosec
	assert.NilError(t, err, "expected the build cache saved by install")
	assert.Equal(t, string(got), "build")

	got, err = os.ReadFile(filepath.Join(modCache, "marker")) //nolint:gosec
	assert.NilError(t, err, "expected the module cache saved by install")
	assert.Equal(t, string(got), "mod")
}

// stubGoCachesBinDir is stubGoEnvPATH with the stub in a bin/ subdirectory and
// a `go version` answer, so `install --detect-only` can resolve a GOROOT.
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
      env) shift; for v in "$@"; do case "$v" in GOPATH) echo /fake/go ;; *) echo "" ;; esac; done ;;
      *) exit 1 ;;
    esac ;;
esac
`, goCache, modCache)
	assert.NilError(t, os.WriteFile(filepath.Join(binDir, "go"), []byte(script), 0750)) //nolint:gosec
	return binDir
}
