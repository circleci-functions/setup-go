package clitest_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk/sdktest"

	"github.com/circleci-functions/setup-go/internal/testing/fakegodev"
)

// TestCLI_WritesNoBashEnv pins the contract this function no longer has: it
// puts the toolchain on PATH by linking it, so nothing is exported to a shell
// and later steps need no particular shell to pick it up.
//
// --detect-only, so a real `go` is not required: the stub answers the `go env`
// queries, as it does in CI (cimg/go) and on a dev machine.
func TestCLI_WritesNoBashEnv(t *testing.T) {
	_, run := sandbox(t)
	ts := fakegodev.New(t, []fakegodev.Release{fakegodev.PlatformRelease("go1.22.3", "")}, nil)
	binDir := stubGoOnlyBinDir(t)
	bashEnvFile := filepath.Join(t.TempDir(), "bash_env")

	out, err := run(t, map[string]string{"PATH": binDir, "GOPATH": "", "BASH_ENV": bashEnvFile},
		"--detect-only", "--api-url", ts.URL)
	assert.NilError(t, err, "got: %s", out)

	_, statErr := os.Stat(bashEnvFile)
	assert.Assert(t, os.IsNotExist(statErr), "expected $BASH_ENV to be left untouched, got %v", statErr)

	// The toolchain --detect-only found is already the one on PATH, so it is
	// reported as-is rather than linked over itself.
	assert.Assert(t, strings.Contains(out, "resolve from "+binDir), "got: %s", out)

	fi, err := os.Lstat(filepath.Join(binDir, "go"))
	assert.NilError(t, err)
	assert.Assert(t, fi.Mode()&os.ModeSymlink == 0, "the go already on PATH must be left in place")
}

func TestCLI_UnknownFlag(t *testing.T) {
	_, run := sandbox(t)
	_, err := run(t, nil, "--no-such-flag")
	assert.Assert(t, err != nil, "expected non-zero exit for unknown flag")
}

// TestCLI_ToolchainCacheRoundTrip verifies the full cache round-trip:
// the first invocation downloads the toolchain and saves it to the cache;
// the second invocation (on a clean install dir) restores from cache without
// re-downloading.
func TestCLI_ToolchainCacheRoundTrip(t *testing.T) {
	godev := newFakeGoDev(t, "go1.22.3")
	s, run := sandbox(t)
	args := installArgs(s, godev, "--version", "1.22.3")
	env := map[string]string{"GOPATH": ""}

	out, err := run(t, env, args...)
	assert.NilError(t, err, "run 1 failed:\n%s", out)
	assert.Equal(t, int(godev.downloads.Load()), 1, "run 1 must download the tarball exactly once")

	// Simulate a fresh executor: clear the install dir so the second run
	// must rely on the cache rather than a locally present toolchain.
	assert.NilError(t, os.RemoveAll(s.InstallDir))

	out, err = run(t, env, args...)
	assert.NilError(t, err, "run 2 failed:\n%s", out)
	assert.Equal(t, int(godev.downloads.Load()), 1, "run 2 must not re-download the tarball")
}

// TestCLI_DetectOnly_NeedsNoGoDev pins that --detect-only, which installs
// nothing, does not depend on go.dev either.
func TestCLI_DetectOnly_NeedsNoGoDev(t *testing.T) {
	godev := newFakeGoDev(t)
	godev.down.Store(true)
	_, run := sandbox(t)
	binDir := stubGoOnlyBinDir(t)

	out, err := run(t, map[string]string{"PATH": binDir, "GOPATH": ""},
		"--detect-only", "--api-url", godev.URL)
	assert.NilError(t, err, "got: %s", out)
	assert.Equal(t, godev.releaseRequests.Load(), int64(0))
}

// TestCLI_VersionFile_ReadsGoMod verifies that --version-file reads the go
// directive from a go.mod file and uses it to select the toolchain version.
func TestCLI_VersionFile_ReadsGoMod(t *testing.T) {
	godev := newFakeGoDev(t, "go1.22.5", "go1.22.3")
	s, run := sandbox(t)

	gomod := filepath.Join(t.TempDir(), "go.mod")
	assert.NilError(t, os.WriteFile(gomod, []byte("module example\n\ngo 1.22\n"), 0600))

	out, err := run(t, map[string]string{"GOPATH": ""}, installArgs(s, godev, "--version-file", gomod)...)
	assert.NilError(t, err, "got: %s", out)
	assert.Assert(t, strings.Contains(out, "go1.22.5"), "expected resolved version in output, got: %s", out)
}

