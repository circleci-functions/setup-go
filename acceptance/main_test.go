//go:build acceptance

// Package setupgo_test exercises setup-go against real infrastructure: a real
// go.dev download, and a real CircleCI task-agent when running inside a job.
// Tagged out of `task test` because it needs network. The CLI contract itself
// is covered hermetically by clitest/.
package setupgo_test

import (
	"context"
	"fmt"
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

// sandbox returns a sandbox wired to the real task-agent when inside a CircleCI
// job and to the fake otherwise, plus a run function bound to its env.
func sandbox(t *testing.T) (*sdktest.Sandbox, func(*testing.T, map[string]string, ...string) (string, error)) {
	t.Helper()
	s := sdktest.NewSandbox(t, sdktest.WithRealAgent())
	return s, func(t *testing.T, extraEnv map[string]string, args ...string) (string, error) {
		t.Helper()
		r := runner.New(t, binaryPath, runner.WithSandboxEnv(s.EnvWith(extraEnv)))
		_, err := r.Run(args...)
		return r.StdoutString() + r.StderrString(), err
	}
}

func fixtureDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "internal", "testing", "testproject"))
	if err != nil {
		t.Fatalf("resolve fixture dir: %v", err)
	}
	return abs
}
