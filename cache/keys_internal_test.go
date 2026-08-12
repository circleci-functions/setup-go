package cache

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk"
)

// TestBuildRestoreKeys asserts the prefix structure a real CircleCI restore
// relies on. sdktest only matches keys exactly, so the tiers are checked here
// directly rather than through a round-trip.
func TestBuildRestoreKeys(t *testing.T) {
	t.Setenv("CIRCLE_JOB", "test")

	keys := buildRestoreKeys("mykey", "sum123")
	assert.Equal(t, len(keys), 3)

	// Tier 1 is the save key, so an exact round-trip hits even without prefix
	// matching.
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
