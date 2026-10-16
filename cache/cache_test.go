package cache_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk"
	"github.com/circleci-functions/toolkit/sdk/sdktest"

	"github.com/circleci-functions/setup-go/cache"
)

// TestLintCacheDir pins the directory golangci-lint itself would use, so the
// cache that is saved is the one it writes.
func TestLintCacheDir(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CACHE_HOME", "")
		t.Setenv("GOLANGCI_LINT_CACHE", "")
		localAppData := t.TempDir()
		t.Setenv("LocalAppData", localAppData)

		want := map[string]string{
			"linux":   filepath.Join(home, ".cache", "golangci-lint"),
			"darwin":  filepath.Join(home, "Library", "Caches", "golangci-lint"),
			"windows": filepath.Join(localAppData, "golangci-lint"),
		}[runtime.GOOS]
		if want == "" {
			t.Skipf("no expected cache directory for %s", runtime.GOOS)
		}

		dir, err := cache.LintCacheDir()
		assert.NilError(t, err)
		assert.Equal(t, dir, want)
	})

	t.Run("XDG_CACHE_HOME", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("only Linux reads XDG_CACHE_HOME")
		}
		xdg := t.TempDir()
		t.Setenv("XDG_CACHE_HOME", xdg)
		t.Setenv("GOLANGCI_LINT_CACHE", "")

		dir, err := cache.LintCacheDir()
		assert.NilError(t, err)
		assert.Equal(t, dir, filepath.Join(xdg, "golangci-lint"))
	})

	t.Run("GOLANGCI_LINT_CACHE", func(t *testing.T) {
		want := t.TempDir()
		t.Setenv("GOLANGCI_LINT_CACHE", want)

		dir, err := cache.LintCacheDir()
		assert.NilError(t, err)
		assert.Equal(t, dir, want)
	})

	t.Run("GOLANGCI_LINT_CACHE off", func(t *testing.T) {
		t.Setenv("GOLANGCI_LINT_CACHE", "off")

		dir, err := cache.LintCacheDir()
		assert.NilError(t, err)
		assert.Equal(t, dir, "")
	})

	t.Run("GOLANGCI_LINT_CACHE relative", func(t *testing.T) {
		t.Setenv("GOLANGCI_LINT_CACHE", "lint-cache")

		_, err := cache.LintCacheDir()
		assert.ErrorContains(t, err, "not an absolute path")
	})
}

// TestRestoreAndSaveLintCache_FollowsTheEnv verifies that the lint cache
// round-trips through the directory $GOLANGCI_LINT_CACHE names.
func TestRestoreAndSaveLintCache_FollowsTheEnv(t *testing.T) {
	srv := sdktest.NewServer(t)
	client := srv.Client()
	ctx := context.Background()

	cacheDir := filepath.Join(t.TempDir(), "lint")
	t.Setenv("GOLANGCI_LINT_CACHE", cacheDir)
	assert.NilError(t, os.MkdirAll(cacheDir, 0750))
	assert.NilError(t, os.WriteFile(filepath.Join(cacheDir, "marker"), []byte("hit"), 0600))

	cache.SaveLintCache(ctx, client, "", "checksum-a")
	assert.NilError(t, os.RemoveAll(cacheDir))
	cache.RestoreLintCache(ctx, client, "", "checksum-a")

	got, err := os.ReadFile(filepath.Join(cacheDir, "marker")) //nolint:gosec
	assert.NilError(t, err)
	assert.Equal(t, string(got), "hit")
}

// TestSaveLintCache_OffSavesNothing verifies that GOLANGCI_LINT_CACHE=off,
// under which golangci-lint caches nothing, registers no save.
func TestSaveLintCache_OffSavesNothing(t *testing.T) {
	srv := sdktest.NewServer(t)
	client := srv.Client()
	t.Setenv("GOLANGCI_LINT_CACHE", "off")

	cache.SaveLintCache(context.Background(), client.AtTeardown(), "", "checksum-a")

	assert.Equal(t, len(srv.PendingCacheSaves()), 0)
}

// TestRestoreAndSaveLintCache_RoundTrip verifies that saving the lint cache
// dir under a checksum and restoring it under the same checksum round-trips
// a file written into the cache dir.
func TestRestoreAndSaveLintCache_RoundTrip(t *testing.T) {
	srv := sdktest.NewServer(t)
	client := srv.Client()
	ctx := context.Background()

	t.Setenv("HOME", t.TempDir())
	cacheDir, err := cache.LintCacheDir()
	assert.NilError(t, err)
	assert.NilError(t, os.MkdirAll(cacheDir, 0750))

	assert.NilError(t, os.WriteFile(filepath.Join(cacheDir, "marker"), []byte("hit"), 0600))
	cache.SaveLintCache(ctx, client, "", "checksum-a")

	assert.NilError(t, os.RemoveAll(cacheDir))

	cache.RestoreLintCache(ctx, client, "", "checksum-a")

	got, err := os.ReadFile(filepath.Join(cacheDir, "marker")) //nolint:gosec
	assert.NilError(t, err)
	assert.Equal(t, string(got), "hit")
}

// TestRestoreLintCache_ChecksumPartitionsCache verifies that a restore under
// a different checksum than what was saved misses the cache.
func TestRestoreLintCache_ChecksumPartitionsCache(t *testing.T) {
	srv := sdktest.NewServer(t)
	client := srv.Client()
	ctx := context.Background()

	t.Setenv("HOME", t.TempDir())
	cacheDir, err := cache.LintCacheDir()
	assert.NilError(t, err)
	assert.NilError(t, os.MkdirAll(cacheDir, 0750))

	assert.NilError(t, os.WriteFile(filepath.Join(cacheDir, "marker"), []byte("hit"), 0600))
	cache.SaveLintCache(ctx, client, "", "checksum-a")

	assert.NilError(t, os.RemoveAll(cacheDir))

	cache.RestoreLintCache(ctx, client, "", "checksum-b")

	_, err = os.Stat(filepath.Join(cacheDir, "marker"))
	assert.Assert(t, os.IsNotExist(err), "expected no marker file restored under a different checksum")
}

