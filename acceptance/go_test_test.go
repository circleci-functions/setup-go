package setupgo_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// TestCLI_Test exercises the `test` subcommand's CLI contract via a table of
// cases, each providing its own env/args setup and expectations on the exit
// error and captured output. Cases not about caching pass --cache=false so
// they don't make a pointless real cache round-trip on every run.
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
				return nil, []string{"test", "--project-path", fixtureDir(t, "test-project"), "--cache=false"}
			},
			contains: []string{"numbers", "words"},
		},
		{
			// Verifies that omitting --project-path falls back to $HOME. The
			// fixture is copied into a temp dir used as HOME rather than
			// pointing HOME at the tracked fixture directly, since `go test`
			// also derives its default build/test cache locations from $HOME
			// and would otherwise leave cache directories inside the repo.
			// GOCACHE/GOMODCACHE are pinned to the real ones so redirecting
			// HOME doesn't cold-start a stdlib rebuild.
			name: "ProjectPathDefaultsToHome",
			setup: func(t *testing.T) (map[string]string, []string) {
				home := t.TempDir()
				assert.NilError(t, os.CopyFS(home, os.DirFS(fixtureDir(t, "test-project"))))
				return map[string]string{
					"HOME":       home,
					"GOCACHE":    goEnv(t, "GOCACHE"),
					"GOMODCACHE": goEnv(t, "GOMODCACHE"),
				}, []string{"test", "--cache=false"}
			},
			contains: []string{"numbers", "words"},
		},
		{
			// Verifies --failfast and --count pass through to `go test`
			// without error on a passing suite.
			name: "FailFastAndCountFlags",
			setup: func(t *testing.T) (map[string]string, []string) {
				return nil, []string{"test", "--project-path", fixtureDir(t, "test-project"), "--failfast", "--count=2", "--cache=false"}
			},
		},
		{
			// Verifies --covermode passes through to `go test`.
			name: "CoverModeFlag",
			setup: func(t *testing.T) (map[string]string, []string) {
				return nil, []string{"test", "--project-path", fixtureDir(t, "test-project"), "--covermode", "count", "--cache=false"}
			},
			contains: []string{"coverage:"},
		},
		{
			// Relies on a real C toolchain being present to build the race
			// detector runtime — true in CI's cimg/go image and on any
			// standard dev machine.
			name: "RaceFlag",
			setup: func(t *testing.T) (map[string]string, []string) {
				return nil, []string{"test", "--project-path", fixtureDir(t, "test-project"), "--race", "--cache=false"}
			},
		},
		{
			// Verifies a failing test package causes the command to return a
			// non-zero exit.
			name: "FailingTests_ReturnsError",
			setup: func(t *testing.T) (map[string]string, []string) {
				return nil, []string{"test", "--project-path", failingProjectDir(t), "--cache=false"}
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
				return map[string]string{"PATH": t.TempDir()}, []string{"test", "--project-path", fixtureDir(t, "test-project"), "--cache=false"}
			},
			wantErr: true,
		},
		{
			// Verifies --use-gotestsum fails fast when gotestsum isn't
			// reachable, rather than silently falling back to plain `go test`.
			name: "UseGoTestSum_NotOnPath",
			setup: func(t *testing.T) (map[string]string, []string) {
				return map[string]string{"PATH": goBinDir(t)},
					[]string{"test", "--project-path", fixtureDir(t, "test-project"), "--use-gotestsum", "--cache=false"}
			},
			wantErr: true,
		},
		{
			// Verifies --use-gotestsum invokes a real gotestsum (rather than
			// go test directly). gotestsum's own "DONE" summary line proves
			// gotestsum ran — something a forwarding stub couldn't distinguish
			// from plain `go test`.
			name: "UseGoTestSum_Passthrough",
			setup: func(t *testing.T) (map[string]string, []string) {
				return map[string]string{"PATH": gotestsumPATH(t)},
					[]string{"test", "--project-path", fixtureDir(t, "test-project"), "--use-gotestsum", "--cache=false"}
			},
			contains: []string{"numbers", "words", "DONE"},
		},
	}

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
// executor's real GOCACHE. --cache-key is unique per subtest: without it the
// second subtest's save would be skipped as a duplicate of the first
// subtest's immutable key, and its restore would return the first subtest's
// bytes.
func TestCLI_Test_BuildCacheRoundTrip(t *testing.T) {
	tests := []struct {
		name        string
		projectPath func(t *testing.T) string
		wantErr     bool
	}{
		{name: "PassingSuite", projectPath: func(t *testing.T) string { return fixtureDir(t, "test-project") }},
		{name: "FailingSuite_StillSavesCache", projectPath: failingProjectDir, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			taskAgent(t)

			cacheDir := t.TempDir()
			key := uniqueKey(t)
			writeMarker(t, cacheDir, "hit")

			args := []string{"test", "--project-path", tt.projectPath(t), "--cache-path", cacheDir, "--cache-key", key, "--checksum", "checksum-a"}

			out, err := run(t, nil, args...)
			if tt.wantErr {
				assert.Assert(t, err != nil, "expected first run to fail, got: %s", out)
			} else {
				assert.NilError(t, err, "first run failed: %s", out)
			}
			assertNoCacheErrors(t, out)

			assert.NilError(t, os.RemoveAll(cacheDir))

			out, _ = run(t, nil, args...)
			assertNoCacheErrors(t, out)

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
