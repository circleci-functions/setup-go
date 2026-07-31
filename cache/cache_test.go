package cache_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk"
	"github.com/circleci-functions/toolkit/sdk/sdktest"

	"github.com/circleci-functions/setup-go/cache"
)

func TestLintCacheDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := cache.LintCacheDir()
	assert.NilError(t, err)
	assert.Equal(t, dir, filepath.Join(home, ".cache", "golangci-lint"))
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
// checksum partitioning: a restore under the same checksum as a prior save
// round-trips a marker file written into the cache dir; a restore under a
// different checksum misses.
func TestGoModCache_RestoreAndSave(t *testing.T) {
	tests := []struct {
		name            string
		saveChecksum    string
		restoreChecksum string
		wantRestored    bool
	}{
		{
			name:            "SameChecksum_RoundTrips",
			saveChecksum:    "checksum-a",
			restoreChecksum: "checksum-a",
			wantRestored:    true,
		},
		{
			name:            "DifferentChecksum_Misses",
			saveChecksum:    "checksum-a",
			restoreChecksum: "checksum-b",
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

			cache.SaveGoModCache(ctx, client, "", tt.saveChecksum, dir)
			assert.NilError(t, os.RemoveAll(dir))

			cache.RestoreGoModCache(ctx, client, "", tt.restoreChecksum)

			got, err := os.ReadFile(filepath.Join(dir, "marker")) //nolint:gosec
			if tt.wantRestored {
				assert.NilError(t, err)
				assert.Equal(t, string(got), "hit")
			} else {
				assert.Assert(t, os.IsNotExist(err), "expected no marker file restored under a different checksum")
			}
		})
	}
}

// TestGoModCache_CacheErrorIsNonFatal verifies that a client which can't
// reach a server never blocks restore/save.
func TestGoModCache_CacheErrorIsNonFatal(t *testing.T) {
	client := sdk.NewWithBaseURL("http://127.0.0.1:0")
	ctx := context.Background()

	cache.RestoreGoModCache(ctx, client, "", "checksum-a")
	cache.SaveGoModCache(ctx, client, "", "checksum-a", t.TempDir())
}
