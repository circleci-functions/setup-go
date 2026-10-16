package goversion_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/setup-go/goversion"
	"github.com/circleci-functions/setup-go/internal/testing/fakegodev"
)

// archiveExt returns the platform-appropriate archive extension: "zip" on
// Windows, "tar.gz" everywhere else. Mirrors the logic in fakegodev.PlatformRelease.
func archiveExt() string {
	if runtime.GOOS == "windows" {
		return "zip"
	}
	return "tar.gz"
}

// fixtureReleases returns a minimal go.dev/dl fixture for the current platform.
// Order mirrors go.dev: newest first, unstable at the top if newer than stable.
func fixtureReleases() []fakegodev.Release {
	ext := archiveExt()
	return []fakegodev.Release{
		// Unstable RC is listed first (newest).
		{
			Version: "go1.24rc1",
			Stable:  false,
			Files: []fakegodev.File{{
				Filename: fmt.Sprintf("go1.24rc1.%s-%s.%s", runtime.GOOS, runtime.GOARCH, ext),
				OS:       runtime.GOOS,
				Arch:     runtime.GOARCH,
				SHA256:   "ddd111",
				Kind:     "archive",
			}},
		},
		fakegodev.PlatformRelease("go1.23.0", "aaa111"),
		fakegodev.PlatformRelease("go1.22.5", "bbb111"),
		fakegodev.PlatformRelease("go1.22.3", "ccc111"),
	}
}

func TestReadVersionFile(t *testing.T) {
	write := func(t *testing.T, content string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "go.mod")
		assert.NilError(t, os.WriteFile(p, []byte(content), 0600))
		return p
	}

	minimum := func(v string) goversion.Requirement { return goversion.Requirement{Version: v} }
	exact := func(v string) goversion.Requirement { return goversion.Requirement{Version: v, Exact: true} }

	tests := []struct {
		name    string
		content string
		want    goversion.Requirement
		wantErr string
	}{
		{
			name:    "patch version",
			content: "module example\n\ngo 1.22.3\n",
			want:    minimum("go1.22.3"),
		},
		{
			name:    "minor only",
			content: "module example\n\ngo 1.22\n",
			want:    minimum("go1.22"),
		},
		{
			name:    "release candidate",
			content: "module example\n\ngo 1.21rc2\n",
			want:    minimum("go1.21rc2"),
		},
		{
			name:    "indented",
			content: "module example\n\n  go 1.22\n",
			want:    minimum("go1.22"),
		},
		{
			name:    "trailing comment",
			content: "module example\n\ngo 1.22 // pinned\n",
			want:    minimum("go1.22"),
		},
		{
			name:    "go.work style",
			content: "go 1.21\n\nuse .\n",
			want:    minimum("go1.21"),
		},
		{
			name:    "toolchain newer than go",
			content: "module example\n\ngo 1.22\n\ntoolchain go1.23.4\n",
			want:    exact("go1.23.4"),
		},
		{
			name:    "toolchain before go",
			content: "module example\n\ntoolchain go1.23.4\n\ngo 1.22\n",
			want:    exact("go1.23.4"),
		},
		{
			name:    "toolchain older than go",
			content: "module example\n\ngo 1.23.2\n\ntoolchain go1.22.5\n",
			want:    minimum("go1.23.2"),
		},
		{
			name:    "toolchain names the go directive's minor",
			content: "module example\n\ngo 1.22\n\ntoolchain go1.22.0\n",
			want:    exact("go1.22.0"),
		},
		{
			name:    "toolchain equal to go",
			content: "module example\n\ngo 1.22.3\n\ntoolchain go1.22.3\n",
			want:    minimum("go1.22.3"),
		},
		{
			name:    "toolchain release candidate",
			content: "module example\n\ngo 1.22\n\ntoolchain go1.23rc1\n",
			want:    exact("go1.23rc1"),
		},
		{
			name:    "go.work toolchain",
			content: "go 1.21\n\ntoolchain go1.22.1\n\nuse .\n",
			want:    exact("go1.22.1"),
		},
		{
			name:    "toolchain without go",
			content: "module example\n\ntoolchain go1.23.4\n",
			want:    exact("go1.23.4"),
		},
		{
			name:    "toolchain default is not a version",
			content: "module example\n\ngo 1.22\n\ntoolchain default\n",
			want:    minimum("go1.22"),
		},
		{
			name:    "go directive that is not a version",
			content: "module example\n\ngo one\n",
			wantErr: `go directive "one"`,
		},
		{
			name:    "no directive",
			content: "module example\n",
			wantErr: `no "go" directive found`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := write(t, tt.content)
			got, err := goversion.ReadVersionFile(p)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got, tt.want)
		})
	}

	t.Run("file not found", func(t *testing.T) {
		_, err := goversion.ReadVersionFile("/nonexistent/go.mod")
		assert.Assert(t, err != nil)
	})
}

