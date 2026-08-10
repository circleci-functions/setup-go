// Command setup-go installs a Go toolchain and prepends it to PATH via
// $BASH_ENV so subsequent steps in the same job pick it up automatically.
//
// Supported platforms: linux, darwin, windows (MSYS2/Git Bash).
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/alecthomas/kong"

	"github.com/circleci-functions/toolkit/functionyaml"
	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-go/cache"
	"github.com/circleci-functions/setup-go/cgoalpine"
	"github.com/circleci-functions/setup-go/goenv"
	"github.com/circleci-functions/setup-go/goversion"
	"github.com/circleci-functions/setup-go/install"
	"github.com/circleci-functions/setup-go/jobenv"
)

type cli struct {
	Install     installCmd     `cmd:"install" default:"withargs" help:"Install a Go toolchain and prepend it to PATH via $BASH_ENV."`
	Test        testCmd        `cmd:"test" help:"Run go tests."`
	Cache       cacheCmd       `cmd:"cache" help:"Save/restore cache."`
	ModDownload modDownloadCmd `cmd:"mod-download" help:"Download and cache go modules."`
}

type installCmd struct {
	Version         string `help:"Go version spec (e.g. 1.22, 1.22.3, stable, latest)." default:"stable"`
	VersionFile     string `name:"version-file" help:"Path to a go.mod or go.work file to read the Go version from. Overrides --version when set." default:""`
	DetectOnly      bool   `help:"Detect and export env using the Go already on PATH, without installing."`
	InstallDir      string `help:"Base directory for Go toolchain installs." default:"${default_install_dir}"`
	APIURL          string `name:"api-url" help:"Override go.dev release API base URL (also overrides tarball download base)." hidden:"" default:""`
	GoCacheChecksum string `name:"go-cache-checksum" help:"Optional checksum to partition the toolchain cache; different checksums force a re-download even for the same Go version." default:""`
	BinDir          string `name:"bin-dir" help:"Override the directory go/gofmt are symlinked into." hidden:"" default:""`
}

func (i *installCmd) Run(ctx context.Context, client *sdk.Client) error {
	spec := i.Version
	if i.VersionFile != "" {
		v, err := goversion.ReadVersionFile(i.VersionFile)
		if err != nil {
			return fmt.Errorf("read version file %q: %w", i.VersionFile, err)
		}
		spec = v
	}

	ver, err := goversion.ResolveVersion(ctx, i.APIURL, spec)
	if err != nil {
		return fmt.Errorf("resolve version: %w", err)
	}
	fmt.Fprintf(os.Stderr, "setup-go: resolved %q → %s\n", spec, ver.Name)

	goroot, err := install.EnsureCached(ctx, client, *ver, install.EnsureCachedOptions{
		GoCacheChecksum: i.GoCacheChecksum,
		InstallDir:      i.InstallDir,
		DetectOnly:      i.DetectOnly,
		BinDir:          i.BinDir,
	})
	if err != nil {
		return err
	}

	goBinary := filepath.Join(goroot, "bin", install.GoBinary())
	goPath, err := jobenv.DetectGoPath(ctx, goBinary)
	if err != nil {
		return fmt.Errorf("detect go path: %w", err)
	}

	if err := jobenv.WriteEnv(goPath, filepath.Join(goroot, "bin")); err != nil {
		return fmt.Errorf("write env: %w", err)
	}
	fmt.Fprintf(os.Stderr, "setup-go: wrote env exports to %s\n", os.Getenv("BASH_ENV"))
	_ = cgoalpine.InstallToolchain(ctx, cgoalpine.DefaultOSReleasePath)
	return nil
}

