package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-go/goenv"
)

// cacheRotation is how often the lint and build cache keys rotate, forcing a
// fresh cache even with no other change. Matches the circleci/go orb's
// {{ epoch | round "72h" }} cache key component.
const cacheRotation = 72 * time.Hour

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
	return rotatingCacheKey("golangci-lint", key, checksum)
}

// rotatingCacheKey builds a job- and arch-scoped cache key that rotates every
// cacheRotation, mirroring the circleci/go orb's key layout:
//
//	v1-<key>-<tool>-<CIRCLE_JOB>-<arch>-<checksum>-<rotating epoch>
func rotatingCacheKey(tool, key, checksum string) string {
	rotation := time.Now().Unix() / int64(cacheRotation.Seconds()) * int64(cacheRotation.Seconds())
	return fmt.Sprintf("v1-%s-%s-%s-%s-%s-%d", key, tool, os.Getenv("CIRCLE_JOB"), sdk.OSArch(), checksum, rotation)
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

// BuildCacheDir returns the go build cache directory as reported by
// `go env GOCACHE`, rather than assuming ~/.cache/go-build (which is wrong on
// darwin).
func BuildCacheDir() (string, error) {
	e, err := goenv.Load()
	if err != nil {
		return "", err
	}
	if e.GOCACHE == "" {
		return "", fmt.Errorf("go env reported an empty GOCACHE")
	}
	return e.GOCACHE, nil
}

func RestoreBuildCache(ctx context.Context, client *sdk.Client, key, checksum string) {
	_, err := client.RestoreCache(ctx, []string{buildCacheKey(key, checksum)})
	sdk.LogCacheErr("restore go build", err)
}

func SaveBuildCache(ctx context.Context, client *sdk.Client, key, checksum, dir string) {
	err := client.SaveCache(ctx, buildCacheKey(key, checksum), []string{dir})
	sdk.LogCacheErr("save go build", err)
}

// buildCacheKey mirrors the circleci/go orb's cache key:
//
//	v1-<< parameters.key >>-go-build-{{ .Environment.CIRCLE_JOB }}-{{ arch }}-<< parameters.checksum >>-{{ epoch | round "72h" }}
func buildCacheKey(key, checksum string) string {
	return rotatingCacheKey("go-build", key, checksum)
}
