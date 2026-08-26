package clitest_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// TestCLI_Test exercises the `test` subcommand's CLI contract via a table of
// cases, each providing its own env/args setup and expectations on the
// exit error and captured output.
func TestCLI_Test(t *testing.T) {
	tests := []struct {
		name string
		// setup prepares any fixtures/env needed for the case and returns
		// the env overrides and full argv (including the "test" subcommand).
		setup       func(t *testing.T) (env map[string]string, args []string)
		wantErr     bool
		contains    []string
		notContains []string
	}{
		{
			// Runs `test` against the checked-in go-test fixture (a tiny
			// module with two passing test packages) and verifies the
			// underlying `go test ./...` run reports both packages as passing.
			name: "RunsGoTestSuite",
			setup: func(t *testing.T) (map[string]string, []string) {
				return nil, []string{"test", "--project-path", fixtureDir(t)}
			},
			contains: []string{"numbers", "words"},
		},
		{
			// Verifies that omitting --project-path falls back to $HOME. The
			// fixture is copied into a temp dir used as HOME rather than
			// pointing HOME at the tracked fixture directly, since `go test`
			// also derives its default build/test cache locations from $HOME
			// and would otherwise leave cache directories inside the repo.
			name: "ProjectPathDefaultsToHome",
			setup: func(t *testing.T) (map[string]string, []string) {
				home := t.TempDir()
				assert.NilError(t, os.CopyFS(home, os.DirFS(fixtureDir(t))))
				return map[string]string{"HOME": home}, []string{"test"}
			},
			contains: []string{"numbers", "words"},
		},
		{
			// Verifies --failfast and --count pass through to `go test`
			// without error on a passing suite.
			name: "FailFastAndCountFlags",
			setup: func(t *testing.T) (map[string]string, []string) {
				return nil, []string{"test", "--project-path", fixtureDir(t), "--failfast", "--count=2"}
			},
		},
		{
			// Verifies --covermode passes through to `go test`.
			name: "CoverModeFlag",
			setup: func(t *testing.T) (map[string]string, []string) {
				return nil, []string{"test", "--project-path", fixtureDir(t), "--covermode", "count"}
			},
			contains: []string{"coverage:"},
		},
		{
			// Relies on a real C toolchain being present to build the race
			// detector runtime — true in CI's cimg/go image and on any
			// standard dev machine (the same assumption other setup-go
			// acceptance tests make about cgo availability).
			name: "RaceFlag",
			setup: func(t *testing.T) (map[string]string, []string) {
				return nil, []string{"test", "--project-path", fixtureDir(t), "--race"}
			},
		},
		{
			// Verifies a failing test package causes the command to return a
			// non-zero exit.
			name: "FailingTests_ReturnsError",
			setup: func(t *testing.T) (map[string]string, []string) {
				return nil, []string{"test", "--project-path", failingProjectDir(t)}
			},
			wantErr:  true,
			contains: []string{"FAIL"},
		},
		{
			name: "UnknownFlag",
			setup: func(t *testing.T) (map[string]string, []string) {
				return nil, []string{"test", "--no-such-flag"}
			},
			wantErr: true,
		},
		{
			// Verifies the explicit `go`-on-PATH check: with PATH pointing at
			// an empty directory, the command must fail before attempting to
			// run anything.
			name: "NoGoOnPath",
			setup: func(t *testing.T) (map[string]string, []string) {
				return map[string]string{"PATH": t.TempDir()}, []string{"test", "--project-path", fixtureDir(t)}
			},
			wantErr: true,
		},
		{
			// Verifies --use-gotestsum fails fast when gotestsum isn't
			// reachable, rather than silently falling back to plain `go test`.
			name: "UseGoTestSum_NotOnPath",
			setup: func(t *testing.T) (map[string]string, []string) {
				return map[string]string{"PATH": goOnlyPATH(t)},
					[]string{"test", "--project-path", fixtureDir(t), "--use-gotestsum"}
			},
			wantErr: true,
		},
		{
			// Verifies --use-gotestsum invokes gotestsum (rather than go test
			// directly) with the expected trailing args. gotestsum is only a
			// go.mod tool dependency here, not installed on PATH, so this
			// stubs it with a script that forwards straight through to `go test`.
			name: "UseGoTestSum_Passthrough",
			setup: func(t *testing.T) (map[string]string, []string) {
				return map[string]string{"PATH": gotestsumStubPATH(t)},
					[]string{"test", "--project-path", fixtureDir(t), "--use-gotestsum"}
			},
			contains: []string{"numbers", "words"},
		},
	}

	// One sandbox for the whole table: these cases only exercise flag
	// pass-through, and a shared $HOME lets `go test` reuse its build cache
	// between them instead of recompiling the fixture each time.
	_, run := sandbox(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, args := tt.setup(t)
			out, err := run(t, env, args...)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error, got: %s", out)
			} else {
				assert.NilError(t, err, "got: %s", out)
			}
			for _, s := range tt.contains {
				assert.Assert(t, strings.Contains(out, s), "expected %q in output, got: %s", s, out)
			}
			for _, s := range tt.notContains {
				assert.Assert(t, !strings.Contains(out, s), "expected %q not in output, got: %s", s, out)
			}
		})
	}
}

