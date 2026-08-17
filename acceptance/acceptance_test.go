package setupgo_test

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk/sdktest"
	"github.com/circleci-functions/toolkit/testing/compiler"
	"github.com/circleci-functions/toolkit/testing/runner"
)

var binaryPath, gotestsumPath string

func TestMain(m *testing.M) {
	status, err := runTests(m)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	os.Exit(status)
}

func runTests(m *testing.M) (int, error) {
	ctx := context.Background()

	p := compiler.NewParallel(2)
	defer p.Cleanup()

	p.Add(compiler.Work{
		Result: &binaryPath,
		Name:   "setup-go",
		Target: "..",
		Source: ".",
	})
	// gotestsum is a go.mod tool dependency, not necessarily on PATH in a bare
	// executor. Building the pinned version keeps --use-gotestsum coverage
	// hermetic rather than depending on whatever the base image happens to ship.
	p.Add(compiler.Work{
		Result: &gotestsumPath,
		Name:   "gotestsum",
		Target: "..",
		Source: "gotest.tools/gotestsum",
	})

	if err := p.Run(ctx); err != nil {
		return 0, err
	}

	return m.Run(), nil
}

// run runs the compiled binary in its own temp directory, with baseEnv merged
// under extraEnv (extraEnv wins on any key both set). Pass a nil extraEnv when
// no overrides beyond baseEnv are needed.
func run(t *testing.T, extraEnv map[string]string, args ...string) (string, error) {
	t.Helper()
	return runIn(t, t.TempDir(), extraEnv, args...)
}

// runIn is like run but with an explicit working directory. mod-download runs
// `go mod download` with cmd.Dir unset (main.go), i.e. in the process's
// working directory, so tests that need a real go.mod present must set dir
// themselves rather than relying on run's bare t.TempDir().
func runIn(t *testing.T, dir string, extraEnv map[string]string, args ...string) (string, error) {
	t.Helper()

	env := baseEnv(t)
	for k, v := range extraEnv {
		env[k] = v
	}

	r := runner.New(t, binaryPath,
		runner.WithUnsafeOSEnvVars(),
		runner.WithUnsafeAdditionalEnvVars(env),
		runner.WithCmdDir(dir))
	_, err := r.Run(args...)
	return r.StdoutString() + r.StderrString(), err
}

// baseEnv sets the env vars every invocation needs, regardless of what the
// test is exercising.
func baseEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		// install hard-fails without BASH_ENV set (toolkit bashenv package), so
		// it's a required input rather than an optional override. It must be a
		// throwaway file rather than the job's real one: bashenv appends, so an
		// inherited BASH_ENV would leak this suite's PATH exports into every
		// later step in the job.
		"BASH_ENV": filepath.Join(t.TempDir(), "bash_env"),
		// Guards against a fixture's `go` directive exceeding the executor's
		// toolchain: with GOTOOLCHAIN=auto that would silently download a new
		// toolchain mid-test; with "local" it's a loud error instead.
		"GOTOOLCHAIN": "local",
	}
}

// taskAgent points the binary under test at a cache backend. Inside a real
// CircleCI job it does nothing, and the binary reaches the real task agent
// over $TMPDIR/circleci-plugin.sock. Off-CI (e.g. a laptop running `task
// test`) it starts the fake plugin server and points the binary at it via
// CIRCLECI_PLUGIN_URL, so the suite still exercises the CLI rather than being
// skipped outright.
//
// CI is authoritative: the fake is a mutable map with exact-key lookup, so it
// has no key immutability, no prefix matching, and no project scoping. A
// green run against the fake is not a guarantee against the real agent.
func taskAgent(t *testing.T) {
	t.Helper()

	if hasRealTaskAgent(t) {
		return
	}

	t.Logf("no task agent at %s; falling back to the sdktest fake", taskAgentSocket())
	sdktest.NewServer(t).SetupEnv(t)
}

// requireRealTaskAgent skips the test when there's no live task agent,
// rather than falling back to the sdktest fake. Real Go module caches
// contain read-only directories (part of Go's module-cache immutability
// guarantee), and sdktest's directory copy preserves each source entry's
// mode before writing into the corresponding target, so it fails partway
// through copying a real GOMODCACHE. The real cache backend has no such
// issue — a tar archiver only needs read access to source files — so this
// only ever skips locally.
func requireRealTaskAgent(t *testing.T) {
	t.Helper()
	if !hasRealTaskAgent(t) {
		t.Skip("no real CircleCI task agent: sdktest's fake can't copy a real GOMODCACHE's read-only module directories")
	}
}

