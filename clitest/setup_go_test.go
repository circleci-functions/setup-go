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

	"github.com/circleci-functions/setup-go/internal/testing/fakegodev"
)

// TestCLI_DetectOnly_ExportsEnv relies on a real `go` binary being present on
// PATH (needed by --detect-only to run `go env`). This is satisfied in CI
// (cimg/go image) and on any standard dev machine.
func TestCLI_DetectOnly_ExportsEnv(t *testing.T) {
	_, run := sandbox(t)
	ts := fakegodev.New(t, []fakegodev.Release{fakegodev.PlatformRelease("go1.22.3", "")}, nil)
	dir := t.TempDir()
	bashEnvFile := filepath.Join(dir, "bash_env")

	out, err := run(t, map[string]string{"GOPATH": "", "BASH_ENV": bashEnvFile},
		"--detect-only", "--api-url", ts.URL)
	assert.NilError(t, err, "got: %s", out)

	content, readErr := os.ReadFile(bashEnvFile)
	assert.NilError(t, readErr)
	assert.Assert(t, strings.Contains(string(content), "export PATH="), "BASH_ENV missing PATH: %s", content)
	assert.Assert(t, !strings.Contains(string(content), "GOPATH="), "BASH_ENV should not export GOPATH: %s", content)
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
	const version = "go1.22.3"

	goBinScript := fmt.Sprintf(
		"#!/bin/sh\ncase \"$1\" in\n"+
			"  version) echo 'go version %s %s/%s' ;;\n"+
			"  env) shift; if [ \"$1\" = -json ]; then echo '{\"GOPATH\":\"/tmp/testgopath\"}'; else for v in \"$@\"; do case \"$v\" in GOPATH) echo /tmp/testgopath ;; *) echo \"\" ;; esac; done; fi ;;\n"+
			"esac\n",
		version, runtime.GOOS, runtime.GOARCH)

	tarball := fakegodev.BuildTarball(t, version, goBinScript)
	tarSHA := sha256Hex(tarball)
	tarFilename := fmt.Sprintf("%s.%s-%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	tarURLPath := "/" + tarFilename

	var downloadCount atomic.Int64
	godevSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == tarURLPath {
			downloadCount.Add(1)
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(tarball)
			return
		}
		rel := fakegodev.PlatformRelease(version, tarSHA)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]fakegodev.Release{rel})
	}))
	t.Cleanup(godevSrv.Close)

	s, run := sandbox(t)

	args := []string{
		"--version", "1.22.3",
		"--install-dir", s.InstallDir,
		"--api-url", godevSrv.URL + "/",
		"--bin-dir", s.BinDir,
	}

	env := map[string]string{"GOPATH": ""}

	out, err := run(t, env, args...)
	assert.NilError(t, err, "run 1 failed:\n%s", out)
	assert.Equal(t, int(downloadCount.Load()), 1, "run 1 must download the tarball exactly once")

	// Simulate a fresh executor: clear the install dir so the second run
	// must rely on the cache rather than a locally present toolchain.
	assert.NilError(t, os.RemoveAll(s.InstallDir))

	out, err = run(t, env, args...)
	assert.NilError(t, err, "run 2 failed:\n%s", out)
	assert.Equal(t, int(downloadCount.Load()), 1, "run 2 must not re-download the tarball")
}

// TestCLI_VersionFile_ReadsGoMod verifies that --version-file reads the go
// directive from a go.mod file and uses it to select the toolchain version.
func TestCLI_VersionFile_ReadsGoMod(t *testing.T) {
	_, run := sandbox(t)
	ts := fakegodev.New(t, []fakegodev.Release{
		fakegodev.PlatformRelease("go1.22.5", "aaa"),
		fakegodev.PlatformRelease("go1.22.3", "bbb"),
	}, nil)

	gomod := filepath.Join(t.TempDir(), "go.mod")
	assert.NilError(t, os.WriteFile(gomod, []byte("module example\n\ngo 1.22\n"), 0600))

	bashEnvFile := filepath.Join(t.TempDir(), "bash_env")
	out, err := run(t, map[string]string{"GOPATH": "", "BASH_ENV": bashEnvFile},
		"--detect-only", "--version-file", gomod, "--api-url", ts.URL)
	assert.NilError(t, err, "got: %s", out)
	assert.Assert(t, strings.Contains(out, "go1.22.5"), "expected resolved version in output, got: %s", out)
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