// TestLintCache_CacheErrorIsNonFatal verifies that a client which can't
// reach a server never blocks restore/save.
func TestLintCache_CacheErrorIsNonFatal(t *testing.T) {
	client := sdk.NewWithBaseURL("http://127.0.0.1:0")
	t.Setenv("HOME", t.TempDir())

	ctx := context.Background()
	cache.RestoreLintCache(ctx, client, "", "checksum-a")
	cache.SaveLintCache(ctx, client, "", "checksum-a")
}

// TestGoModCache_RestoreAndSave is a table test covering the go mod cache's
// checksum and job partitioning: a restore under the same checksum and job as
// a prior save round-trips a marker file written into the cache dir; a
// restore under a different checksum or job misses.
func TestGoModCache_RestoreAndSave(t *testing.T) {
	tests := []struct {
		name            string
		saveChecksum    string
		restoreChecksum string
		saveJob         string
		restoreJob      string
		wantRestored    bool
	}{
		{
			name:            "SameChecksum_RoundTrips",
			saveChecksum:    "checksum-a",
			restoreChecksum: "checksum-a",
			saveJob:         "test",
			restoreJob:      "test",
			wantRestored:    true,
		},
		{
			name:            "DifferentChecksum_Misses",
			saveChecksum:    "checksum-a",
			restoreChecksum: "checksum-b",
			saveJob:         "test",
			restoreJob:      "test",
			wantRestored:    false,
		},
		{
			name:            "DifferentJob_Misses",
			saveChecksum:    "checksum-a",
			restoreChecksum: "checksum-a",
			saveJob:         "test-linux",
			restoreJob:      "test-alpine",
			wantRestored:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := sdktest.NewServer(t)
			client := srv.Client()
			ctx := context.Background()

			dir := t.TempDir()
			assert.NilError(t, os.WriteFile(filepath.Join(dir, "marker"), []byte("hit"), 0600))

			t.Setenv("CIRCLE_JOB", tt.saveJob)
			cache.SaveGoModCache(ctx, client, "", tt.saveChecksum, dir)
			assert.NilError(t, os.RemoveAll(dir))

			t.Setenv("CIRCLE_JOB", tt.restoreJob)
			cache.RestoreGoModCache(ctx, client, "", tt.restoreChecksum)

			got, err := os.ReadFile(filepath.Join(dir, "marker")) //nolint:gosec
			if tt.wantRestored {
				assert.NilError(t, err)
				assert.Equal(t, string(got), "hit")
			} else {
				assert.Assert(t, os.IsNotExist(err), "expected no marker file restored under a different checksum or job")
			}
		})
	}
}

// TestBuildCache_KeyIsDistinctFromLintCache verifies the build and lint caches
// don't collide: both keys are built by the same shared helper and differ only
// in their tool component, so a restore of one must not pick up the other's
// entry saved under the same key/checksum.
func TestBuildCache_KeyIsDistinctFromLintCache(t *testing.T) {
	srv := sdktest.NewServer(t)
	client := srv.Client()
	ctx := context.Background()

	t.Setenv("HOME", t.TempDir())
	lintDir, err := cache.LintCacheDir()
	assert.NilError(t, err)
	assert.NilError(t, os.MkdirAll(lintDir, 0750))
	assert.NilError(t, os.WriteFile(filepath.Join(lintDir, "marker"), []byte("hit"), 0600))

	cache.SaveLintCache(ctx, client, "", "checksum-a")
	assert.NilError(t, os.RemoveAll(lintDir))

	cache.RestoreBuildCache(ctx, client, "", "checksum-a")

	_, err = os.Stat(filepath.Join(lintDir, "marker"))
	assert.Assert(t, os.IsNotExist(err), "expected the build cache restore to miss the lint cache entry")
}

// TestGoModCache_CacheErrorIsNonFatal verifies that a client which can't
// reach a server never blocks restore/save.
func TestGoModCache_CacheErrorIsNonFatal(t *testing.T) {
	client := sdk.NewWithBaseURL("http://127.0.0.1:0")
	ctx := context.Background()

	cache.RestoreGoModCache(ctx, client, "", "checksum-a")
	cache.SaveGoModCache(ctx, client, "", "checksum-a", t.TempDir())
}

// TestSaveBuildCache_AtTeardown verifies that saving through
// Client.AtTeardown registers the save rather than running it, and that the
// registered save still round-trips once the job ends.
func TestSaveBuildCache_AtTeardown(t *testing.T) {
	srv := sdktest.NewServer(t)
	client := srv.Client()
	ctx := context.Background()

	dir := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "marker"), []byte("hit"), 0600))

	cache.SaveBuildCache(ctx, client.AtTeardown(), "", "checksum-a", dir)

	// Registered, not saved: nothing is stored until the job ends.
	assert.Equal(t, len(srv.PendingCacheSaves()), 1)

	srv.EndJob(t)
	assert.NilError(t, os.RemoveAll(dir))

	cache.RestoreBuildCache(ctx, client, "", "checksum-a")
	got, err := os.ReadFile(filepath.Join(dir, "marker")) //nolint:gosec
	assert.NilError(t, err)
	assert.Equal(t, string(got), "hit")
}
