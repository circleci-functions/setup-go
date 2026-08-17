package setupgo_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// TestCLI_Install_DetectOnly covers --detect-only's CLI contract: it relies
// on a real `go` binary being present on PATH (needed to run `go env`), and
// resolves a real Go release from go.dev even though the resolved version is
// only used when detectOnly is false — so --version is pinned to 1.22, a
// frozen EOL minor, purely to keep the resolution call small and stable.
func TestCLI_Install_DetectOnly(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T) (env map[string]string, args []string)
		want    string
		notWant string
	}{
		{
			name: "InstallSubcommand",
			setup: func(t *testing.T) (map[string]string, []string) {
				return map[string]string{"GOPATH": ""}, []string{"install", "--detect-only", "--version", "1.22"}
			},
		},
		{
			// setup-go works without an explicit "install" subcommand — i.e.
			// `setup-go --detect-only` is equivalent to `setup-go install --detect-only`.
			name: "TopLevelDefault",
			setup: func(t *testing.T) (map[string]string, []string) {
				return map[string]string{"GOPATH": ""}, []string{"--detect-only", "--version", "1.22"}
			},
		},
		{
			// goBinDir contains only go/gofmt, no cc/gcc/clang, so this proves
			// --detect-only works with nothing else on PATH.
			name: "MinimalPATH_NoCCompiler",
			setup: func(t *testing.T) (map[string]string, []string) {
				return map[string]string{"GOPATH": "", "PATH": goBinDir(t), "CC": ""},
					[]string{"install", "--detect-only", "--version", "1.22"}
			},
		},
		{
			// --version-file reads the go directive from a go.mod file and uses
			// it in place of --version. Asserts a prefix rather than an exact
			// patch version, since 1.22 resolves to whatever the newest stable
			// go1.22.x currently is.
			name: "VersionFileOverridesVersion",
			setup: func(t *testing.T) (map[string]string, []string) {
				gomod := filepath.Join(t.TempDir(), "go.mod")
				assert.NilError(t, os.WriteFile(gomod, []byte("module example\n\ngo 1.22\n"), 0600))
				return map[string]string{"GOPATH": ""},
					[]string{"install", "--detect-only", "--version", "1.26", "--version-file", gomod}
			},
			want: "go1.22.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, args := tt.setup(t)
			bashEnvFile := filepath.Join(t.TempDir(), "bash_env")
			env["BASH_ENV"] = bashEnvFile

			out, err := run(t, env, args...)
			assert.NilError(t, err, "got: %s", out)

			content, readErr := os.ReadFile(bashEnvFile) //nolint:gosec
			assert.NilError(t, readErr)
			assert.Assert(t, strings.Contains(string(content), "export PATH="), "BASH_ENV missing PATH: %s", content)
			assert.Assert(t, !strings.Contains(string(content), "GOPATH="), "BASH_ENV should not export GOPATH: %s", content)

			if tt.want != "" {
				assert.Assert(t, strings.Contains(out, tt.want), "expected %q in output, got: %s", tt.want, out)
			}
			if tt.notWant != "" {
				assert.Assert(t, !strings.Contains(out, tt.notWant), "expected %q not in output, got: %s", tt.notWant, out)
			}
		})
	}
}

// TestCLI_Install_DetectOnly_BashEnvIsSourceable asserts the contract
// task-agent actually relies on: bash sourcing $BASH_ENV puts the detected
// toolchain first on PATH. A substring assertion on the file's contents (as
// used above) can't catch a quoting or ordering bug that makes the export a
// no-op.
func TestCLI_Install_DetectOnly_BashEnvIsSourceable(t *testing.T) {
	bashEnv := filepath.Join(t.TempDir(), "bash_env")
	binDir := goBinDir(t)

	out, err := run(t, map[string]string{"GOPATH": "", "PATH": binDir, "BASH_ENV": bashEnv},
		"install", "--detect-only", "--version", "1.22")
	assert.NilError(t, err, "got: %s", out)

	sourced, err := exec.Command("bash", "-c", ". "+bashEnv+" >/dev/null 2>&1; command -v go").Output() //nolint:gosec
	assert.NilError(t, err)
	assert.Equal(t, strings.TrimSpace(string(sourced)), filepath.Join(binDir, "go"))
}

// TestCLI_Install_NoBashEnv_ReturnsError verifies install hard-fails when
// $BASH_ENV isn't set, rather than silently doing nothing.
func TestCLI_Install_NoBashEnv_ReturnsError(t *testing.T) {
	out, err := run(t, map[string]string{"GOPATH": "", "PATH": goBinDir(t), "BASH_ENV": ""},
		"install", "--detect-only", "--version", "1.22")
	assert.Assert(t, err != nil, "expected error, got: %s", out)
	assert.Assert(t, strings.Contains(out, "BASH_ENV must be set"), "got: %s", out)
}

func TestCLI_UnknownFlag(t *testing.T) {
	_, err := run(t, nil, "install", "--no-such-flag")
	assert.Assert(t, err != nil, "expected non-zero exit for unknown flag")
}

// TestCLI_ToolchainCacheRoundTrip verifies the full cache round-trip against
// the real toolchain cache: the first invocation downloads the toolchain for
// real and saves it; the second invocation, on a wiped --install-dir but the
// same absolute path (cache entries store absolute paths), restores from
// cache without re-downloading. --install-dir/--bin-dir are pointed at temp
// dirs so this never touches the executor's real Go toolchain.
func TestCLI_ToolchainCacheRoundTrip(t *testing.T) {
	taskAgent(t)

	const version = "go1.22.3"
	installDir := t.TempDir()
	binDir := t.TempDir()
	checksum := uniqueKey(t)

	args := []string{
		"install",
		"--version", "1.22.3",
		"--install-dir", installDir,
		"--bin-dir", binDir,
		"--go-cache-checksum", checksum,
	}

	out, err := run(t, map[string]string{"GOPATH": ""}, args...)
	assert.NilError(t, err, "run 1 failed:\n%s", out)
	assertNoCacheErrors(t, out)
	// EnsureCached's fresh-install path calls Install directly and never
	// reaches Ensure's "installing" log line, so the only reliable run-1
	// observable is the absence of a cache hit.
	assert.Assert(t, !strings.Contains(out, "restored from cache"), "expected a fresh download on run 1, got: %s", out)

	goBin := filepath.Join(installDir, version, "bin", "go")
	verCmd := exec.Command(goBin, "version") //nolint:gosec
	// Without this, go1.22.3 would see the repo's go.mod (go 1.26.4) via the
	// test binary's own working directory and, under the default
	// GOTOOLCHAIN=auto, transparently forward to that newer toolchain instead
	// of reporting its own version.
	verCmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	verOut, err := verCmd.Output()
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(verOut), version), "expected %s, got: %s", version, verOut)

	link, err := os.Readlink(filepath.Join(binDir, "go"))
	assert.NilError(t, err)
	assert.Equal(t, link, goBin, "expected go to be symlinked from --bin-dir into --install-dir")

	// Simulate a fresh executor: clear the install dir so the second run must
	// rely on the cache rather than a locally present toolchain.
	assert.NilError(t, os.RemoveAll(installDir))

	out, err = run(t, map[string]string{"GOPATH": ""}, args...)
	assert.NilError(t, err, "run 2 failed:\n%s", out)
	assertNoCacheErrors(t, out)
	assert.Assert(t, strings.Contains(out, "restored from cache"), "expected run 2 to restore from cache, got: %s", out)
}
