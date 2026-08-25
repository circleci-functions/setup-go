package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/circleci-functions/toolkit/sdk"
)

// cacheRotation is how often the lint and build cache keys rotate, forcing a
// fresh cache even with no other change. Matches the circleci/go orb's
// {{ epoch | round "72h" }} cache key component.
const cacheRotation = 72 * time.Hour

// Saver saves paths under a cache key. Both *sdk.Client and the *sdk.Teardown
// returned by Client.AtTeardown satisfy it, so a caller chooses whether the
// save runs immediately or at the end of the job.
type Saver interface {
	SaveCache(ctx context.Context, key string, paths []string) error
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
	_, err := client.RestoreCache(ctx, []string{lintCacheKey(key, checksum)})
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

	sdk.LogCacheErr("save lint", saver.SaveCache(ctx, lintCacheKey(key, checksum), []string{dir}))
}

// lintCacheKey mirrors the circleci/go orb's cache key:
//
//	v1-<< parameters.key >>-golangci-lint-{{ .Environment.CIRCLE_JOB }}-{{ arch }}-<< parameters.checksum >>-{{ epoch | round "72h" }}
func lintCacheKey(key, checksum string) string {
	return rotatingCacheKey("golangci-lint", key, checksum)
}

// rotatingCacheKey builds a job- and arch-scoped cache key that rotates every
// cacheRotation, mirroring the circleci/go orb's key layout:
//
//	v1-<key>-<tool>-<CIRCLE_JOB>-<arch>-<checksum>-<rotating epoch>
func rotatingCacheKey(tool, key, checksum string) string {
	return fmt.Sprintf("%s-%s-%d", cacheKeyBase(tool, key), checksum, rotationEpoch())
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
	_, err := client.RestoreCache(ctx, []string{goModCacheKey(key, checksum)})
	sdk.LogCacheErr("restore go mod", err)
}

func SaveGoModCache(ctx context.Context, saver Saver, key, checksum, dir string) {
	err := saver.SaveCache(ctx, goModCacheKey(key, checksum), []string{dir})
	sdk.LogCacheErr("save go mod", err)
}

// goModCacheKey mirrors the circleci/go orb's cache key:
//
// v1-<< parameters.key >>-go-mod-{{ arch }}-<< parameters.checksum >>
func goModCacheKey(key, checksum string) string {
	return fmt.Sprintf("v1-%s-go-mod-%s-%s", key, sdk.OSArch(), checksum)
}

// RestoreBuildCache restores the go build cache (GOCACHE), trying the ordered
// fallback keys from buildRestoreKeys so a rotation boundary or a dependency
// bump warm-starts from a near miss instead of compiling from cold.
func RestoreBuildCache(ctx context.Context, client *sdk.Client, key, checksum string) {
	_, err := client.RestoreCache(ctx, buildRestoreKeys(key, checksum))
	sdk.LogCacheErr("restore go build", err)
}

func SaveBuildCache(ctx context.Context, saver Saver, key, checksum, dir string) {
	err := saver.SaveCache(ctx, buildCacheKey(key, checksum), []string{dir})
	sdk.LogCacheErr("save go build", err)
}

// buildCacheKey mirrors the circleci/go orb's cache key:
//
//	v1-<< parameters.key >>-go-build-{{ .Environment.CIRCLE_JOB }}-{{ arch }}-<< parameters.checksum >>-{{ epoch | round "72h" }}
func buildCacheKey(key, checksum string) string {
	return rotatingCacheKey("go-build", key, checksum)
}

// buildRestoreKeys returns the keys a restore tries in order. CircleCI matches
// each key as a prefix and takes the most recent entry matching the first key
// that hits at all, so the list broadens as it goes:
//
//  1. exact — same deps, same rotation window
//  2. same deps, any window — bridges a rotation boundary
//  3. any deps — after a dependency bump only the changed modules recompile
//
// Entry 1 is the save key, so an exact round-trip still hits on a cache that
// only matches exactly. Restoring an older cache is safe: GOCACHE is
// content-addressed, so entries that no longer apply simply go unused.
func buildRestoreKeys(key, checksum string) []string {
	base := cacheKeyBase("go-build", key)
	return []string{
		buildCacheKey(key, checksum),
		fmt.Sprintf("%s-%s-", base, checksum),
		base + "-",
	}
}
