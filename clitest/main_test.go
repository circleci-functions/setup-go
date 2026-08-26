// Package clitest_test exercises the setup-go CLI contract end to end against
// fakes: a fake go.dev (internal/testing/fakegodev) and a fake CircleCI plugin
// server (sdktest's sandbox). Hermetic and no network, so it runs under
// `task test` on every commit. Real go.dev and a real task-agent are covered
// by acceptance/.
package clitest_test

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/circleci-functions/toolkit/sdk/sdktest"
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

// runFunc runs the compiled binary with extraEnv applied on top of the
// sandbox's environment, so entries in extraEnv take precedence.
type runFunc func(t *testing.T, extraEnv map[string]string, args ...string) (string, error)

// sandbox returns a sandbox plus a run function bound to its env. It is the only
// way to run the binary in this package, so no test can install into the
// developer's real home directory or reach a cache it did not set up.
func sandbox(t *testing.T) (*sdktest.Sandbox, runFunc) {
	t.Helper()

	s := sdktest.NewSandbox(t)
	return s, func(t *testing.T, extraEnv map[string]string, args ...string) (string, error) {
		t.Helper()
		r := runner.New(t, binaryPath, runner.WithSandboxEnv(sandboxEnv(s, extraEnv)))
		_, err := r.Run(args...)
		return r.StdoutString() + r.StderrString(), err
	}
}

// sandboxEnv returns the sandbox's environment with extra applied on top.
//
// GOROOT is cleared: `go test` sets it for this test process, and a Go
// toolchain honours it over its own location — so leaving it set makes a
// toolchain setup-go installs run with the test runner's tools. This is
// setup-go's concern rather than the sandbox's, since only a function that
// installs a Go toolchain has it.
func sandboxEnv(s *sdktest.Sandbox, extra map[string]string) map[string]string {
	env := s.Env()
	env["GOROOT"] = ""
	maps.Copy(env, extra)
	return env
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
