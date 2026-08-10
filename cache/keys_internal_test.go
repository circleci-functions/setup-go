package cache

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk"
)

// TestGoBuildRestoreKeys_Fallback asserts the ordered fallback structure that
// prefix matching relies on in real CircleCI. sdktest only exact-matches, so
// this verifies the key shapes directly rather than through a round-trip:
//
//  1. exact — equals the save key (same deps + window)
//  2. same deps, any window — a prefix of tier 1 that still pins the checksum
//  3. any deps — a prefix of tier 2 that drops the checksum, bounded by the arch token
func TestGoBuildRestoreKeys_Fallback(t *testing.T) {
	t.Setenv("CIRCLE_JOB", "test")

	keys := goBuildRestoreKeys("mykey", "sum123")
	assert.Equal(t, len(keys), 3)

	// Tier 1 is the exact save key — guarantees an exact round-trip hits.
	assert.Equal(t, keys[0], goBuildSaveKey("mykey", "sum123"))

	// Each tier is a prefix of the previous, broadening the match.
	assert.Assert(t, strings.HasPrefix(keys[0], keys[1]), "tier 1 should extend tier 2:\n%q\n%q", keys[0], keys[1])
	assert.Assert(t, strings.HasPrefix(keys[1], keys[2]), "tier 2 should extend tier 3:\n%q\n%q", keys[1], keys[2])

	// Tier 2 still pins the checksum; tier 3 drops it (dep-bump fallback).
	assert.Assert(t, strings.Contains(keys[1], "sum123"), "tier 2 should pin the checksum: %q", keys[1])
	assert.Assert(t, !strings.Contains(keys[2], "sum123"), "tier 3 should drop the checksum: %q", keys[2])

	// Tier 3 is the job/arch base truncated on the fixed arch token, so it
	// can't prefix-collide across similarly-named jobs (test vs test-integration).
	assert.Equal(t, keys[2], "v1-mykey-go-build-test-"+sdk.OSArch()+"-")
}