type testCmd struct {
	ProjectPath  string  `help:"The path to the directory containing your Go project files: .goreleaser.yaml, go.mod, main.go. Defaults to $HOME."`
	Race         bool    `help:"Run tests with -race option. If enabled, --covermode will default to 'atomic'."`
	CoverMode    *string `name:"covermode" enum:",set,count,atomic" help:"Flag to set the coverage mode. The go default is 'set', unless --race is enabled, in which case the go default is 'atomic')."`
	FailFast     bool    `name:"failfast" help:"Do not start new tests after the first test failure."`
	Count        *int    `help:"Run each test, benchmark, and fuzz seed n times. If n=1, test caching is disabled."`
	UseGoTestSum bool    `name:"use-gotestsum" help:"Run via gotestsum instead of go test. Requires gotestsum on PATH."`
	Cache        bool    `help:"Set to false to skip restoring and saving the go build cache." default:"true"`
	CacheKey     string  `help:"Optional cache key component to partition cache entries. If not set, defaults to the empty string."`
	Checksum     string  `help:"Optional checksum to partition the cache entry. If not set, defaults to the empty string."`
	CachePath    *string `help:"Override the go build cache directory (GOCACHE) to save/restore, instead of detecting it via 'go env'."`
}

func (t *testCmd) Run(ctx context.Context, client *sdk.Client) error {
	if _, err := exec.LookPath("go"); err != nil {
		return err
	}

	projectPath := t.ProjectPath
	if projectPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve project path: %w", err)
		}
		projectPath = home
	}

	args := []string{"test", "./..."}

	if t.Race {
		args = append(args, "-race")
	}

	if t.CoverMode != nil {
		args = append(args, "-covermode", *t.CoverMode)
	}

	if t.FailFast {
		args = append(args, "-failfast")
	}

	if t.Count != nil {
		args = append(args, fmt.Sprintf("-count=%d", *t.Count))
	}

	name := "go"
	if t.UseGoTestSum {
		if _, err := exec.LookPath("gotestsum"); err != nil {
			return err
		}
		name = "gotestsum"
		args = append([]string{"--"}, args[1:]...)
	}

	// Resolved up front so a bad --cache-path or an unusable `go env` fails
	// before the tests run rather than after.
	buildCache := ""
	if t.Cache {
		dir, err := buildCacheDir(t.CachePath, "--cache-path", nil)
		if err != nil {
			return err
		}
		buildCache = dir
		cache.RestoreBuildCache(ctx, client, t.CacheKey, t.Checksum)
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = projectPath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	testErr := cmd.Run()

	// Saved even when the tests fail: a failed run still compiled packages a
	// re-run can reuse.
	if t.Cache {
		cache.SaveBuildCache(ctx, client, t.CacheKey, t.Checksum, buildCache)
	}

	if testErr != nil {
		return fmt.Errorf("go test: %w", testErr)
	}

	return nil
}

type cacheCmd struct {
	Save cacheSaveCmd `cmd:"" help:"Save cache."`
	Load cacheLoadCmd `cmd:"" help:"Load cache."`
}

type cacheLoadCmd struct {
	cacheFlags
}

type cacheSaveCmd struct {
	cacheFlags
	BuildPath *string `name:"build-path" help:"Override the go build cache directory (GOCACHE) to save, instead of detecting it via 'go env'."`
}

type cacheFlags struct {
	// TODO(mang): implement this too.
	// Test         bool   `help:"go test cache."`
	GoMod        bool   `help:"go module cache." default:"false"`
	Build        bool   `help:"go build cache (GOCACHE)." default:"false"`
	GolangCILint bool   `name:"golangci-lint" help:"golangci-lint cache." default:"false"`
	Key          string `help:"Optional cache key component to partition cache entries. If not set, defaults to the empty string."`
	Checksum     string `help:"Optional checksum to partition the cache entry. Different checksums force a cache miss. If not set, defaults to the empty string."`
}

var errNoCacheTarget = fmt.Errorf("no cache target specified. Use --golangci-lint, --go-mod or --build")

func (f *cacheFlags) anyTarget() bool {
	return f.GolangCILint || f.GoMod || f.Build
}

func (l *cacheLoadCmd) Run(ctx context.Context, client *sdk.Client) error {
	if !l.anyTarget() {
		return errNoCacheTarget
	}

	if l.GoMod {
		cache.RestoreGoModCache(ctx, client, l.Key, l.Checksum)
	}

	if l.Build {
		cache.RestoreBuildCache(ctx, client, l.Key, l.Checksum)
	}

	if l.GolangCILint {
		cache.RestoreLintCache(ctx, client, l.Key, l.Checksum)
	}

	return nil
}