func taskAgentSocket() string {
	return filepath.Join(os.TempDir(), "circleci-plugin.sock")
}

func hasRealTaskAgent(t *testing.T) bool {
	t.Helper()

	sock := taskAgentSocket()
	c, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	t.Logf("using the real task agent at %s", sock)
	return true
}

var keySanitizeRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// sanitizeKey reduces s to [A-Za-z0-9._-]. Cache keys are rendered through a
// text/template by the real save_cache step, so a literal "{{" must never
// appear; subtest names (which contain "/") are the main source of that risk.
func sanitizeKey(s string) string {
	return keySanitizeRe.ReplaceAllString(s, "-")
}

// uniqueKey returns a cache key component unique to this job attempt and this
// test. Uniqueness per attempt is mandatory, not hygiene: the real save_cache
// step is immutable per key ("Skipping cache generation, cache already exists
// for key"), so without it a rerun-from-failed would restore a previous
// attempt's bytes and a save-then-assert-restore test would pass or fail on
// stale data rather than on what this run actually did.
//
// CIRCLE_WORKFLOW_JOB_ID is the per-job-instance id: CircleCI mints a new one
// on every rerun (including rerun-from-failed), unlike CIRCLE_WORKFLOW_ID.
func uniqueKey(t *testing.T) string {
	t.Helper()

	id := os.Getenv("CIRCLE_WORKFLOW_JOB_ID")
	if id == "" {
		id = os.Getenv("CIRCLE_BUILD_NUM")
	}
	if id == "" {
		id = strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return sanitizeKey(id + "-" + t.Name())
}

var cacheErrRe = regexp.MustCompile(`(?m)^cache (save|restore)\b`)

// assertNoCacheErrors fails if the binary logged a cache error. sdk.LogCacheErr
// prints "cache <op>: <err>" for every failure except ErrNoServer (not running
// inside a job), so this catches a stale socket, a rejected key, or a
// server-side error — none of which change the binary's exit status.
func assertNoCacheErrors(t *testing.T, out string) {
	t.Helper()
	assert.Assert(t, !cacheErrRe.MatchString(out), "binary logged a cache error:\n%s", out)
}

// writeMarker writes a file into dir so a later cache restore is verifiable.
// Must be called before the first save under a given key: the real cache
// step silently uploads an empty archive for a path that doesn't exist or is
// empty, permanently burning that key.
func writeMarker(t *testing.T, dir, content string) {
	t.Helper()
	assert.NilError(t, os.MkdirAll(dir, 0750))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "marker"), []byte(content), 0600))
}

// dirTree renders a recursive listing of dir, for use in assertion failure
// messages. Cache save/restore output goes to a separate CircleCI step, not
// this binary's stdout, so a bare "marker missing" message gives no way to
// tell whether the directory is empty, missing, or has unexpected contents.
func dirTree(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			fmt.Fprintf(&b, "%s: %v\n", path, err)
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = path
		}
		fmt.Fprintln(&b, rel)
		return nil
	})
	return b.String()
}

// goEnv returns the real `go env <name>` value for the go on PATH.
func goEnv(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("go", "env", name).Output() //nolint:gosec
	assert.NilError(t, err)
	return strings.TrimSpace(string(out))
}

// goBinDir returns GOROOT/bin for the toolchain running the tests: a real
// directory containing go and gofmt and nothing else — in particular no
// cc/gcc/clang — and one for which install --detect-only's GOROOT inference
// (two dirs up from the resolved `go`) is correct by construction. Derived
// from `go env GOROOT` rather than the directory containing `go` on PATH,
// because Homebrew and GOTOOLCHAIN-managed toolchains put the launcher
// outside GOROOT.
func goBinDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(goEnv(t, "GOROOT"), "bin")
	name := "go"
	if runtime.GOOS == "windows" {
		name = "go.exe"
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Skipf("no go binary at %s: %v", dir, err)
	}
	return dir
}

// gotestsumPATH returns a PATH containing the really-built gotestsum plus a
// go-only directory, so --use-gotestsum has a real gotestsum to exec and a
// real go for it to invoke, but nothing else (e.g. not the setup-go binary
// that compiler built into the same directory as gotestsum).
func gotestsumPATH(t *testing.T) string {
	t.Helper()
	return strings.Join([]string{filepath.Dir(gotestsumPath), goBinDir(t)}, string(os.PathListSeparator))
}

// fixtureDir resolves acceptance/<name> to an absolute path. The compiled
// setup-go binary is run with its own temp directory as its working
// directory, so a relative path would resolve against the wrong directory.
func fixtureDir(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(name)
	assert.NilError(t, err)
	return abs
}
