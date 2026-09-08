// Package install downloads, verifies, and extracts a Go toolchain archive.
package install

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/circleci-functions/toolkit/download"

	"github.com/circleci-functions/toolkit/archive"
	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-go/goversion"
)

// GoBinary returns the name of the Go binary for the current platform:
// "go.exe" on Windows, "go" everywhere else.
func GoBinary() string {
	if runtime.GOOS == "windows" {
		return "go.exe"
	}
	return "go"
}

// AlreadyInstalled reports whether a Go binary at installDir/v.Name/bin/go[.exe]
// reports the expected version string.
func AlreadyInstalled(installDir string, v goversion.Version) bool {
	goBin := filepath.Join(installDir, v.Name, "bin", GoBinary())
	out, err := exec.Command(goBin, "version").Output() //nolint:gosec // goBin is constructed from trusted configuration
	if err != nil {
		return false
	}
	return strings.Contains(string(out), v.Name)
}

// Ensure returns the GOROOT for v, installing it if not already present.
// If detectOnly is true, it returns the GOROOT of the go binary already on PATH
// without downloading anything.
func Ensure(ctx context.Context, v goversion.Version, installDir string, detectOnly bool) (string, error) {
	if detectOnly {
		p, err := exec.LookPath("go")
		if err != nil {
			return "", fmt.Errorf("no 'go' binary found on PATH")
		}
		return filepath.Dir(filepath.Dir(p)), nil
	}
	if AlreadyInstalled(installDir, v) {
		goroot := filepath.Join(installDir, v.Name)
		fmt.Fprintf(os.Stderr, "setup-go: %s already installed at %s, skipping download\n", v.Name, goroot)
		return goroot, nil
	}
	fmt.Fprintf(os.Stderr, "setup-go: installing %s into %s\n", v.Name, installDir)
	return Install(ctx, v, installDir)
}

type EnsureCachedOptions struct {
	InstallDir string
	DetectOnly bool
	NoCache    bool
	// CacheNonce partitions the toolchain cache. Only the acceptance suite
	// sets it, so a run cannot restore an earlier run's toolchain.
	CacheNonce string
}

// EnsureCached is like Ensure but attempts to restore the toolchain from cache
// before downloading, and saves it to cache after a fresh install. Cache errors
// are non-fatal — a cache hiccup never blocks the install.
//
// When NoCache or DetectOnly is set, or the toolchain is already locally
// installed, it delegates straight to Ensure with no cache calls.
func EnsureCached(ctx context.Context, client *sdk.Client, v goversion.Version, options EnsureCachedOptions) (string, error) {
	if options.NoCache || options.DetectOnly || AlreadyInstalled(options.InstallDir, v) {
		return Ensure(ctx, v, options.InstallDir, options.DetectOnly)
	}

	key := toolchainCacheKey(v, options.CacheNonce)
	_, err := client.RestoreCache(ctx, []string{key},
		sdk.WithStepName("Restoring Go toolchain cache"))
	sdk.LogCacheErr("restore", err)
	if err == nil && AlreadyInstalled(options.InstallDir, v) {
		goroot := filepath.Join(options.InstallDir, v.Name)
		fmt.Fprintf(os.Stderr, "setup-go: %s restored from cache at %s\n", v.Name, goroot)
		return goroot, nil
	}

	goroot, err := Install(ctx, v, options.InstallDir)
	if err != nil {
		return "", err
	}

	sdk.LogCacheErr("save", client.SaveCache(ctx, key, []string{goroot},
		sdk.WithStepName("Saving Go toolchain cache")))
	return goroot, nil
}

// toolchainCacheKey returns the cache key for the given Go toolchain version.
// The v1 prefix leaves room to change what's cached without colliding with old entries.
func toolchainCacheKey(v goversion.Version, nonce string) string {
	key := fmt.Sprintf("setup-go-toolchain-v1-%s-%s", sdk.OSArch(), v.Name)
	if nonce != "" {
		key += "-" + nonce
	}
	return key
}

// Install downloads the Go archive for v, verifies its sha256, extracts it under
// installDir, and returns the GOROOT path (installDir/v.Name). Putting the
// toolchain on PATH is the pathlink package's job, and happens on every run
// rather than only on a fresh install.
func Install(ctx context.Context, v goversion.Version, installDir string) (string, error) {
	dldr, err := download.NewDownloader(ctx, 30*time.Minute, installDir)
	if err != nil {
		return "", fmt.Errorf("create downloader: %w", err)
	}

	archivePath, err := dldr.Download(ctx, v.URL, 0600)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", v.URL, err)
	}

	if err := verifySHA256(archivePath, v.SHA256); err != nil {
		_ = os.Remove(archivePath)
		return "", fmt.Errorf("checksum verification failed: %w", err)
	}

	goroot := filepath.Join(installDir, v.Name)
	var extractErr error
	if strings.HasSuffix(v.URL, ".zip") {
		extractErr = extractGoZip(archivePath, goroot)
	} else {
		extractErr = extractGoTarball(archivePath, goroot)
	}
	if extractErr != nil {
		return "", fmt.Errorf("extract: %w", extractErr)
	}
	_ = os.Remove(archivePath)

	return goroot, nil
}

func verifySHA256(path, wantHex string) error {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != strings.ToLower(wantHex) {
		return fmt.Errorf("got %s, want %s", got, wantHex)
	}
	return nil
}

// extractGoTarball unpacks a Go .tar.gz into destDir, stripping the leading
// "go/" prefix so the tarball's "go/bin/go" lands at destDir/bin/go.
func extractGoTarball(tarGzPath, destDir string) error {
	f, err := os.Open(tarGzPath) //nolint:gosec
	if err != nil {
		return err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}

		// All entries in the official Go tarball begin with "go/".
		const goPrefix = "go/"
		if !strings.HasPrefix(hdr.Name, goPrefix) {
			continue
		}
		rel := strings.TrimPrefix(hdr.Name, goPrefix)

		target := filepath.Join(destDir, rel)

		// Guard against directory traversal in the tarball.
		destAbs := filepath.Clean(destDir) + string(os.PathSeparator)
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), destAbs) {
			return fmt.Errorf("illegal path in tarball: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
				return err
			}
			if err := archive.WriteFile(target, tr, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeSymlink:
			_ = os.Remove(target) // ignore error; target may not exist
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}
	return nil
}

// extractGoZip unpacks a Go .zip into destDir, stripping the leading "go/" prefix
// so the zip's "go/bin/go.exe" lands at destDir/bin/go.exe.
func extractGoZip(zipPath, destDir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("zip: %w", err)
	}
	defer zr.Close()

	const goPrefix = "go/"
	destAbs := filepath.Clean(destDir) + string(os.PathSeparator)

	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, goPrefix) {
			continue
		}
		rel := strings.TrimPrefix(f.Name, goPrefix)
		if rel == "" {
			continue
		}

		target := filepath.Join(destDir, rel)
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), destAbs) {
			return fmt.Errorf("illegal path in zip: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, f.Mode()); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open zip entry %s: %w", f.Name, err)
		}
		writeErr := archive.WriteFile(target, rc, f.Mode())
		if cerr := rc.Close(); cerr != nil && writeErr == nil {
			writeErr = cerr
		}
		if writeErr != nil {
			return writeErr
		}
	}
	return nil
}