// fakeGoDev is a go.dev serving a stub toolchain for each of its releases.
// Setting down fails its release list, as an outage would; tarballs still
// download, since they come from a different host in production.
type fakeGoDev struct {
	URL             string
	down            atomic.Bool
	releaseRequests atomic.Int64
	downloads       atomic.Int64
}

// newFakeGoDev serves versions, newest first as go.dev lists them.
func newFakeGoDev(t *testing.T, versions ...string) *fakeGoDev {
	t.Helper()
	f := &fakeGoDev{}
	tarballs := map[string][]byte{}
	var releases []fakegodev.Release
	for _, version := range versions {
		tarball := fakegodev.BuildTarball(t, version, stubToolchainScript(version))
		tarballs[fmt.Sprintf("/%s.%s-%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)] = tarball
		releases = append(releases, fakegodev.PlatformRelease(version, sha256Hex(tarball)))
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tarball, ok := tarballs[r.URL.Path]; ok {
			f.downloads.Add(1)
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(tarball)
			return
		}
		f.releaseRequests.Add(1)
		if f.down.Load() {
			http.Error(w, "go.dev is down", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(releases)
	}))
	t.Cleanup(srv.Close)
	f.URL = srv.URL + "/"
	return f
}

// installArgs returns the flags that keep an install inside the sandbox and
// pointed at godev, followed by extra.
func installArgs(s *sdktest.Sandbox, godev *fakeGoDev, extra ...string) []string {
	return append([]string{
		"--install-dir", s.InstallDir,
		"--api-url", godev.URL,
		"--bin-dir", s.BinDir,
	}, extra...)
}

// stubToolchainScript is a `go` that reports version and answers the `go env`
// queries setup-go makes of a toolchain it has just installed.
func stubToolchainScript(version string) string {
	return fmt.Sprintf(
		"#!/bin/sh\ncase \"$1\" in\n"+
			"  version) echo 'go version %s %s/%s' ;;\n"+
			"  env) shift; if [ \"$1\" = -json ]; then echo '{\"GOPATH\":\"/tmp/testgopath\"}'; else for v in \"$@\"; do case \"$v\" in GOPATH) echo /tmp/testgopath ;; *) echo \"\" ;; esac; done; fi ;;\n"+
			"esac\n",
		version, runtime.GOOS, runtime.GOARCH)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// stubGoOnlyBinDir writes a `go` stub (under a "bin" subdirectory, so
// --detect-only's GOROOT inference — two dirs up from the resolved binary —
// lands on a sane path) that answers `go env` queries. It contains nothing
// else, in particular no cc/gcc/clang, so PATH can be pointed at the
// returned "bin" dir to simulate an environment with no C toolchain.
func stubGoOnlyBinDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0750); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	script := `#!/bin/sh
case "$1" in
  env)
    shift
    if [ "$1" = -json ]; then
      echo '{"GOMODCACHE":"/fake/go/pkg/mod","GOCACHE":"/fake/cache/go-build","GOPATH":"/fake/go"}'
      exit 0
    fi
    for v in "$@"; do
      case "$v" in
        GOMODCACHE) echo "/fake/go/pkg/mod" ;;
        GOCACHE)    echo "/fake/cache/go-build" ;;
        GOPATH)     echo "/fake/go" ;;
        GOROOT)     echo "$(cd "${0%/*}/.." && pwd)" ;;
        *)          echo "" ;;
      esac
    done
    ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(script), 0750); err != nil {
		t.Fatalf("write stub go: %v", err)
	}
	return binDir
}

func TestCLI_CGOCheck_DetectOnly(t *testing.T) {
	_, run := sandbox(t)
	ts := fakegodev.New(t, []fakegodev.Release{fakegodev.PlatformRelease("go1.22.3", "")}, nil)
	binDir := stubGoOnlyBinDir(t)
	bashEnvFile := filepath.Join(t.TempDir(), "bash_env")

	out, err := run(t,
		map[string]string{"PATH": binDir, "CC": "", "GOPATH": "", "BASH_ENV": bashEnvFile},
		"--detect-only", "--api-url", ts.URL)
	assert.NilError(t, err, "got: %s", out)
}
