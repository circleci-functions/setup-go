package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/circleci-functions/toolkit/sdk"
)

// cacheRotation is how often the cache keys rotate, forcing a fresh cache even
// with no other change. Matches the circleci/go orb's
// {{ epoch | round "72h" }} cache key component.
//
// For the build cache this is the only mechanism that ever reclaims space, so
// buildRestoreKeys is careful never to reach across a window. See its comment.
const cacheRotation = 72 * time.Hour

// Saver saves paths under a cache key. Both *sdk.Client and the *sdk.Teardown
// returned by Client.AtTeardown satisfy it, so a caller chooses whether the
// save runs immediately or at the end of the job.
type Saver interface {
	SaveCache(ctx context.Context, key string, paths []string, opts ...sdk.CacheOption) error
}

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
	_, err := client.RestoreCache(ctx, []string{lintCacheKey(key, checksum)},
		sdk.WithStepName("Restoring golangci-lint cache"))
	sdk.LogCacheErr("restore lint", err)
}

// SaveLintCache saves golangci-lint's cache directory to the CircleCI job
// cache.
func SaveLintCache(ctx context.Context, saver Saver, key, checksum string) {
	// TODO: need to be able to set the cache dir other than the default.
	dir, err := LintCacheDir()
	if err != nil {
		sdk.LogCacheErr("save lint", err)
		return
	}

	sdk.LogCacheErr("save lint", saver.SaveCache(ctx, lintCacheKey(key, checksum), []string{dir},
		sdk.WithStepName("Saving golangci-lint cache")))
}

// lintCacheKey mirrors the circleci/go orb's cache key:
//
//	v1-<< parameters.key >>-golangci-lint-{{ .Environment.CIRCLE_JOB }}-{{ arch }}-<< parameters.checksum >>-{{ epoch | round "72h" }}
func lintCacheKey(key, checksum string) string {
	return fmt.Sprintf("%s-%s-%d", cacheKeyBase("golangci-lint", key), checksum, rotationEpoch())
}

// cacheKeyBase is the job- and arch-scoped prefix a tool's cache keys share:
// v1-<key>-<tool>-<CIRCLE_JOB>-<arch>. Ending on the arch token means a
// truncated prefix can't collide across similarly named jobs — a "test" job's
// base is not a prefix of a "test-integration" job's.
func cacheKeyBase(tool, key string) string {
	return fmt.Sprintf("v1-%s-%s-%s-%s", key, tool, os.Getenv("CIRCLE_JOB"), sdk.OSArch())
}

func rotationEpoch() int64 {
	secs := int64(cacheRotation.Seconds())
	return time.Now().Unix() / secs * secs
}

func RestoreGoModCache(ctx context.Context, client *sdk.Client, key, checksum string) {
	_, err := client.RestoreCache(ctx, []string{goModCacheKey(key, checksum)},
		sdk.WithStepName("Restoring go module cache"))
	sdk.LogCacheErr("restore go mod", err)
}

func SaveGoModCache(ctx context.Context, saver Saver, key, checksum, dir string) {
	err := saver.SaveCache(ctx, goModCacheKey(key, checksum), []string{dir},
		sdk.WithStepName("Saving go module cache"))
	sdk.LogCacheErr("save go mod", err)
}

// goModCacheKey is the circleci/go orb's go-mod key plus the rotating epoch the
// orb only applies to its other caches:
//
// v1-<< parameters.key >>-go-mod-{{ arch }}-<< parameters.checksum >>-{{ epoch | round "72h" }}
//
// Without the epoch a caller that passes no checksum has a constant key, and
// since a save is skipped when the key already exists the cache would keep its
// first contents forever.
func goModCacheKey(key, checksum string) string {
	return fmt.Sprintf("v1-%s-go-mod-%s-%s-%d", key, sdk.OSArch(), checksum, rotationEpoch())
}

// RestoreBuildCache restores the go build cache (GOCACHE), trying the ordered
// fallback keys from buildRestoreKeys so a dependency bump warm-starts from a
// near miss instead of compiling from cold.
func RestoreBuildCache(ctx context.Context, client *sdk.Client, key, checksum string) {
	_, err := client.RestoreCache(ctx, buildRestoreKeys(key, checksum),
		sdk.WithStepName("Restoring go build cache"))
	sdk.LogCacheErr("restore go build", err)
}

func SaveBuildCache(ctx context.Context, saver Saver, key, checksum, dir string) {
	err := saver.SaveCache(ctx, buildCacheKey(key, checksum), []string{dir},
		sdk.WithStepName("Saving go build cache"))
	sdk.LogCacheErr("save go build", err)
}

// buildCacheKey is the key a build saves under. It carries the same components
// as the circleci/go orb's go-build key, but with the rotation epoch ahead of
// the checksum rather than after it:
//
//	v1-<key>-go-build-<CIRCLE_JOB>-<arch>-<rotating epoch>-<checksum>
//
// The order matters because prefix matching can only drop trailing components.
// With the epoch last, a key broad enough to match any checksum is also broad
// enough to match any window, which buildRestoreKeys must not allow.
func buildCacheKey(key, checksum string) string {
	return buildWindowPrefix(key) + checksum
}

// buildWindowPrefix matches every build cache key saved in the current
// rotation window: v1-<key>-go-build-<CIRCLE_JOB>-<arch>-<epoch>-. The epoch
// is a fixed-width unix timestamp and the prefix ends on the separator that
// follows it, so it cannot match a neighbouring window's keys.
func buildWindowPrefix(key string) string {
	return fmt.Sprintf("%s-%d-", cacheKeyBase("go-build", key), rotationEpoch())
}

// buildRestoreKeys returns the keys a restore tries in order. CircleCI matches
// each key as a prefix and takes the most recent entry matching the first key
// that hits at all:
//
//  1. exact — same deps, same rotation window
//  2. same window, any deps — after a dependency bump only the changed
//     modules recompile
//
// Entry 1 is the save key, so an exact round-trip still hits on a cache that
// only matches exactly.
//
// Neither entry can reach an earlier window, and that bound is what keeps the
// cache from growing without limit. A restore lands its entry on disk and the
// teardown save writes the whole directory back, so a tier that matched an
// earlier window would carry that window's contents forward into the next one,
// forever. Nothing downstream would reclaim them: GOCACHE self-trims by mtime
// (cmd/go/internal/cache.Trim drops entries unused for five days), but a
// restored file's mtime is its restore time rather than its original one, so
// no entry in a restored cache ever looks old enough to collect. Rotation is
// the only thing that shrinks this cache, and it only works if prefix
// matching stops at the window.
func buildRestoreKeys(key, checksum string) []string {
	return []string{
		buildCacheKey(key, checksum),
		buildWindowPrefix(key),
	}
}
