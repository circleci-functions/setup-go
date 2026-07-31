package jobenv_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/setup-go/jobenv"
)

// writeStubGo writes a script that answers `go env` queries predictably. On
// Windows this must be a .bat file: exec.Command requires a recognized
// PATHEXT extension to run an absolute path, and Windows can't execute a
// shebang script directly.
func writeStubGo(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return writeStubGoWindows(t, dir)
	}
	stub := `#!/bin/sh
case "$1" in
  env)
    shift
    for v in "$@"; do
      case "$v" in
        GOPATH)     echo "/test/go" ;;
        *)          echo "" ;;
      esac
    done
    ;;
  *) exit 1 ;;
esac
`
	path := filepath.Join(dir, "go")
	if err := os.WriteFile(path, []byte(stub), 0750); err != nil {
		t.Fatalf("write stub go: %v", err)
	}
	return path
}

func writeStubGoWindows(t *testing.T, dir string) string {
	t.Helper()
	stub := "@echo off\r\n" +
		"if \"%1\"==\"env\" (\r\n" +
		"  shift\r\n" +
		"  :loop\r\n" +
		"  if \"%1\"==\"GOPATH\" echo /test/go\r\n" +
		"  shift\r\n" +
		"  if not \"%1\"==\"\" goto loop\r\n" +
		") else (\r\n" +
		"  exit /b 1\r\n" +
		")\r\n"
	path := filepath.Join(dir, "go.bat")
	if err := os.WriteFile(path, []byte(stub), 0750); err != nil {
		t.Fatalf("write stub go.bat: %v", err)
	}
	return path
}

func TestDetectGoPath(t *testing.T) {
	dir := t.TempDir()
	goBin := writeStubGo(t, dir)

	goPath, err := jobenv.DetectGoPath(context.Background(), goBin)
	assert.NilError(t, err)
	assert.Equal(t, goPath, "/test/go")
}

func TestWriteEnv_BashEnvFile(t *testing.T) {
	bashEnvPath := filepath.Join(t.TempDir(), "env.sh")
	t.Setenv("BASH_ENV", bashEnvPath)

	assert.NilError(t, jobenv.WriteEnv("/test/go", "/usr/local/go1.22.3/bin"))

	content, err := os.ReadFile(bashEnvPath)
	assert.NilError(t, err)

	got := string(content)
	assert.Assert(t, !strings.Contains(got, "GOPATH"), "BASH_ENV should not export GOPATH:\n%s", got)
	assert.Assert(t, !strings.Contains(got, "GOMODCACHE"), "BASH_ENV should not export GOMODCACHE:\n%s", got)
	assert.Assert(t, !strings.Contains(got, "GOROOT"), "BASH_ENV should not export GOROOT:\n%s", got)
	assert.Assert(t, !strings.Contains(got, "GOCACHE"), "BASH_ENV should not export GOCACHE:\n%s", got)
	assert.Assert(t, strings.Contains(got, "export PATH=/usr/local/go1.22.3/bin:/test/go/bin:$PATH"),
		"BASH_ENV missing expected PATH export:\n%s", got)
}

func TestWriteEnv_NoBashEnv(t *testing.T) {
	t.Setenv("BASH_ENV", "")

	err := jobenv.WriteEnv("/go", "/usr/local/go/bin")
	assert.ErrorContains(t, err, "BASH_ENV must be set")
}

func TestWriteEnv_Append(t *testing.T) {
	bashEnvPath := filepath.Join(t.TempDir(), "env.sh")
	if err := os.WriteFile(bashEnvPath, []byte("# existing\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BASH_ENV", bashEnvPath)

	assert.NilError(t, jobenv.WriteEnv("/go", "/go-root/bin"))

	content, err := os.ReadFile(bashEnvPath)
	assert.NilError(t, err)
	assert.Assert(t, strings.HasPrefix(string(content), "# existing\n"), "WriteEnv must append, not overwrite; got:\n%s", string(content))
	assert.Assert(t, strings.Contains(string(content), "export PATH=/go-root/bin:/go/bin:$PATH"))
}

func TestWriteEnv_PathExpansion(t *testing.T) {
	bashEnvPath := filepath.Join(t.TempDir(), "env.sh")
	t.Setenv("BASH_ENV", bashEnvPath)

	assert.NilError(t, jobenv.WriteEnv("/test/go", "/usr/local/go/bin"))

	cmd := exec.Command("sh", "-c", fmt.Sprintf(". %s && printf '%%s' \"$PATH\"", bashEnvPath))
	out, err := cmd.Output()
	assert.NilError(t, err)

	resultPath := string(out)
	assert.Assert(t, strings.Contains(resultPath, "/test/go/bin"),
		"goPath's bin dir must appear in PATH after sourcing; got PATH=%s", resultPath)
	assert.Assert(t, strings.Contains(resultPath, "/usr/local/go/bin"),
		"goRootBin must appear in PATH after sourcing; got PATH=%s", resultPath)
}
