// Package goversion resolves Go version specs against the go.dev release API.
package goversion

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	hc "github.com/circleci-functions/toolkit/httpcl"
)

var goDirectiveRe = regexp.MustCompile(`^go\s+(\d+\.\d+(?:\.\d+)?)`)

// ReadVersionFile reads a Go version spec from the "go X.Y[.Z]" directive in
// a go.mod or go.work file.
func ReadVersionFile(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 — path is caller-supplied; this is a CLI tool
	if err != nil {
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if m := goDirectiveRe.FindStringSubmatch(scanner.Text()); m != nil {
			return m[1], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no \"go\" directive found in %s", path)
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
		return nil, fmt.Errorf("fetch go releases from %s: %w", apiBaseURL, err)
	}

	return resolve(releases, spec, runtime.GOOS, runtime.GOARCH, downloadBaseURL)
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
		for _, f := range rel.Files {
			if f.OS == goos && f.Arch == goarch && f.Kind == "archive" {
				short := strings.TrimPrefix(rel.Version, "go")
				return &Version{
					Spec:   spec,
					Name:   rel.Version,
					Short:  short,
					Stable: rel.Stable,
					URL:    strings.TrimSuffix(downloadBase, "/") + "/" + f.Filename,
					SHA256: f.SHA256,
				}, nil
			}
		}
	}

	return nil, fmt.Errorf("no Go release found for spec %q (os=%s arch=%s)", spec, goos, goarch)
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
