package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk"
	"github.com/circleci-functions/toolkit/sdk/sdktest"
)

// TestBuildRestoreKeys asserts the prefix structure a real CircleCI restore
// relies on. The round-trip through each tier is covered by
// TestRestoreBuildCache_FallsBackThroughTiers.
func TestBuildRestoreKeys(t *testing.T) {
	t.Setenv("CIRCLE_JOB", "test")

	keys := buildRestoreKeys("mykey", "sum123")
	assert.Equal(t, len(keys), 3)

	// Tier 1 is the save key, so an exact restore hits.
	assert.Equal(t, keys[0], buildCacheKey("mykey", "sum123"))

	// Each tier is a prefix of the one before it, broadening the match.
	assert.Assert(t, strings.HasPrefix(keys[0], keys[1]), "tier 1 should extend tier 2:\n%q\n%q", keys[0], keys[1])
	assert.Assert(t, strings.HasPrefix(keys[1], keys[2]), "tier 2 should extend tier 3:\n%q\n%q", keys[1], keys[2])

	// Tier 2 still pins the checksum; tier 3 drops it so a dependency bump
	// falls back to the previous deps rather than to nothing.
	assert.Assert(t, strings.Contains(keys[1], "sum123"), "tier 2 should pin the checksum: %q", keys[1])
	assert.Assert(t, !strings.Contains(keys[2], "sum123"), "tier 3 should drop the checksum: %q", keys[2])

	// Tier 3 ends on the arch token, so it can't prefix-collide with a job
	// named "test-integration".
	assert.Equal(t, keys[2], "v1-mykey-go-build-test-"+sdk.OSArch()+"-")
}

// TestRestoreBuildCache_FallsBackThroughTiers round-trips each tier of
// buildRestoreKeys through the cache, which is what the tiers exist for: a
// restore matches a key as a prefix, so a near miss warm-starts instead of
// compiling from cold.
func TestRestoreBuildCache_FallsBackThroughTiers(t *testing.T) {
	const key = "mykey"

	tests := []struct {
		name string
		// savedKey builds the key an earlier job saved under, given the base
		// this job's restore tiers are built from.
		savedKey func(base string) string
		wantHit  bool
	}{
		{
			// Tier 1: same deps, same rotation window.
			name:     "ExactKey",
			savedKey: func(string) string { return buildCacheKey(key, "sum-current") },
			wantHit:  true,
		},
		{
			// Tier 2: same deps, an earlier rotation window. Only a prefix
			// match can bridge a rotation boundary.
			name:     "EarlierRotationWindow",
			savedKey: func(base string) string { return base + "-sum-current-1700000000" },
			wantHit:  true,
		},
		{
			// Tier 3: different deps. After a dependency bump only the changed
			// modules recompile, rather than the whole project.
			name:     "DifferentChecksum",
			savedKey: func(base string) string { return base + "-sum-previous-1700000000" },
			wantHit:  true,
		},
		{
			// Tier 3 ends on the arch token, so a job whose name merely extends
			// this one's must not have its entries picked up. This is the
			// collision cacheKeyBase's trailing arch token exists to prevent.
			name: "AnotherJobsCache",
			savedKey: func(string) string {
				return fmt.Sprintf("v1-%s-go-build-test-integration-%s-sum-current-1700000000", key, sdk.OSArch())
			},
		},
		{
			// The lint cache shares the key layout and differs only in its tool
			// component, so no tier may reach it.
			name:     "AnotherToolsCache",
			savedKey: func(string) string { return lintCacheKey(key, "sum-current") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CIRCLE_JOB", "test")

			srv := sdktest.NewServer(t)
			client := srv.Client()
			ctx := context.Background()

			cached := t.TempDir()
			assert.NilError(t, os.WriteFile(filepath.Join(cached, "marker"), []byte("hit"), 0600))
			assert.NilError(t, client.SaveCache(ctx, tt.savedKey(cacheKeyBase("go-build", key)), []string{cached}))
			assert.NilError(t, os.RemoveAll(cached))

			RestoreBuildCache(ctx, client, key, "sum-current")

			_, err := os.Stat(filepath.Join(cached, "marker"))
			if tt.wantHit {
				assert.NilError(t, err, "expected a restore to fall back to the saved entry")
				return
			}
			assert.Assert(t, os.IsNotExist(err), "expected no tier to match the saved entry")
		})
	}
}

// A restore stops at the first tier that matches, so a more recent entry under
// a broader tier must not displace an exact hit.
func TestRestoreBuildCache_PrefersTheExactKey(t *testing.T) {
	t.Setenv("CIRCLE_JOB", "test")

	srv := sdktest.NewServer(t)
	client := srv.Client()
	ctx := context.Background()

	exact := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(exact, "marker"), []byte("exact"), 0600))
	assert.NilError(t, client.SaveCache(ctx, buildCacheKey("mykey", "sum-current"), []string{exact}))

	// Saved later, and matched only by the broader tiers.
	broader := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(broader, "marker"), []byte("broader"), 0600))
	assert.NilError(t, client.SaveCache(ctx, cacheKeyBase("go-build", "mykey")+"-sum-previous-1700000000", []string{broader}))

	assert.NilError(t, os.RemoveAll(exact))
	assert.NilError(t, os.RemoveAll(broader))

	RestoreBuildCache(ctx, client, "mykey", "sum-current")

	got, err := os.ReadFile(filepath.Join(exact, "marker")) //nolint:gosec
	assert.NilError(t, err, "expected the exact key to win")
	assert.Equal(t, string(got), "exact")

	_, err = os.Stat(filepath.Join(broader, "marker"))
	assert.Assert(t, os.IsNotExist(err), "a broader tier must not be consulted once the exact key matched")
}
