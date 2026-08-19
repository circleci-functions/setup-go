// Package clitest_test exercises the setup-go CLI contract end to end against
// fakes: a fake go.dev (internal/testing/fakegodev) and a fake CircleCI plugin
// server (functest's sandbox). Hermetic and no network, so it runs under
// `task test` on every commit. Real go.dev and a real task-agent are covered
// by acceptance/.
package clitest_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/circleci-functions/toolkit/testing/compiler"
	"github.com/circleci-functions/toolkit/testing/functest"
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

func runTests(m *testing.M) (int, error) {
	p := compiler.NewParallel(1)
	defer p.Cleanup()

	p.Add(compiler.Work{
		Result: &binaryPath,
		Name:   "setup-go",
		Target: "..",
		Source: ".",
	})

	if err := p.Run(context.Background()); err != nil {
		return 0, err
	}
	return m.Run(), nil
}

// run runs the compiled binary with extraEnv merged on top of the inherited
// environment, so entries in extraEnv take precedence.
func run(t *testing.T, extraEnv map[string]string, args ...string) (string, error) {
	t.Helper()
	r := runner.New(t, binaryPath, runner.WithUnsafeOSEnvVars(), runner.WithUnsafeAdditionalEnvVars(extraEnv))
	_, err := r.Run(args...)
	return r.StdoutString() + r.StderrString(), err
}

// sandbox returns a functest sandbox plus a run function bound to its env, so a
// test can't accidentally install into the developer's real home directory.
func sandbox(t *testing.T) (*functest.Sandbox, func(*testing.T, map[string]string, ...string) (string, error)) {
	t.Helper()
	s := functest.New(t)
	return s, func(t *testing.T, extraEnv map[string]string, args ...string) (string, error) {
		t.Helper()
		return run(t, s.EnvWith(extraEnv), args...)
	}
}

// fixtureDir resolves the checked-in test project to an absolute path. The
// binary runs with its own temp dir as cwd, so a relative path would resolve
// against the wrong directory.
func fixtureDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "internal", "testing", "testproject"))
	if err != nil {
		t.Fatalf("resolve fixture dir: %v", err)
	}
	return abs
}
