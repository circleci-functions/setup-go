package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/circleci-functions/toolkit/sdk"
)

// lintCacheRotation is how often the lint cache key rotates, forcing a fresh
// cache even with no other change. Matches the circleci/go orb's
// {{ epoch | round "72h" }} cache key component.
const lintCacheRotation = 72 * time.Hour

// LintCacheDir returns golangci-lint's cache directory. It currently always
// returns the tool's own default, ~/.cache/golangci-lint.
func LintCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".cache", "golangci-lint"), nil
}

// RestoreLintCache restores golangci-lint's cache directory from the
// CircleCI job cache under the key computed by lintCacheKey. It is entirely
// best-effort: a cache-call failure is logged to stderr and never blocks the
// test run.
func RestoreLintCache(ctx context.Context, client *sdk.Client, key, checksum string) {
	_, err := client.RestoreCache(ctx, []string{lintCacheKey(key, checksum)})
	sdk.LogCacheErr("restore lint", err)
}

// SaveLintCache saves golangci-lint's cache directory to the CircleCI job
// cache.
func SaveLintCache(ctx context.Context, client *sdk.Client, key, checksum string) {
	// TODO: need to be able to set the cache dir other than the default.
	dir, err := LintCacheDir()
	if err != nil {
		sdk.LogCacheErr("save lint", err)
		return
	}

	sdk.LogCacheErr("save lint", client.SaveCache(ctx, lintCacheKey(key, checksum), []string{dir}))
}

// lintCacheKey mirrors the circleci/go orb's cache key:
//
//	v1-<< parameters.key >>-golangci-lint-{{ .Environment.CIRCLE_JOB }}-{{ arch }}-<< parameters.checksum >>-{{ epoch | round "72h" }}
func lintCacheKey(key, checksum string) string {
	rotation := time.Now().Unix() / int64(lintCacheRotation.Seconds()) * int64(lintCacheRotation.Seconds())
	return fmt.Sprintf("v1-%s-golangci-lint-%s-%s-%s-%d", key, os.Getenv("CIRCLE_JOB"), sdk.OSArch(), checksum, rotation)
}

func RestoreGoModCache(ctx context.Context, client *sdk.Client, key, checksum string) {
	_, err := client.RestoreCache(ctx, []string{goModCacheKey(key, checksum)})
	sdk.LogCacheErr("restore go mod", err)
}

func SaveGoModCache(ctx context.Context, client *sdk.Client, key, checksum, dir string) {
	err := client.SaveCache(ctx, goModCacheKey(key, checksum), []string{dir})
	sdk.LogCacheErr("save go mod", err)
}

// goModCacheKey mirrors the circleci/go orb's cache key:
//
// v1-<< parameters.key >>-go-mod-{{ arch }}-<< parameters.checksum >>
func goModCacheKey(key, checksum string) string {
	return fmt.Sprintf("v1-%s-go-mod-%s-%s", key, sdk.OSArch(), checksum)
}

// buildCacheRotation is how often the go build cache key's rotation window
// advances. The dependency portion of GOCACHE — the bulk of what's compiled —
// is captured deterministically by the go.sum checksum in the key. The
// rotation is a second axis that absorbs drift in our *own* source, which we
// can't cheaply checksum: each window saves a fresh cache, bounding staleness.
const buildCacheRotation = 72 * time.Hour

// RestoreGoBuildCache restores the go build cache (GOCACHE) from the CircleCI
// job cache, trying the ordered fallback keys from goBuildRestoreKeys so a new
// rotation window or a dependency bump restores a warm cache rather than going
// cold. Best-effort: a cache-call failure is logged and never blocks the build.
func RestoreGoBuildCache(ctx context.Context, client *sdk.Client, key, checksum string) {
	_, err := client.RestoreCache(ctx, goBuildRestoreKeys(key, checksum))
	sdk.LogCacheErr("restore go build", err)
}

// SaveGoBuildCache saves dir (GOCACHE) under the exact key for the current
// window and go.sum checksum. The key is write-once; the next build in the
// same window is a no-op save and restores this one via the exact fallback.
func SaveGoBuildCache(ctx context.Context, client *sdk.Client, key, checksum, dir string) {
	err := client.SaveCache(ctx, goBuildSaveKey(key, checksum), []string{dir})
	sdk.LogCacheErr("save go build", err)
}

// goBuildCacheBase is the job- and arch-scoped prefix every build cache key
// shares: v1-<key>-go-build-<CIRCLE_JOB>-<arch>. Job precedes arch so the
// broadest restore fallback (goBuildRestoreKeys' last entry) truncates on the
// fixed arch token — a job named "test" can't prefix-collide with
// "test-integration".
func goBuildCacheBase(key string) string {
	return fmt.Sprintf("v1-%s-go-build-%s-%s", key, os.Getenv("CIRCLE_JOB"), sdk.OSArch())
}

// goBuildSaveKey is the exact key a build saves under: base, go.sum checksum,
// and the current rotation window.
func goBuildSaveKey(key, checksum string) string {
	return fmt.Sprintf("%s-%s-%d", goBuildCacheBase(key), checksum, buildRotationEpoch())
}

// goBuildRestoreKeys returns the ordered fallback list a restore tries. Real
// CircleCI restore does prefix matching and returns the most recent entry that
// matches the first key with any hit:
//
//  1. exact — same deps, same window (also the same-commit retry case)
//  2. same deps, any window — bridges the rotation boundary
//  3. any deps — after a dep bump, only the changed modules recompile; the rest stay warm
//
// The save key equals entry 1, so an exact round-trip hits even on a cache
// server that only exact-matches.
func goBuildRestoreKeys(key, checksum string) []string {
	base := goBuildCacheBase(key)
	return []string{
		fmt.Sprintf("%s-%s-%d", base, checksum, buildRotationEpoch()),
		fmt.Sprintf("%s-%s-", base, checksum),
		base + "-",
	}
}

func buildRotationEpoch() int64 {
	secs := int64(buildCacheRotation.Seconds())
	return time.Now().Unix() / secs * secs
}