func TestResolveRequirement(t *testing.T) {
	ts := fakegodev.New(t, fixtureReleases(), nil)

	tests := []struct {
		name     string
		req      goversion.Requirement
		wantName string
		wantErr  string
	}{
		{name: "minor picks newest patch", req: goversion.Requirement{Version: "go1.22"}, wantName: "go1.22.5"},
		{name: "patch is a minimum", req: goversion.Requirement{Version: "go1.22.0"}, wantName: "go1.22.5"},
		{name: "minimum met exactly", req: goversion.Requirement{Version: "go1.22.5"}, wantName: "go1.22.5"},
		{name: "release candidate takes the stable release", req: goversion.Requirement{Version: "go1.23rc1"}, wantName: "go1.23.0"},
		{name: "prerelease only when nothing stable meets it", req: goversion.Requirement{Version: "go1.24"}, wantName: "go1.24rc1"},
		{
			name:    "never a newer minor",
			req:     goversion.Requirement{Version: "go1.21"},
			wantErr: `no Go release found for "go 1.21"`,
		},
		{
			name:    "minimum above every release of its minor",
			req:     goversion.Requirement{Version: "go1.22.6"},
			wantErr: "pass --version to use a newer Go",
		},
		{name: "toolchain is exact", req: goversion.Requirement{Version: "go1.22.3", Exact: true}, wantName: "go1.22.3"},
		{name: "toolchain prerelease", req: goversion.Requirement{Version: "go1.24rc1", Exact: true}, wantName: "go1.24rc1"},
		{
			name:    "toolchain not released",
			req:     goversion.Requirement{Version: "go1.22.4", Exact: true},
			wantErr: `no Go release found for "toolchain go1.22.4"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := goversion.ResolveRequirement(context.Background(), ts.URL+"/", tt.req)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, v.Name, tt.wantName)
			assert.Equal(t, v.Spec, tt.req.String())
		})
	}
}

func TestResolveVersion(t *testing.T) {
	ts := fakegodev.New(t, fixtureReleases(), nil)

	tests := []struct {
		name       string
		spec       string
		wantName   string
		wantShort  string
		wantURL    string
		wantSHA256 string
		wantStable *bool
		wantErr    string
	}{
		{name: "empty spec resolves stable", spec: "", wantName: "go1.23.0", wantStable: new(true)},
		{name: "stable", spec: "stable", wantName: "go1.23.0", wantStable: new(true)},
		{name: "latest includes unstable", spec: "latest", wantName: "go1.24rc1"},
		{name: "major.minor picks newest patch", spec: "1.22", wantName: "go1.22.5"},
		{
			name: "exact version resolves url and sha",
			spec: "1.22.3", wantName: "go1.22.3", wantShort: "1.22.3",
			wantURL:    fmt.Sprintf("%s/go1.22.3.%s-%s.%s", ts.URL, runtime.GOOS, runtime.GOARCH, archiveExt()),
			wantSHA256: "ccc111",
		},
		{name: "not found", spec: "1.99", wantErr: "no Go release found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := goversion.ResolveVersion(context.Background(), ts.URL+"/", tt.spec)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, v.Name, tt.wantName)
			if tt.wantStable != nil {
				assert.Equal(t, v.Stable, *tt.wantStable)
			}
			if tt.wantShort != "" {
				assert.Equal(t, v.Short, tt.wantShort)
			}
			if tt.wantURL != "" {
				assert.Equal(t, v.URL, tt.wantURL)
			}
			if tt.wantSHA256 != "" {
				assert.Equal(t, v.SHA256, tt.wantSHA256)
			}
		})
	}
}