func (s *cacheSaveCmd) Run(ctx context.Context, client *sdk.Client) error {
	if !s.anyTarget() {
		return errNoCacheTarget
	}

	// Both --go-mod and --build read their directory from `go env`, so load it
	// once instead of shelling out per target.
	var goEnv *goenv.GoEnv
	if s.GoMod || (s.Build && s.BuildPath == nil) {
		var err error
		if goEnv, err = goenv.Load(); err != nil {
			return err
		}
	}

	if s.GoMod {
		cache.SaveGoModCache(ctx, client, s.Key, s.Checksum, goEnv.GOMODCACHE)
	}

	if s.Build {
		dir, err := buildCacheDir(s.BuildPath, "--build-path", goEnv)
		if err != nil {
			return err
		}
		cache.SaveBuildCache(ctx, client, s.Key, s.Checksum, dir)
	}

	if s.GolangCILint {
		cache.SaveLintCache(ctx, client, s.Key, s.Checksum)
	}
	return nil
}

// buildCacheDir resolves the go build cache directory, preferring an explicit
// override over `go env` (GOCACHE, not a hardcoded ~/.cache/go-build, which is
// wrong on darwin). flag names the override flag, for the error message; env,
// when non-nil, is a already-loaded `go env` to reuse.
func buildCacheDir(override *string, flag string, env *goenv.GoEnv) (string, error) {
	if override != nil {
		if *override == "" {
			return "", fmt.Errorf("must pass a non-empty directory with %s", flag)
		}
		return *override, nil
	}

	if env == nil {
		var err error
		if env, err = goenv.Load(); err != nil {
			return "", err
		}
	}
	if env.GOCACHE == "" {
		return "", fmt.Errorf("go env reported an empty GOCACHE")
	}
	return env.GOCACHE, nil
}

type modDownloadCmd struct {
	Cache    bool    `help:"Set to false to skip restoring and saving the go mod cache." default:"true"`
	CacheKey string  `help:"Optional cache key component to partition cache entries. If not set, defaults to the empty string."`
	Checksum string  `help:"Optional checksum to partition the cache entry.If not set, defaults to the empty string."`
	Path     *string `help:"Override the go module cache directory (GOMODCACHE) to save/restore, instead of detecting it via 'go env'."`
}

func (md *modDownloadCmd) Run(ctx context.Context, client *sdk.Client) error {
	if md.Cache {
		cache.RestoreGoModCache(ctx, client, md.CacheKey, md.Checksum)
	}

	cmd := exec.CommandContext(ctx, "go", "mod", "download")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go mod download: %w", err)
	}

	if md.Cache {
		goModCacheDir := ""
		if md.Path != nil {
			if *md.Path == "" {
				return fmt.Errorf("must pass a non-empty directory with --path")
			}
			goModCacheDir = *md.Path
		} else {
			goEnv, err := goenv.Load()
			if err != nil {
				return err
			}
			goModCacheDir = goEnv.GOMODCACHE
		}
		cache.SaveGoModCache(ctx, client, md.CacheKey, md.Checksum, goModCacheDir)
	}

	return nil
}

func main() {
	sdk.Run(run)
}

func run(ctx context.Context, client *sdk.Client) error {
	var flags cli
	kCtx := kong.Parse(&flags,
		kong.Name("setup-go"),
		kong.Description("Install a Go toolchain and prepend it to PATH via $BASH_ENV.\n\nExample:\n  circleci run setup-go@<version> -- --version 1.22"),
		kong.UsageOnError(),
		kong.Vars{"default_install_dir": defaultInstallDir()},
		functionyaml.Embed(),
	)
	kCtx.BindTo(ctx, (*context.Context)(nil))
	kCtx.Bind(client)
	return kCtx.Run()
}

func defaultInstallDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".cache/setup-go"
	}
	return filepath.Join(home, ".cache", "setup-go")
}
