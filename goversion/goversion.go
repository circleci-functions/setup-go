// Package goversion resolves Go version specs against the go.dev release API.
package goversion

import (
	"bufio"
	"context"
	"fmt"
	"go/version"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	hc "github.com/circleci-functions/toolkit/httpcl"
)

var (
	goDirectiveRe = regexp.MustCompile(`^\s*go\s+(\S+)`)
	toolchainRe   = regexp.MustCompile(`^\s*toolchain\s+(\S+)`)
)

// Requirement is the Go a go.mod or go.work file asks for.
type Requirement struct {
	// Version is a Go version with its "go" prefix, e.g. "go1.22", "go1.22.3"
	// or "go1.21rc2".
	Version string
	// Exact is set when Version comes from a toolchain line, which names one
	// release. Otherwise it comes from the go directive, which is a minimum:
	// any later release of the same minor meets it too.
	Exact bool
}

// String returns the requirement as the file declares it.
func (r Requirement) String() string {
	if r.Exact {
		return "toolchain " + r.Version
	}
	return "go " + strings.TrimPrefix(r.Version, "go")
}

// matches reports whether release (e.g. "go1.22.3") meets the requirement. A
// minimum never reaches a newer minor: a module that declares go 1.15 is
// built with Go 1.15, not whatever is newest.
func (r Requirement) matches(release string) bool {
	if r.Exact {
		return version.Compare(release, r.Version) == 0
	}
	return version.Lang(release) == version.Lang(r.Version) && version.Compare(release, r.Version) >= 0
}

// ReadVersionFile reads the Go a go.mod or go.work file asks for: the higher
// of its go directive and its toolchain line. That is the toolchain a later go
// command would switch to under GOTOOLCHAIN=auto, so installing it means no
// later step downloads another.
func ReadVersionFile(path string) (Requirement, error) {
	f, err := os.Open(path) // #nosec G304 — path is caller-supplied; this is a CLI tool
	if err != nil {
		return Requirement{}, err
	}
	defer f.Close()

	var goVersion, toolchain string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if m := goDirectiveRe.FindStringSubmatch(line); m != nil && goVersion == "" {
			goVersion = "go" + m[1]
		}
		if m := toolchainRe.FindStringSubmatch(line); m != nil && toolchain == "" {
			toolchain = m[1]
		}
	}
	if err := scanner.Err(); err != nil {
		return Requirement{}, err
	}
	if goVersion != "" && !version.IsValid(goVersion) {
		return Requirement{}, fmt.Errorf("go directive %q in %s is not a Go version", strings.TrimPrefix(goVersion, "go"), path)
	}
	if !version.IsValid(toolchain) {
		toolchain = ""
	}
	if goVersion == "" && toolchain == "" {
		return Requirement{}, fmt.Errorf("no \"go\" directive found in %s", path)
	}
	if version.Compare(toolchain, goVersion) > 0 {
		return Requirement{Version: toolchain, Exact: true}, nil
	}
	return Requirement{Version: goVersion}, nil
}

const (
	// DefaultAPIBaseURL is the go.dev releases JSON endpoint.
	DefaultAPIBaseURL = "https://go.dev/dl/"
	// DefaultDownloadBaseURL is the base URL for Go toolchain tarballs.
	DefaultDownloadBaseURL = "https://dl.google.com/go/"
)

// Version describes a resolved Go release for the current OS/arch.
type Version struct {
	Spec   string // what the caller requested
	Name   string // e.g. "go1.22.3"
	Short  string // e.g. "1.22.3"
	Stable bool
	URL    string // download URL for the archive
	SHA256 string // hex sha256 of the archive
}

// apiRelease is one entry in the go.dev/dl JSON array.
type apiRelease struct {
	Version string    `json:"version"`
	Stable  bool      `json:"stable"`
	Files   []apiFile `json:"files"`
}

