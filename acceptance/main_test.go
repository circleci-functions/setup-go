//go:build acceptance

// Package setupgo_test exercises setup-go against real infrastructure: a real
// go.dev download, and a real CircleCI task-agent when running inside a job.
// Tagged out of `task test` because it needs network. The CLI contract itself
// is covered hermetically by clitest/.
package setupgo_test

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

// sandbox returns a sandbox plus a run function bound to its env.
//
// The cache is CircleCI's own when the caller asks for it with
// TOOLKIT_CACHE_MODE=live — which the CI job does, so the suite is exact
// there — and in-memory otherwise, so the same suite runs on a laptop.
func sandbox(t *testing.T) (*sdktest.Sandbox, func(*testing.T, map[string]string, ...string) (string, error)) {
	t.Helper()
	s := sdktest.NewSandbox(t, sdktest.WithLiveCache())
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
// toolchain honours it over its own location — so leaving it set makes the
// toolchain setup-go just installed run with the test runner's tools, and the
// suite would pass without ever exercising what it downloaded.
func sandboxEnv(s *sdktest.Sandbox, extra map[string]string) map[string]string {
	env := s.Env()
	env["GOROOT"] = ""
	maps.Copy(env, extra)
	return env
}

func fixtureDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "internal", "testing", "testproject"))
	if err != nil {
		t.Fatalf("resolve fixture dir: %v", err)
	}
	return abs
}
