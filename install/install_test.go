package install_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/fs"

	"github.com/circleci-functions/toolkit/sdk"
	"github.com/circleci-functions/toolkit/sdk/sdktest"

	"github.com/circleci-functions/setup-go/goversion"
	"github.com/circleci-functions/setup-go/install"
	"github.com/circleci-functions/setup-go/internal/testing/fakegodev"
)

// fakeGoBin is a placeholder for the extracted go binary. install never executes
// it (it only verifies extraction landed the file), so the contents are arbitrary.
const fakeGoBin = "#!/bin/sh\nexit 0\n"

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestInstall_GoodChecksum(t *testing.T) {
	tarball := fakegodev.BuildTarball(t, "go1.22.3", fakeGoBin)
	ts := fakegodev.New(t, nil, map[string][]byte{
		"/go1.22.3.linux-amd64.tar.gz": tarball,
	})

	v := goversion.Version{
		Name:   "go1.22.3",
		Short:  "1.22.3",
		URL:    ts.URL + "/go1.22.3.linux-amd64.tar.gz",
		SHA256: sha256Hex(tarball),
		Stable: true,
	}

	dir := t.TempDir()
	goroot, err := install.Install(context.Background(), v, dir, t.TempDir())
	assert.NilError(t, err)
	assert.Equal(t, goroot, filepath.Join(dir, "go1.22.3"))

	assert.Assert(t, fs.Equal(goroot, fs.Expected(t,
		fs.WithMode(0755),
		fs.WithDir("bin",
			fs.WithFile("go", fakeGoBin, fs.WithMode(0755))),
		fs.WithFile("VERSION", "go1.22.3\n", fs.WithMode(0644)),
	)))
}

// TestInstall_SymlinksGoBinaries verifies that Install symlinks go/gofmt
// into binDir, without ever touching the real DefaultBinDir.
func TestInstall_SymlinksGoBinaries(t *testing.T) {
	tarball := fakegodev.BuildTarball(t, "go1.22.3", fakeGoBin)
	ts := fakegodev.New(t, nil, map[string][]byte{
		"/go1.22.3.linux-amd64.tar.gz": tarball,
	})

	v := goversion.Version{
		Name:   "go1.22.3",
		Short:  "1.22.3",
		URL:    ts.URL + "/go1.22.3.linux-amd64.tar.gz",
		SHA256: sha256Hex(tarball),
		Stable: true,
	}

	binDir := t.TempDir()
	goroot, err := install.Install(context.Background(), v, t.TempDir(), binDir)
	assert.NilError(t, err)

	if runtime.GOOS == "windows" {
		return
	}
	target, err := os.Readlink(filepath.Join(binDir, "go"))
	assert.NilError(t, err)
	assert.Equal(t, target, filepath.Join(goroot, "bin", "go"))
}