// TestCLI_Test_BuildCacheRoundTrip verifies that `test` restores the go build
// cache before running and saves it afterwards — including when the tests
// fail, since a failed run still compiled packages a re-run can reuse.
// --cache-path points the save at a temp dir so the test never touches the
// executor's real GOCACHE.
func TestCLI_Test_BuildCacheRoundTrip(t *testing.T) {
	tests := []struct {
		name        string
		projectPath func(t *testing.T) string
		wantErr     bool
	}{
		{name: "PassingSuite", projectPath: fixtureDir},
		{name: "FailingSuite_StillSavesCache", projectPath: failingProjectDir, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, run := sandbox(t)

			cacheDir := t.TempDir()
			assert.NilError(t, os.WriteFile(filepath.Join(cacheDir, "marker"), []byte("hit"), 0600))

			args := []string{"test", "--project-path", tt.projectPath(t), "--cache-path", cacheDir, "--checksum", "checksum-a"}

			out, err := run(t, nil, args...)
			if tt.wantErr {
				assert.Assert(t, err != nil, "expected first run to fail, got: %s", out)
			} else {
				assert.NilError(t, err, "first run failed: %s", out)
			}

			// The save is registered to run at the end of the job, so it has
			// not happened yet when the command exits.
			assert.Equal(t, len(s.Cache.PendingCacheSaves()), 1)
			s.Cache.EndJob(t)

			assert.NilError(t, os.RemoveAll(cacheDir))

			out, _ = run(t, nil, args...)

			got, err := os.ReadFile(filepath.Join(cacheDir, "marker")) //nolint:gosec
			assert.NilError(t, err, "second run output:\n%s", out)
			assert.Equal(t, string(got), "hit", "expected build cache marker restored from a prior save")
		})
	}
}

// failingProjectDir writes a minimal module with a single always-failing test.
func failingProjectDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module failtest\n\ngo 1.21\n"), 0600))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "fail_test.go"), []byte(
		"package failtest\n\nimport \"testing\"\n\nfunc TestAlwaysFails(t *testing.T) {\n\tt.Fatal(\"boom\")\n}\n"), 0600))
	return dir
}

// goOnlyPATH returns a PATH directory containing only a symlink to the real
// go binary — no gotestsum, no other tools.
func goOnlyPATH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	symlinkTool(t, dir, "go")
	return dir
}

// gotestsumStubPATH returns a PATH directory containing a symlink to the real
// go binary plus a "gotestsum" stub script. setup-go invokes the stub as
// `gotestsum -- <go test args>`; the stub drops the leading "--" and execs
// `go test` with the rest, so the fixture's real test output comes back.
func gotestsumStubPATH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	symlinkTool(t, dir, "go")

	script := "#!/bin/sh\nshift\nexec go test \"$@\"\n"
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "gotestsum"), []byte(script), 0750))
	return dir
}

func symlinkTool(t *testing.T, dir, name string) {
	t.Helper()
	real, err := exec.LookPath(name)
	assert.NilError(t, err)
	assert.NilError(t, os.Symlink(real, filepath.Join(dir, name)))
}
