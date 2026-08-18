//go:build acceptance

package setupgo_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/circleci-functions/toolkit/testing/compiler"
	"github.com/circleci-functions/toolkit/testing/runner"
)

var binaryPath string

func TestMain(m *testing.M) {
	status, err := runTests(m)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	os.Exit(status)
}

// runTests resolves the setup-go binary under test, then runs the suite. In
// CI, SETUP_GO_ACCEPTANCE_BINARY points at the already cross-compiled release
// artifact the `build` job produced (the same one `publish` is about to
// ship), since these tests run on a bare executor with no `go` on PATH for
// the toolkit compiler to invoke. With no env var set, it falls back to
// compiling a fresh copy via the toolkit compiler, for local/dev runs.
func runTests(m *testing.M) (int, error) {
	if bin := os.Getenv("SETUP_GO_ACCEPTANCE_BINARY"); bin != "" {
		binaryPath = bin
		return m.Run(), nil
	}

	ctx := context.Background()

	p := compiler.NewParallel(1)
	defer p.Cleanup()

	p.Add(compiler.Work{
		Result: &binaryPath,
		Name:   "setup-go",
		Target: "..",
		Source: ".",
	})

	if err := p.Run(ctx); err != nil {
		return 0, err
	}

	return m.Run(), nil
}

// run runs the compiled binary with extraEnv merged on top of the inherited
// environment, so entries in extraEnv take precedence over any matching
// inherited vars. Pass a nil extraEnv when no overrides are needed.
func run(t *testing.T, extraEnv map[string]string, args ...string) (string, error) {
	t.Helper()
	r := runner.New(t, binaryPath, runner.WithUnsafeOSEnvVars(), runner.WithUnsafeAdditionalEnvVars(extraEnv))
	_, err := r.Run(args...)
	return r.StdoutString() + r.StderrString(), err
}
