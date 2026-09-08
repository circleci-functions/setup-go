// Command setup-go installs a Go toolchain and links it onto PATH so
// subsequent steps in the same job pick it up automatically, whatever shell
// they run in.
//
// Supported platforms: linux, darwin, windows.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"

	"github.com/circleci-functions/toolkit/functionyaml"
	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-go/cache"
	"github.com/circleci-functions/setup-go/cgoalpine"
	"github.com/circleci-functions/setup-go/goenv"
	"github.com/circleci-functions/setup-go/goversion"
	"github.com/circleci-functions/setup-go/install"
	"github.com/circleci-functions/setup-go/pathlink"
)

type cli struct {
	Version     string `help:"Go version spec (e.g. 1.22, 1.22.3, stable, latest)." default:"stable"`
	VersionFile string `name:"version-file" help:"Path to a go.mod or go.work file to read the Go version from. Overrides --version when set." default:""`
	DetectOnly  bool   `help:"Detect and export env using the Go already on PATH, without installing."`
	InstallDir  string `help:"Base directory for Go toolchain installs." default:"~/.cache/setup-go"`
	APIURL      string `name:"api-url" help:"Override go.dev release API base URL (also overrides tarball download base)." hidden:"" default:""`
	BinDir      string `name:"bin-dir" help:"Link go/gofmt into this directory instead of discovering one on PATH." hidden:"" default:""`
	CacheNonce  string `name:"cache-nonce" help:"Partition the toolchain cache, so a test run cannot restore an earlier run's toolchain." hidden:"" default:""`

	Cache             bool   `help:"Use the job cache: restore the selected caches up front and save them at the end of the job. Set to false to disable all caching, including the toolchain cache." default:"true"`
	GoModCache        bool   `name:"go-mod-cache" help:"Include the go module cache (GOMODCACHE). Ignored when --cache=false." default:"true"`
	BuildCache        bool   `name:"build-cache" help:"Include the go build cache (GOCACHE). Ignored when --cache=false." default:"true"`
	GolangCILintCache bool   `name:"golangci-lint-cache" help:"Include golangci-lint's cache. Ignored when --cache=false." default:"false"`
	Key               string `help:"Optional cache key component to partition the job caches."`
	Checksum          string `help:"Optional checksum to partition the job caches. Different checksums force a cache miss."`
}

func (c *cli) Run(ctx context.Context, client *sdk.Client) error {
	spec := c.Version
	if c.VersionFile != "" {
		v, err := goversion.ReadVersionFile(c.VersionFile)
		if err != nil {
			return fmt.Errorf("read version file %q: %w", c.VersionFile, err)
		}
		spec = v
	}

	ver, err := goversion.ResolveVersion(ctx, c.APIURL, spec)
	if err != nil {
		return fmt.Errorf("resolve version: %w", err)
	}
	fmt.Fprintf(os.Stderr, "setup-go: resolved %q → %s\n", spec, ver.Name)

	goroot, err := install.EnsureCached(ctx, client, *ver, install.EnsureCachedOptions{
		InstallDir: expandHome(c.InstallDir),
		DetectOnly: c.DetectOnly,
		NoCache:    !c.Cache,
		CacheNonce: c.CacheNonce,
	})
	if err != nil {
		return err
	}

	// The toolchain just installed, not whatever is on PATH: until pathlink
	// runs, `go` still resolves to the one the image shipped, if any.
	goBinary := filepath.Join(goroot, "bin", install.GoBinary())
	env, err := goenv.LoadFrom(ctx, goBinary)
	if err != nil {
		return err
	}

	linked, err := pathlink.Ensure(pathlink.Options{
		GoRoot: goroot,
		GoPath: env.GOPATH,
		BinDir: c.BinDir,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "setup-go: go and gofmt resolve from %s\n", linked.Dir)

	_ = cgoalpine.InstallToolchain(ctx, cgoalpine.DefaultOSReleasePath)

	return c.setupJobCaches(ctx, client, env)
}

// setupJobCaches restores the selected caches and registers their saves for the
// end of the job, so the steps in between need no cache steps of their own.
func (c *cli) setupJobCaches(ctx context.Context, client *sdk.Client, env *goenv.GoEnv) error {
	if !c.Cache {
		return nil
	}

	if c.GoModCache {
		cache.RestoreGoModCache(ctx, client, c.Key, c.Checksum)
		cache.SaveGoModCache(ctx, client.AtTeardown(), c.Key, c.Checksum, env.GOMODCACHE)
	}

	if c.BuildCache {
		cache.RestoreBuildCache(ctx, client, c.Key, c.Checksum)
		cache.SaveBuildCache(ctx, client.AtTeardown(), c.Key, c.Checksum, env.GOCACHE)
	}

	if c.GolangCILintCache {
		cache.RestoreLintCache(ctx, client, c.Key, c.Checksum)
		cache.SaveLintCache(ctx, client.AtTeardown(), c.Key, c.Checksum)
	}

	return nil
}

// expandHome resolves a leading ~ against the home directory, which kong leaves
// alone. The tag holds ~/ so function.yaml publishes a readable default.
func expandHome(path string) string {
	if path == "" || path[0] != '~' || (len(path) > 1 && !os.IsPathSeparator(path[1])) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

func main() {
	sdk.Run(run)
}

func run(ctx context.Context, client *sdk.Client) error {
	var flags cli
	kCtx := kong.Parse(&flags,
		kong.Name("setup-go"),
		kong.Description("Install a Go toolchain and link it onto PATH for later steps.\n\nExample:\n  circleci run setup-go@<version> -- --version 1.22"),
		kong.UsageOnError(),
		functionyaml.Embed(),
	)
	kCtx.BindTo(ctx, (*context.Context)(nil))
	kCtx.Bind(client)
	return kCtx.Run()
}
