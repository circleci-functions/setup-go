package setupgo_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// TestCLI_ModDownload_CacheRoundTrip verifies the full cache round-trip: the
// first invocation runs a real `go mod download` against the mod-project
// fixture and saves the redirected GOMODCACHE; with GOMODCACHE cleared, a
// second invocation under the same key restores it before `go mod download`
// runs again.
func TestCLI_ModDownload_CacheRoundTrip(t *testing.T) {
	requireRealTaskAgent(t)

	modCache := t.TempDir()
	key := uniqueKey(t)
	dir := fixtureDir(t, "mod-project")
	env := map[string]string{"GOMODCACHE": modCache}
	args := []string{"mod-download", "--cache-key", key, "--checksum", "checksum-a"}

	// The marker is something `go mod download` will never create, so its
	// presence after run 2 proves the restore repopulated the directory —
	// run 2 also runs a real `go mod download`, which would otherwise
	// repopulate the go-cmp cache entry itself regardless of whether the
	// restore worked.
	writeMarker(t, modCache, "hit")

	out, err := runIn(t, dir, env, args...)
	assert.NilError(t, err, "first run failed:\n%s", out)
	assertNoCacheErrors(t, out)

	_, err = os.Stat(filepath.Join(modCache, "cache", "download", "github.com", "google", "go-cmp", "@v"))
	assert.NilError(t, err, "expected go mod download to populate GOMODCACHE, got:\n%s", dirTree(t, modCache))

	assert.NilError(t, os.RemoveAll(modCache))

	out, err = runIn(t, dir, env, args...)
	assert.NilError(t, err, "second run failed:\n%s", out)
	assertNoCacheErrors(t, out)

	got, err := os.ReadFile(filepath.Join(modCache, "marker")) //nolint:gosec
	assert.NilError(t, err, "second run output:\n%s\nmod cache now:\n%s", out, dirTree(t, modCache))
	assert.Equal(t, string(got), "hit")
}

// TestCLI_ModDownload_NoCache_DoesNotSaveModCache verifies --cache=false
// skips saving to the cache: a subsequent cache load under the same key
// restores nothing, even though `go mod download` itself still ran.
//
// The old stubbed assertion this replaces ("go env -json" is never called)
// is not observable with a real `go`: `go mod download` needs a real `go`,
// so `go env -json` can't be made to fail independently of it. What's
// observable is the effect the skip protects — nothing to restore later.
func TestCLI_ModDownload_NoCache_DoesNotSaveModCache(t *testing.T) {
	requireRealTaskAgent(t)

	modCache := t.TempDir()
	key := uniqueKey(t)
	env := map[string]string{"GOMODCACHE": modCache}

	out, err := runIn(t, fixtureDir(t, "mod-project"), env,
		"mod-download", "--cache=false", "--cache-key", key, "--checksum", "checksum-a")
	assert.NilError(t, err, "got: %s", out)
	assertNoCacheErrors(t, out)

	assert.NilError(t, os.RemoveAll(modCache))

	out, err = run(t, nil, "cache", "load", "--go-mod", "--key", key, "--checksum", "checksum-a")
	assert.NilError(t, err, "load failed: %s", out)
	assertNoCacheErrors(t, out)

	_, err = os.Stat(modCache)
	assert.Assert(t, os.IsNotExist(err), "expected --cache=false to have saved nothing (so there's nothing to restore)")
}

// TestCLI_ModDownload_PathOverridesDetectedModCache verifies --path saves the
// override directory instead of the go-env-detected GOMODCACHE. Two distinct
// directories, neither recreated after being removed: only the one that was
// actually part of the save request comes back from a restore.
func TestCLI_ModDownload_PathOverridesDetectedModCache(t *testing.T) {
	requireRealTaskAgent(t)

	detected, override := t.TempDir(), t.TempDir()
	writeMarker(t, override, "override")
	key := uniqueKey(t)
	env := map[string]string{"GOMODCACHE": detected}

	out, err := runIn(t, fixtureDir(t, "mod-project"), env,
		"mod-download", "--path", override, "--cache-key", key, "--checksum", "checksum-a")
	assert.NilError(t, err, "got: %s", out)
	assertNoCacheErrors(t, out)

	assert.NilError(t, os.RemoveAll(detected))
	assert.NilError(t, os.RemoveAll(override))

	out, err = run(t, nil, "cache", "load", "--go-mod", "--key", key, "--checksum", "checksum-a")
	assert.NilError(t, err, "load failed: %s", out)
	assertNoCacheErrors(t, out)

	got, err := os.ReadFile(filepath.Join(override, "marker")) //nolint:gosec
	assert.NilError(t, err, "expected --path dir restored; override now:\n%s", dirTree(t, override))
	assert.Equal(t, string(got), "override")

	_, err = os.Stat(detected)
	assert.Assert(t, os.IsNotExist(err),
		"expected the go-env-detected dir not to have been saved (and so not restored), got:\n%s", dirTree(t, detected))
}

// TestCLI_ModDownload is a table test over mod-download's simpler CLI
// contract: flag validation and error propagation.
func TestCLI_ModDownload(t *testing.T) {
	tests := []struct {
		name string
		// setup prepares any fixtures/env needed for the case and returns the
		// working directory, env overrides, and full argv (including the
		// "mod-download" subcommand).
		setup    func(t *testing.T) (dir string, env map[string]string, args []string)
		wantErr  bool
		contains []string
	}{
		{
			// A `go mod download` failure (no go.mod present) must surface as
			// a non-zero exit with the underlying error visible, not be
			// swallowed.
			name: "GoModDownloadFails_ReturnsError",
			setup: func(t *testing.T) (string, map[string]string, []string) {
				return t.TempDir(), nil, []string{"mod-download", "--cache=false"}
			},
			wantErr:  true,
			contains: []string{"go mod download", "no modules specified"},
		},
		{
			// The check runs after the download, so this case needs a real
			// module as the working directory. --cache-key/--checksum are
			// set even though this test isn't about caching, so the one
			// restore call it makes before failing never touches an
			// unscoped, empty-key cache slot.
			name: "Path_EmptyString_ReturnsError",
			setup: func(t *testing.T) (string, map[string]string, []string) {
				return fixtureDir(t, "mod-project"), nil,
					[]string{"mod-download", "--path=", "--cache-key", uniqueKey(t), "--checksum", "checksum-a"}
			},
			wantErr:  true,
			contains: []string{"must pass a non-empty directory with --path"},
		},
		{
			name: "NoGoOnPath_ReturnsError",
			setup: func(t *testing.T) (string, map[string]string, []string) {
				return t.TempDir(), map[string]string{"PATH": t.TempDir()}, []string{"mod-download", "--cache=false"}
			},
			wantErr:  true,
			contains: []string{"executable file not found"},
		},
		{
			name: "UnknownFlag",
			setup: func(t *testing.T) (string, map[string]string, []string) {
				return t.TempDir(), nil, []string{"mod-download", "--no-such-flag"}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, env, args := tt.setup(t)
			out, err := runIn(t, dir, env, args...)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error, got: %s", out)
			} else {
				assert.NilError(t, err, "got: %s", out)
			}
			for _, s := range tt.contains {
				assert.Assert(t, strings.Contains(out, s), "expected %q in output, got: %s", s, out)
			}
		})
	}
}
