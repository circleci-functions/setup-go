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
	assert.Equal(t, len(keys), 2)

	// Tier 1 is the save key, so an exact restore hits.
	assert.Equal(t, keys[0], buildCacheKey("mykey", "sum123"))

	// Tier 2 is a prefix of tier 1, broadening the match.
	assert.Assert(t, strings.HasPrefix(keys[0], keys[1]), "tier 1 should extend tier 2:\n%q\n%q", keys[0], keys[1])

	// Tier 2 drops the checksum so a dependency bump falls back to the
	// previous deps rather than to nothing.
	assert.Assert(t, !strings.Contains(keys[1], "sum123"), "tier 2 should drop the checksum: %q", keys[1])

	// Tier 2 still pins the rotation window. Dropping the checksum must not
	// also drop the window, or a restore would carry an earlier window's
	// contents forward and nothing would ever reclaim them.
	assert.Equal(t, keys[1], fmt.Sprintf("v1-mykey-go-build-test-%s-%d-", sdk.OSArch(), rotationEpoch()))
}

// TestBuildRestoreKeys_CannotReachAnotherWindow is the property the key
// ordering exists for: no restore key may match a key from a different
// rotation window, in either direction.
func TestBuildRestoreKeys_CannotReachAnotherWindow(t *testing.T) {
	t.Setenv("CIRCLE_JOB", "test")

	base := cacheKeyBase("go-build", "mykey")
	// The neighbouring windows, and one whose epoch shares a digit prefix with
	// the current one — the case a fixed-width epoch plus a trailing separator
	// has to rule out.
	secs := int64(cacheRotation.Seconds())
	others := []string{
		fmt.Sprintf("%s-%d-sum123", base, rotationEpoch()-secs),
		fmt.Sprintf("%s-%d-sum123", base, rotationEpoch()+secs),
		fmt.Sprintf("%s-%d-sum123", base, rotationEpoch()/10),
	}

	for _, key := range buildRestoreKeys("mykey", "sum123") {
		for _, other := range others {
			assert.Assert(t, !strings.HasPrefix(other, key),
				"restore key must not match another window's entry:\nkey:   %q\nentry: %q", key, other)
		}
	}
}

// TestRestoreBuildCache_FallsBackThroughTiers round-trips a saved entry against
// each tier of buildRestoreKeys, covering both what a restore is meant to reach
// — a near miss inside the current window, so a dependency bump warm-starts
// instead of compiling from cold — and what it must not reach.
func TestRestoreBuildCache_FallsBackThroughTiers(t *testing.T) {
	const key = "mykey"

	tests := []struct {
		name string
		// savedKey builds the key an earlier job saved under.
		savedKey func() string
		wantHit  bool
	}{
		{
			// Tier 1: same deps, same rotation window.
			name:     "ExactKey",
			savedKey: func() string { return buildCacheKey(key, "sum-current") },
			wantHit:  true,
		},
		{
			// Tier 2: different deps, same window. After a dependency bump only
			// the changed modules recompile, rather than the whole project.
			name:     "DifferentChecksumSameWindow",
			savedKey: func() string { return buildWindowPrefix(key) + "sum-previous" },
			wantHit:  true,
		},
		{
			// The rotation is the only thing that ever reclaims space in this
			// cache, because a restored file's mtime is its restore time and so
			// GOCACHE's own five-day trim never collects anything a restore put
			// there. A hit here would carry the previous window's contents
			// forward and the cache would grow without limit.
			name: "EarlierRotationWindow",
			savedKey: func() string {
				secs := int64(cacheRotation.Seconds())
				return fmt.Sprintf("%s-%d-sum-current", cacheKeyBase("go-build", key), rotationEpoch()-secs)
			},
		},
		{
			// cacheKeyBase ends on the arch token, so a job whose name merely
			// extends this one's must not have its entries picked up.
			name: "AnotherJobsCache",
			savedKey: func() string {
				return fmt.Sprintf("v1-%s-go-build-test-integration-%s-%d-sum-current", key, sdk.OSArch(), rotationEpoch())
			},
		},
		{
			// The lint cache shares the key layout and differs only in its tool
			// component, so no tier may reach it.
			name:     "AnotherToolsCache",
			savedKey: func() string { return lintCacheKey(key, "sum-current") },
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
			assert.NilError(t, client.SaveCache(ctx, tt.savedKey(), []string{cached}))
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

	// Saved later, and matched only by the broader tier.
	broader := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(broader, "marker"), []byte("broader"), 0600))
	assert.NilError(t, client.SaveCache(ctx, buildWindowPrefix("mykey")+"sum-previous", []string{broader}))

	assert.NilError(t, os.RemoveAll(exact))
	assert.NilError(t, os.RemoveAll(broader))

	RestoreBuildCache(ctx, client, "mykey", "sum-current")

	got, err := os.ReadFile(filepath.Join(exact, "marker")) //nolint:gosec
	assert.NilError(t, err, "expected the exact key to win")
	assert.Equal(t, string(got), "exact")

	_, err = os.Stat(filepath.Join(broader, "marker"))
	assert.Assert(t, os.IsNotExist(err), "a broader tier must not be consulted once the exact key matched")
}

// TestRestoreGoModCache_OnlyMatchesTheExactKey pins the module cache to its
// exact key. A restore that fell back to another checksum or rotation window
// would have the teardown save re-upload those stale modules alongside the new
// ones, so the cache would only ever grow.
func TestRestoreGoModCache_OnlyMatchesTheExactKey(t *testing.T) {
	const key = "mykey"
	secs := int64(cacheRotation.Seconds())

	tests := []struct {
		name     string
		savedKey func() string
		wantHit  bool
	}{
		{
			name:     "ExactKey",
			savedKey: func() string { return goModCacheKey(key, "sum-current") },
			wantHit:  true,
		},
		{
			name: "DifferentChecksumSameWindow",
			savedKey: func() string {
				return fmt.Sprintf("%s-sum-previous-%d", cacheKeyBase("go-mod", key), rotationEpoch())
			},
		},
		{
			name: "EarlierRotationWindow",
			savedKey: func() string {
				return fmt.Sprintf("%s-sum-current-%d", cacheKeyBase("go-mod", key), rotationEpoch()-secs)
			},
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
			assert.NilError(t, client.SaveCache(ctx, tt.savedKey(), []string{cached}))
			assert.NilError(t, os.RemoveAll(cached))

			RestoreGoModCache(ctx, client, key, "sum-current")

			_, err := os.Stat(filepath.Join(cached, "marker"))
			if tt.wantHit {
				assert.NilError(t, err, "expected the exact key to restore")
				return
			}
			assert.Assert(t, os.IsNotExist(err), "expected no restore from another checksum or window")
		})
	}
}
