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
