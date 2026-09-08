// Package goroot puts an installed Go toolchain on PATH for the rest of the
// job, and makes what `go install` builds reachable too.
package goroot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/circleci-functions/toolkit/pathlink"
)

// binaries is all of GOROOT/bin.
var binaries = []string{"go", "gofmt"}

// Options configures Link.
type Options struct {
	// GoRoot is the installed toolchain's root.
	GoRoot string
	// GoPath is the effective GOPATH, whose bin directory is made reachable on
	// a best-effort basis. Empty skips it.
	GoPath string
	// BinDir links into exactly this directory and skips discovery. Tests set
	// it, so they cannot write to the real PATH.
	BinDir string
	// Path is the search path to use. Empty means this process's PATH.
	Path string
}

// Link makes `go` and `gofmt` resolvable by name, and reports where they went.
func Link(opts Options) (pathlink.Result, error) {
	if opts.GoRoot == "" {
		return pathlink.Result{}, fmt.Errorf("goroot: GoRoot is required")
	}

	res, err := pathlink.Ensure(pathlink.Options{
		Names:     binaries,
		FromDir:   filepath.Join(opts.GoRoot, "bin"),
		Preferred: preferredDirs(opts.GoPath),
		Dir:       opts.BinDir,
		Path:      opts.Path,
		Verify:    verifyGoRoot(opts.GoRoot),
	})
	if err != nil {
		return res, err
	}

	reportGoPathBin(opts.GoPath, res.Dir, pathlink.SearchDirs(opts.Path))
	return res, nil
}

// preferredDirs puts GOPATH/bin first: the toolchain already writes there, so
// nothing else reorganises it.
func preferredDirs(goPath string) []string {
	if bin := GoPathBin(goPath); bin != "" {
		return append([]string{bin}, pathlink.UserBinDirs()...)
	}
	return pathlink.UserBinDirs()
}

// GoPathBin returns the bin directory `go install` writes to, or "". GOPATH is
// a list — the machine executor sets two — and `go install` uses the first.
func GoPathBin(goPath string) string {
	for _, p := range filepath.SplitList(goPath) {
		if p != "" {
			return filepath.Join(p, "bin")
		}
	}
	return ""
}

// verifyGoRoot asks the resolved toolchain for its GOROOT. A toolchain honours
// $GOROOT over its own location, so it is dropped: leaving it set would report
// the caller's GOROOT for any binary.
func verifyGoRoot(goRoot string) func(string) error {
	return func(bin string) error {
		cmd := pathlink.Command(bin, "env", "GOROOT")
		cmd.Env = withoutGoRoot(os.Environ())

		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("could not report its GOROOT: %w", err)
		}
		got := strings.TrimSpace(string(out))
		if !sameDir(got, goRoot) {
			return fmt.Errorf("has GOROOT %q, not %q", got, goRoot)
		}
		return nil
	}
}

func withoutGoRoot(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if name, _, ok := strings.Cut(kv, "="); ok && name == "GOROOT" {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if r, err := filepath.EvalSymlinks(a); err == nil {
		a = r
	}
	if r, err := filepath.EvalSymlinks(b); err == nil {
		b = r
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
