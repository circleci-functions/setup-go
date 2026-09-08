// Package pathlink makes an installed Go toolchain resolvable by name for the
// rest of the job, by linking it into a directory PATH already holds.
//
// A CircleCI step inherits its environment from the job rather than from the
// step before it, so a PATH prepend only carries if it is written somewhere the
// next step's shell sources — $BASH_ENV, which needs that shell to be bash.
// A link in a directory already on PATH carries to every later process, in any
// shell, and to processes that never source a profile at all.
package pathlink

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultBinDir is linked into when PATH offers nowhere writable.
const DefaultBinDir = "/usr/local/bin"

// linked are the toolchain binaries made resolvable by name. GOROOT/bin holds
// only these two.
var linked = []string{"go", "gofmt"}

// Options configures Ensure.
type Options struct {
	// GoRoot is the installed toolchain's root; GoRoot/bin holds the binaries
	// to link.
	GoRoot string
	// GoPath is the effective GOPATH. Its bin directory is where `go install`
	// writes, and is made reachable on a best-effort basis. Empty skips it.
	GoPath string
	// BinDir links into exactly this directory and skips PATH discovery,
	// leaving the result unverified. Tests set it so they cannot write into a
	// directory on the developer's real PATH.
	BinDir string
	// Path is the search path to inspect and verify against. Empty means the
	// current process's PATH.
	Path string
}

// Result describes what Ensure did.
type Result struct {
	// Dir is the directory `go` and `gofmt` now resolve from.
	Dir string
	// Verified reports that PATH was confirmed to resolve `go` to GoRoot.
	// False when BinDir skipped discovery, since a caller-chosen directory
	// need not be on PATH at all.
	Verified bool
}

// Ensure makes `go` and `gofmt` from opts.GoRoot resolvable by name for any
// later process that searches PATH, and reports where it linked them.
//
// Unless opts.BinDir skips discovery, it fails if PATH does not resolve `go` to
// opts.GoRoot afterwards, so a job stops at setup rather than building on with
// a toolchain other than the one asked for.
func Ensure(opts Options) (Result, error) {
	if opts.GoRoot == "" {
		return Result{}, errors.New("pathlink: GoRoot is required")
	}
	dirs := SearchDirs(opts.Path)

	if opts.BinDir != "" {
		if err := linkInto(opts.BinDir, opts.GoRoot, false); err != nil {
			return Result{}, err
		}
		reportGoPathBin(opts.GoPath, opts.BinDir, dirs)
		return Result{Dir: opts.BinDir}, nil
	}

	// Already resolvable: --detect-only, or GOROOT/bin is itself on PATH.
	if dir, ok := resolves(dirs, opts.GoRoot); ok {
		reportGoPathBin(opts.GoPath, dir, dirs)
		return Result{Dir: dir, Verified: true}, nil
	}

	dir, elevate := ChooseDir(dirs, IndexOfGo(dirs), Writable)
	if err := linkInto(dir, opts.GoRoot, elevate); err != nil {
		return Result{}, err
	}
	reportGoPathBin(opts.GoPath, dir, dirs)

	if _, ok := resolves(dirs, opts.GoRoot); !ok {
		return Result{Dir: dir}, fmt.Errorf(
			"pathlink: linked go into %s, but PATH still resolves go elsewhere (PATH=%s)",
			dir, strings.Join(dirs, string(os.PathListSeparator)))
	}
	return Result{Dir: dir, Verified: true}, nil
}

// SearchDirs splits path the way the OS does, dropping entries no link can be
// relied on from: empty entries and relative ones, both of which resolve
// against whatever directory a later step happens to run in.
func SearchDirs(path string) []string {
	if path == "" {
		path = os.Getenv("PATH")
	}
	var dirs []string
	for _, d := range filepath.SplitList(path) {
		if d == "" || !filepath.IsAbs(d) {
			continue
		}
		dirs = append(dirs, filepath.Clean(d))
	}
	return dirs
}

// IndexOfGo returns the index of the first directory in dirs holding an
// executable named go, or -1. That is the one a later step's `go` resolves to,
// so it is the entry a link has to get ahead of or replace.
func IndexOfGo(dirs []string) int {
	for i, d := range dirs {
		if goIn(d) != "" {
			return i
		}
	}
	return -1
}

// ChooseDir picks the directory to link into: the earliest writable entry that
// comes before the `go` already on PATH, so the link wins the lookup and a
// preinstalled toolchain is shadowed rather than disturbed. Failing that it
// takes over that `go` where it stands, which needs elevation if the directory
// holding it is root-owned.
//
// existing is the index ChooseDir must get ahead of, or -1 when PATH holds no
// go at all — in which case any writable entry will do.
func ChooseDir(dirs []string, existing int, writable func(string) bool) (dir string, elevate bool) {
	limit := len(dirs)
	if existing >= 0 {
		limit = existing
	}
	for _, d := range dirs[:limit] {
		if writable(d) {
			return d, false
		}
	}
	if existing >= 0 {
		return dirs[existing], !writable(dirs[existing])
	}
	return DefaultBinDir, !writable(DefaultBinDir)
}

// Writable reports whether dir can be linked into without elevation, treating a
// missing directory as writable when it can be created — PATH entries under
// $HOME such as ~/.local/bin often do not exist yet.
func Writable(dir string) bool {
	if err := canCreateIn(dir); err == nil {
		return true
	}
	if _, err := os.Stat(dir); err == nil {
		return false
	}
	return canCreateIn(filepath.Dir(dir)) == nil
}

// canCreateIn reports whether the current process can create entries in dir, by
// creating one. Checking the mode bits would answer for the wrong user under
// sudo, and would miss a read-only mount.
func canCreateIn(dir string) error {
	f, err := os.CreateTemp(dir, ".setup-go-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// resolves reports the directory PATH resolves `go` from, and whether that go
// belongs to goroot.
func resolves(dirs []string, goroot string) (string, bool) {
	i := IndexOfGo(dirs)
	if i < 0 {
		return "", false
	}
	bin := goIn(dirs[i])
	root, err := goRootOf(bin)
	if err != nil {
		return dirs[i], false
	}
	return dirs[i], sameDir(root, goroot)
}

// goIn returns the path to an executable go in dir, or "". On Windows it also
// accepts the shims linkInto falls back to.
func goIn(dir string) string {
	for _, name := range goNames() {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

func goNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"go.exe", "go.bat", "go.cmd"}
	}
	return []string{"go"}
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// goRootOf asks the toolchain at bin where its GOROOT is. $GOROOT is dropped
// from its environment: a toolchain honours that variable over its own
// location, so leaving it set would report the caller's GOROOT for any binary.
func goRootOf(bin string) (string, error) {
	cmd := exec.Command(bin, "env", "GOROOT") //nolint:gosec // bin came from PATH
	cmd.Env = withoutGoRoot(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
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

// sameDir compares two paths as the filesystem would, following symlinks so a
// link into a PATH directory compares equal to the toolchain it points at.
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
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
