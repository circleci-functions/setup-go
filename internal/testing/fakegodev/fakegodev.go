// Package fakegodev provides a stub go.dev release API server for use in tests.
package fakegodev

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
)

// Release mirrors a go.dev/dl JSON release entry.
type Release struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
	Files   []File `json:"files"`
}

// File mirrors a go.dev/dl JSON file entry.
type File struct {
	Filename string `json:"filename"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	SHA256   string `json:"sha256"`
	Kind     string `json:"kind"`
}

// Server is a test stub for the go.dev release API.
type Server struct {
	URL string
	s   *httptest.Server
}

// New starts a Server that serves releases as go.dev/dl JSON, and optionally
// serves tarballs keyed by URL path (e.g. "/go1.22.3.linux-amd64.tar.gz").
// Pass nil for tarballs if no tarball serving is needed.
func New(t *testing.T, releases []Release, tarballs map[string][]byte) *Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if data, ok := tarballs[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(data)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(releases)
	}))
	t.Cleanup(ts.Close)
	return &Server{URL: ts.URL, s: ts}
}

// PlatformRelease returns a stable Release with a single archive file for the current OS/arch.
// On Windows the archive uses the .zip extension; elsewhere .tar.gz.
func PlatformRelease(version, sha256 string) Release {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return Release{
		Version: version,
		Stable:  true,
		Files: []File{{
			Filename: fmt.Sprintf("%s.%s-%s.%s", version, runtime.GOOS, runtime.GOARCH, ext),
			OS:       runtime.GOOS,
			Arch:     runtime.GOARCH,
			SHA256:   sha256,
			Kind:     "archive",
		}},
	}
}

// BuildZip creates a minimal .zip that mirrors the structure of an official Go
// Windows release: go/bin/go.exe (with goBinContent) and go/VERSION.
func BuildZip(t *testing.T, version, goBinContent string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	entries := []struct {
		name string
		mode os.FileMode
		body string
	}{
		{"go/", 0755, ""},
		{"go/bin/", 0755, ""},
		{"go/bin/go.exe", 0755, goBinContent},
		{"go/VERSION", 0644, version + "\n"},
	}

	for _, e := range entries {
		fh := &zip.FileHeader{
			Name:   e.name,
			Method: zip.Deflate,
		}
		fh.SetMode(e.mode)
		w, err := zw.CreateHeader(fh)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", e.name, err)
		}
		if e.body != "" {
			if _, err := io.WriteString(w, e.body); err != nil {
				t.Fatalf("write zip body %s: %v", e.name, err)
			}
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// BuildTraversalZip returns a .zip containing a path-traversal entry for use in
// security-related extraction tests.
func BuildTraversalZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	body := "owned"
	fh := &zip.FileHeader{Name: "go/../evil", Method: zip.Deflate}
	fh.SetMode(0644)
	w, err := zw.CreateHeader(fh)
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := io.WriteString(w, body); err != nil {
		t.Fatalf("write zip body: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// BuildTarball creates a minimal .tar.gz that mimics the structure of an official
// Go tarball: go/bin/go (executable with goBinScript content) + go/VERSION.
func BuildTarball(t *testing.T, version, goBinScript string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	entries := []struct {
		name string
		mode int64
		body string
	}{
		{"go/", 0755, ""},
		{"go/bin/", 0755, ""},
		{"go/bin/go", 0755, goBinScript},
		{"go/VERSION", 0644, version + "\n"},
	}

	for _, e := range entries {
		hdr := &tar.Header{
			Name: e.name,
			Mode: e.mode,
			Size: int64(len(e.body)),
		}
		if e.body == "" {
			hdr.Typeflag = tar.TypeDir
			hdr.Size = 0
		} else {
			hdr.Typeflag = tar.TypeReg
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %s: %v", e.name, err)
		}
		if e.body != "" {
			if _, err := io.WriteString(tw, e.body); err != nil {
				t.Fatalf("write tar body %s: %v", e.name, err)
			}
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// BuildTraversalTarball returns a .tar.gz containing a path-traversal entry (go/../evil)
// for use in security-related extraction tests.
func BuildTraversalTarball(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	body := "owned"
	hdr := &tar.Header{Name: "go/../evil", Mode: 0644, Size: int64(len(body)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := io.WriteString(tw, body); err != nil {
		t.Fatalf("write tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}
