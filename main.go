// Command setup-go installs a Go toolchain and links it onto PATH, so later
// steps in the job pick it up whatever shell they run in.
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
	"github.com/circleci-functions/setup-go/goroot"
	"github.com/circleci-functions/setup-go/goversion"
	"github.com/circleci-functions/setup-go/install"
)

type cli struct {
	Version     string `help:"Go version spec (e.g. 1.22, 1.22.3, stable, latest)." default:"stable" example:"1.22"`
	VersionFile string `name:"version-file" help:"Path to a go.mod or go.work file to read the Go version from: the newest patch its go directive allows, or its toolchain line if higher. Overrides --version when set." default:""`
	DetectOnly  bool   `help:"Use the Go already on PATH, without installing. --version and --version-file are ignored."`
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
	gorootDir, err := c.toolchain(ctx, client)
	if err != nil {
		return err
	}

	// The toolchain just installed: until it is linked, `go` still resolves to
	// whatever the image shipped.
	goBinary := filepath.Join(gorootDir, "bin", install.GoBinary())
	env, err := goenv.LoadFrom(ctx, goBinary)
	if err != nil {
		return err
	}

	linked, err := goroot.Link(goroot.Options{
		GoRoot: gorootDir,
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

// toolchain returns the GOROOT of the toolchain to set up. Under --detect-only
// that is the one already on PATH, so no version spec is resolved at all.
func (c *cli) toolchain(ctx context.Context, client *sdk.Client) (string, error) {
	if c.DetectOnly {
		return install.Detect()
	}

	ver, err := c.resolve(ctx)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "setup-go: resolved %q → %s\n", ver.Spec, ver.Name)

	return install.EnsureCached(ctx, client, *ver, install.EnsureCachedOptions{
		InstallDir: expandHome(c.InstallDir),
		NoCache:    !c.Cache,
		CacheNonce: c.CacheNonce,
	})
}

// resolve picks the release to install: the one --version-file asks for when
// it is set, and --version otherwise.
func (c *cli) resolve(ctx context.Context) (*goversion.Version, error) {
	var ver *goversion.Version
	var err error
	if c.VersionFile == "" {
		ver, err = goversion.ResolveVersion(ctx, c.APIURL, c.Version)
	} else {
		req, readErr := goversion.ReadVersionFile(c.VersionFile)
		if readErr != nil {
			return nil, fmt.Errorf("read version file %q: %w", c.VersionFile, readErr)
		}
		ver, err = goversion.ResolveRequirement(ctx, c.APIURL, req)
	}
	if err != nil {
		return nil, fmt.Errorf("resolve version: %w", err)
	}
	return ver, nil
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
		kong.Description("Install a Go toolchain and link it onto PATH for later steps."),
		kong.UsageOnError(),
		functionyaml.Embed(),
	)
	kCtx.BindTo(ctx, (*context.Context)(nil))
	kCtx.Bind(client)
	return kCtx.Run()
}