func TestInstall_Errors(t *testing.T) {
	goodTarball := fakegodev.BuildTarball(t, "go1.22.3", fakeGoBin)
	traversalTarball := fakegodev.BuildTraversalTarball(t)

	tests := []struct {
		name    string
		tarball []byte
		sha256  string
		wantErr string
	}{
		{
			name:    "bad checksum",
			tarball: goodTarball,
			sha256:  "0000000000000000000000000000000000000000000000000000000000000000",
			wantErr: "checksum verification failed",
		},
		{
			name:    "path traversal",
			tarball: traversalTarball,
			sha256:  sha256Hex(traversalTarball),
			wantErr: "illegal path",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := fakegodev.New(t, nil, map[string][]byte{
				"/go1.22.3.linux-amd64.tar.gz": tc.tarball,
			})
			v := goversion.Version{
				Name:   "go1.22.3",
				URL:    ts.URL + "/go1.22.3.linux-amd64.tar.gz",
				SHA256: tc.sha256,
			}
			_, err := install.Install(context.Background(), v, t.TempDir(), t.TempDir())
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// TestInstallZip_GoodChecksum tests the .zip extraction path. It runs on any
// platform — the dispatch is driven by the URL suffix, not runtime.GOOS.
func TestInstallZip_GoodChecksum(t *testing.T) {
	zipData := fakegodev.BuildZip(t, "go1.22.3", fakeGoBin)
	ts := fakegodev.New(t, nil, map[string][]byte{
		"/go1.22.3.windows-amd64.zip": zipData,
	})

	v := goversion.Version{
		Name:   "go1.22.3",
		Short:  "1.22.3",
		URL:    ts.URL + "/go1.22.3.windows-amd64.zip",
		SHA256: sha256Hex(zipData),
		Stable: true,
	}

	dir := t.TempDir()
	goroot, err := install.Install(context.Background(), v, dir, t.TempDir())
	assert.NilError(t, err)
	assert.Equal(t, goroot, filepath.Join(dir, "go1.22.3"))

	assert.Assert(t, fs.Equal(goroot, fs.Expected(t,
		fs.WithMode(0755),
		fs.WithDir("bin",
			fs.WithFile("go.exe", fakeGoBin, fs.WithMode(0755))),
		fs.WithFile("VERSION", "go1.22.3\n", fs.WithMode(0644)),
	)))
}

func TestInstall_ZipErrors(t *testing.T) {
	traversalZip := fakegodev.BuildTraversalZip(t)

	ts := fakegodev.New(t, nil, map[string][]byte{
		"/go1.22.3.windows-amd64.zip": traversalZip,
	})
	v := goversion.Version{
		Name:   "go1.22.3",
		URL:    ts.URL + "/go1.22.3.windows-amd64.zip",
		SHA256: sha256Hex(traversalZip),
	}
	_, err := install.Install(context.Background(), v, t.TempDir(), t.TempDir())
	assert.ErrorContains(t, err, "illegal path")
}

func TestEnsure(t *testing.T) {
	t.Run("detect-only go on PATH", func(t *testing.T) {
		tmp := t.TempDir()
		binDir := filepath.Join(tmp, "bin")
		if err := os.MkdirAll(binDir, 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(binDir, "go"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", binDir)
		v := goversion.Version{Name: "go1.22.3"}
		goroot, err := install.Ensure(context.Background(), v, "", "", true)
		assert.NilError(t, err)
		assert.Equal(t, goroot, tmp)
	})

	t.Run("detect-only no go on PATH", func(t *testing.T) {
		t.Setenv("PATH", "")
		v := goversion.Version{Name: "go1.22.3"}
		_, err := install.Ensure(context.Background(), v, "", "", true)
		assert.ErrorContains(t, err, "no 'go' binary found on PATH")
	})

	t.Run("already installed", func(t *testing.T) {
		installDir := t.TempDir()
		goBin := filepath.Join(installDir, "go1.22.3", "bin", "go")
		if err := os.MkdirAll(filepath.Dir(goBin), 0750); err != nil {
			t.Fatal(err)
		}
		script := "#!/bin/sh\necho 'go version go1.22.3 linux/amd64'\n"
		if err := os.WriteFile(goBin, []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
		v := goversion.Version{Name: "go1.22.3"}
		goroot, err := install.Ensure(context.Background(), v, installDir, "", false)
		assert.NilError(t, err)
		assert.Equal(t, goroot, filepath.Join(installDir, "go1.22.3"))
	})

	t.Run("needs install", func(t *testing.T) {
		tarball := fakegodev.BuildTarball(t, "go1.22.3", fakeGoBin)
		ts := fakegodev.New(t, nil, map[string][]byte{
			"/go1.22.3.linux-amd64.tar.gz": tarball,
		})
		v := goversion.Version{
			Name:   "go1.22.3",
			Short:  "1.22.3",
			URL:    ts.URL + "/go1.22.3.linux-amd64.tar.gz",
			SHA256: sha256Hex(tarball),
			Stable: true,
		}
		installDir := t.TempDir()
		goroot, err := install.Ensure(context.Background(), v, installDir, t.TempDir(), false)
		assert.NilError(t, err)
		assert.Equal(t, goroot, filepath.Join(installDir, "go1.22.3"))
		_, statErr := os.Stat(filepath.Join(goroot, "bin", "go"))
		assert.NilError(t, statErr)
	})
}

// TestEnsureCached_CacheErrorIsNonFatal verifies that a failing cache server
// never blocks the install: the toolchain is still downloaded and goroot returned.
func TestEnsureCached_CacheErrorIsNonFatal(t *testing.T) {
	const version = "go1.22.3"
	tarball := fakegodev.BuildTarball(t, version, fakeGoBin)
	tarPath := fmt.Sprintf("/go1.22.3.%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)

	tests := []struct {
		name       string
		makeClient func(t *testing.T) *sdk.Client
	}{
		{
			name: "connection refused",
			makeClient: func(t *testing.T) *sdk.Client {
				dead := httptest.NewServer(http.NotFoundHandler())
				dead.Close() // closed immediately; any request returns connection refused
				return sdk.NewWithBaseURL(dead.URL)
			},
		},
		{
			name: "server error",
			makeClient: func(t *testing.T) *sdk.Client {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Error(w, "internal error", http.StatusInternalServerError)
				}))
				t.Cleanup(srv.Close)
				return sdk.NewWithBaseURL(srv.URL)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			godevSrv := fakegodev.New(t, nil, map[string][]byte{tarPath: tarball})
			v := goversion.Version{
				Name:   version,
				Short:  "1.22.3",
				URL:    godevSrv.URL + tarPath,
				SHA256: sha256Hex(tarball),
				Stable: true,
			}
			installDir := t.TempDir()

			goroot, err := install.EnsureCached(context.Background(), tc.makeClient(t), v, install.EnsureCachedOptions{
				InstallDir: installDir,
				BinDir:     t.TempDir(),
			})
			assert.NilError(t, err)
			assert.Equal(t, goroot, filepath.Join(installDir, version))
			_, statErr := os.Stat(filepath.Join(goroot, "bin", "go"))
			assert.NilError(t, statErr)
		})
	}
}

// TestEnsureCached_GoCacheChecksumPartitionsCache verifies that different
// --toolchain-checksum values produce distinct cache keys: a restore only hits
// when the checksum matches the entry that was saved.
func TestEnsureCached_GoCacheChecksumPartitionsCache(t *testing.T) {
	const version = "go1.22.3"
	goBinScript := fmt.Sprintf(
		"#!/bin/sh\ncase \"$1\" in\n"+
			"  version) echo 'go version %s %s/%s' ;;\n"+
			"esac\n",
		version, runtime.GOOS, runtime.GOARCH)
	tarball := fakegodev.BuildTarball(t, version, goBinScript)
	tarPath := fmt.Sprintf("/go1.22.3.%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)

	var downloadCount atomic.Int64
	godevSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == tarPath {
			downloadCount.Add(1)
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(tarball)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]fakegodev.Release{
			fakegodev.PlatformRelease(version, sha256Hex(tarball)),
		})
	}))
	t.Cleanup(godevSrv.Close)

	v := goversion.Version{
		Name:   version,
		Short:  "1.22.3",
		URL:    godevSrv.URL + tarPath,
		SHA256: sha256Hex(tarball),
		Stable: true,
	}

	srv := sdktest.NewServer(t)
	installDir := t.TempDir()
	binDir := t.TempDir()
	ctx := context.Background()

	_, err := install.EnsureCached(ctx, srv.Client(), v, install.EnsureCachedOptions{
		InstallDir:      installDir,
		GoCacheChecksum: "checksum-a",
		BinDir:          binDir,
	})
	assert.NilError(t, err)
	assert.Equal(t, int(downloadCount.Load()), 1)

	assert.NilError(t, os.RemoveAll(installDir))

	_, err = install.EnsureCached(ctx, srv.Client(), v, install.EnsureCachedOptions{
		InstallDir:      installDir,
		GoCacheChecksum: "checksum-a",
		BinDir:          binDir,
	})
	assert.NilError(t, err)
	assert.Equal(t, int(downloadCount.Load()), 1, "same checksum must restore from cache without re-downloading")

	assert.NilError(t, os.RemoveAll(installDir))

	_, err = install.EnsureCached(ctx, srv.Client(), v, install.EnsureCachedOptions{
		InstallDir:      installDir,
		GoCacheChecksum: "checksum-b",
		BinDir:          binDir,
	})
	assert.NilError(t, err)
	assert.Equal(t, int(downloadCount.Load()), 2, "different checksum must miss cache and download again")
}