type apiFile struct {
	Filename string `json:"filename"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	SHA256   string `json:"sha256"`
	Kind     string `json:"kind"`
}

// ResolveVersion resolves spec to a concrete release for the current OS/arch.
//
// spec semantics:
//   - "" or "stable" → newest stable release
//   - "latest"       → newest release (may be unstable)
//   - "1.22"         → newest stable go1.22.x
//   - "1.22.3"       → exact go1.22.3
//
// overrideBaseURL, when non-empty, overrides both the release API and the
// tarball download base (they point at the same server in tests); pass "" to
// use the production defaults.
func ResolveVersion(ctx context.Context, overrideBaseURL, spec string) (*Version, error) {
	releases, downloadBaseURL, err := fetchReleases(ctx, overrideBaseURL)
	if err != nil {
		return nil, err
	}
	return resolve(releases, spec, runtime.GOOS, runtime.GOARCH, downloadBaseURL)
}

// ResolveRequirement resolves what a go.mod or go.work file asks for to a
// concrete release for the current OS/arch. A minimum resolves to the newest
// stable release of its minor that meets it, and to a prerelease only when no
// stable release does. overrideBaseURL is as for ResolveVersion.
func ResolveRequirement(ctx context.Context, overrideBaseURL string, req Requirement) (*Version, error) {
	releases, downloadBaseURL, err := fetchReleases(ctx, overrideBaseURL)
	if err != nil {
		return nil, err
	}
	return resolveRequirement(releases, req, runtime.GOOS, runtime.GOARCH, downloadBaseURL)
}

func fetchReleases(ctx context.Context, overrideBaseURL string) ([]apiRelease, string, error) {
	apiBaseURL, downloadBaseURL := DefaultAPIBaseURL, DefaultDownloadBaseURL
	if overrideBaseURL != "" {
		apiBaseURL, downloadBaseURL = overrideBaseURL, overrideBaseURL
	}

	c := hc.New(ctx, hc.Config{
		Name:    "go-version-resolver",
		Timeout: 30 * time.Second,
	})

	var releases []apiRelease
	err := c.Call(ctx, hc.NewRequest("GET", apiBaseURL,
		hc.QueryParams(map[string]string{"mode": "json", "include": "all"}),
		hc.JSONDecoder(&releases),
	))
	if err != nil {
		return nil, "", fmt.Errorf("fetch go releases from %s: %w", apiBaseURL, err)
	}
	return releases, downloadBaseURL, nil
}

func resolve(releases []apiRelease, spec, goos, goarch, downloadBase string) (*Version, error) {
	includeUnstable := spec == "latest"
	anyVersion := spec == "" || spec == "stable" || spec == "latest"

	for _, rel := range releases {
		if !includeUnstable && !rel.Stable {
			continue
		}
		if !anyVersion && !matchSpec(rel.Version, spec) {
			continue
		}
		if v, ok := platformVersion(rel, spec, goos, goarch, downloadBase); ok {
			return v, nil
		}
	}

	return nil, fmt.Errorf("no Go release found for spec %q (os=%s arch=%s)", spec, goos, goarch)
}

func resolveRequirement(releases []apiRelease, req Requirement, goos, goarch, downloadBase string) (*Version, error) {
	for _, stable := range []bool{true, false} {
		var best *Version
		for _, rel := range releases {
			if rel.Stable != stable || !req.matches(rel.Version) {
				continue
			}
			if best != nil && version.Compare(rel.Version, best.Name) <= 0 {
				continue
			}
			if v, ok := platformVersion(rel, req.String(), goos, goarch, downloadBase); ok {
				best = v
			}
		}
		if best != nil {
			return best, nil
		}
	}

	if req.Exact {
		return nil, fmt.Errorf("no Go release found for %q (os=%s arch=%s)", req, goos, goarch)
	}
	return nil, fmt.Errorf("no Go release found for %q (os=%s arch=%s); setup-go stays within %s, so pass --version to use a newer Go",
		req, goos, goarch, version.Lang(req.Version))
}

// platformVersion returns rel's archive for goos/goarch, if it has one.
func platformVersion(rel apiRelease, spec, goos, goarch, downloadBase string) (*Version, bool) {
	for _, f := range rel.Files {
		if f.OS == goos && f.Arch == goarch && f.Kind == "archive" {
			return &Version{
				Spec:   spec,
				Name:   rel.Version,
				Short:  strings.TrimPrefix(rel.Version, "go"),
				Stable: rel.Stable,
				URL:    strings.TrimSuffix(downloadBase, "/") + "/" + f.Filename,
				SHA256: f.SHA256,
			}, true
		}
	}
	return nil, false
}

// matchSpec reports whether the release version (e.g. "go1.22.3") satisfies the spec.
// spec "1.22" matches any go1.22.x; spec "1.22.3" requires exact match.
func matchSpec(version, spec string) bool {
	vNum := strings.TrimPrefix(version, "go")
	parts := strings.Split(spec, ".")
	if len(parts) >= 3 {
		return vNum == spec
	}
	return strings.HasPrefix(vNum, spec+".")
}
