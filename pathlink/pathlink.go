// Package pathlink makes an installed Go toolchain resolvable by name for the
// rest of the job, by linking it into a directory PATH already holds.
//
// A step inherits its environment from the job, not from the step before it, so
// a PATH prepend only carries via $BASH_ENV, and only into bash. A link carries
// into any shell, and into processes that source nothing.
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

// DefaultBinDir is the last preference on PATH. Windows has no equivalent.
const DefaultBinDir = "/usr/local/bin"

// linked is all of GOROOT/bin.
var linked = []string{"go", "gofmt"}

// Options configures Ensure.
type Options struct {
	// GoRoot is the installed toolchain's root.
	GoRoot string
	// GoPath is the effective GOPATH, whose bin directory is made reachable on
	// a best-effort basis. Empty skips it.
	GoPath string
	// BinDir links into exactly this directory and skips discovery, leaving the
	// result unverified. Tests set it, so they cannot write to the real PATH.
	BinDir string
	// Path is the search path to use. Empty means this process's PATH.
	Path string
}

// Result describes what Ensure did.
type Result struct {
	// Dir is where `go` and `gofmt` now resolve from.
	Dir string
	// Verified reports that PATH was confirmed to resolve `go` to GoRoot. False
	// when BinDir skipped discovery, since that directory need not be on PATH.
	Verified bool
}

// Ensure makes `go` and `gofmt` from opts.GoRoot resolvable by name, and
// reports where it linked them.
//
// Unless opts.BinDir skips discovery, it fails if PATH does not then resolve
// `go` to opts.GoRoot, rather than letting the job build with another
// toolchain.
func Ensure(opts Options) (Result, error) {
	if opts.GoRoot == "" {
		return Result{}, errors.New("pathlink: GoRoot is required")
	}
	dirs := SearchDirs(opts.Path)

	if opts.BinDir != "" {
		if err := linkInto(opts.BinDir, opts.GoRoot, false); err != nil {
			return Result{}, err
		}
		reportGoPathBin(opts.GoPath, opts.BinDir, dirs, false)
		return Result{Dir: opts.BinDir}, nil
	}

	// Already resolvable: --detect-only, or GOROOT/bin is itself on PATH.
	if r, ok := resolveGo(dirs); ok && r.matches(opts.GoRoot) {
		reportGoPathBin(opts.GoPath, r.dir, dirs, false)
		return Result{Dir: r.dir, Verified: true}, nil
	}

	dir, elevate := ChooseDir(dirs, IndexOfGo(dirs), PreferredDirs(opts.GoPath), Writable)
	if dir == "" {
		return Result{}, fmt.Errorf(
			"pathlink: no directory on PATH can hold the toolchain (PATH=%s)",
			strings.Join(dirs, string(os.PathListSeparator)))
	}
	if err := linkInto(dir, opts.GoRoot, elevate); err != nil {
		return Result{}, err
	}
	reportGoPathBin(opts.GoPath, dir, dirs, elevate)

	if r, ok := resolveGo(dirs); !ok || !r.matches(opts.GoRoot) {
		return Result{Dir: dir}, fmt.Errorf("pathlink: linked go into %s, but %s", dir, r.explain(opts.GoRoot))
	}
	return Result{Dir: dir, Verified: true}, nil
}

// SearchDirs splits path, dropping relative entries: those resolve against
// whatever directory a later step runs in.
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

// IndexOfGo returns the index of the first directory holding a go, or -1. That
// is what a later step resolves, so a link has to get ahead of it or replace
// it.
func IndexOfGo(dirs []string) int {
	for i, d := range dirs {
		if goIn(d) != "" {
			return i
		}
	}
	return -1
}

// ChooseDir picks the directory to link into, by preference rather than PATH
// order: a version manager rewrites its shims when it rehashes, taking any link
// left there with it. The choice has to come ahead of the `go` already on PATH
// or it would be shadowed; failing that it takes that `go` over, which needs
// elevation if its directory is root-owned.
//
// existing is the index to get ahead of, or -1 when PATH holds no go.
func ChooseDir(dirs []string, existing int, preferred []string, writable func(string) bool) (dir string, elevate bool) {
	// -1 is not on PATH at all, so a link there resolves for nobody.
	ahead := func(i int) bool { return i >= 0 && (existing < 0 || i < existing) }

	for _, p := range preferred {
		if i := indexOfDir(dirs, p); ahead(i) && writable(p) {
			return p, false
		}
	}
	// Before scanning: that directory is a toolchain's by definition, where an
	// arbitrary writable entry belongs to whatever tool put it there.
	if existing >= 0 {
		return dirs[existing], !writable(dirs[existing])
	}
	for i, d := range dirs {
		if ahead(i) && !managed(d) && writable(d) {
			return d, false
		}
	}
	if runtime.GOOS == "windows" {
		return "", false
	}
	return DefaultBinDir, !writable(DefaultBinDir)
}

// PreferredDirs returns the directories worth linking into, best first.
// GOPATH/bin leads because the toolchain already writes there, so nothing else
// reorganises it.
func PreferredDirs(goPath string) []string {
	var dirs []string
	if bin := GoPathBin(goPath); bin != "" {
		dirs = append(dirs, bin)
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"), filepath.Join(home, "bin"))
	}
	if runtime.GOOS != "windows" {
		dirs = append(dirs, DefaultBinDir)
	}
	return dirs
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

// managed reports whether dir belongs to a tool that rewrites its contents.
// pyenv, rbenv, asdf and goenv all call that directory "shims".
func managed(dir string) bool {
	for _, part := range strings.Split(filepath.ToSlash(dir), "/") {
		switch part {
		case "shims", ".nvm", "node_modules":
			return true
		}
	}
	return false
}

// indexOfDir returns dir's position in dirs, or -1.
func indexOfDir(dirs []string, dir string) int {
	for i, d := range dirs {
		if sameDir(d, dir) {
			return i
		}
	}
	return -1
}

// Writable reports whether dir can be linked into without elevation. A missing
// directory counts if it can be created: ~/.local/bin is often on PATH before
// it exists.
func Writable(dir string) bool {
	if err := canCreateIn(dir); err == nil {
		return true
	}
	if _, err := os.Stat(dir); err == nil {
		return false
	}
	return canCreateIn(filepath.Dir(dir)) == nil
}

// canCreateIn tries it, rather than reading mode bits: those answer for the
// wrong user under sudo and miss a read-only mount.
func canCreateIn(dir string) error {
	f, err := os.CreateTemp(dir, ".setup-go-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// resolution is the `go` PATH resolves to, and what it says about itself.
type resolution struct {
	dir  string // the PATH entry it was found in
	bin  string // the executable PATH resolves to
	root string // the GOROOT it reports
	err  error  // why it could not be asked
}

// resolveGo finds the `go` a later step would run and asks for its GOROOT.
// False when PATH holds no go.
func resolveGo(dirs []string) (resolution, bool) {
	i := IndexOfGo(dirs)
	if i < 0 {
		return resolution{}, false
	}
	r := resolution{dir: dirs[i], bin: goIn(dirs[i])}
	r.root, r.err = goRootOf(r.bin)
	return r, true
}

// matches reports whether the resolved go is goroot's own.
func (r resolution) matches(goroot string) bool {
	return r.err == nil && sameDir(r.root, goroot)
}

// explain distinguishes a shadowed link from a toolchain that will not answer.
func (r resolution) explain(goroot string) string {
	switch {
	case r.bin == "":
		return "PATH holds no go at all"
	case r.err != nil:
		return fmt.Sprintf("PATH resolves go to %s, which could not report its GOROOT: %v", r.bin, r.err)
	default:
		return fmt.Sprintf("PATH resolves go to %s, whose GOROOT is %q, not %q", r.bin, r.root, goroot)
	}
}

// goIn returns an executable go in dir, or "". On Windows it accepts shims too.
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

// goRootOf asks the toolchain at bin for its GOROOT. $GOROOT is dropped: a
// toolchain honours it over its own location, so leaving it set would report
// the caller's GOROOT for any binary.
func goRootOf(bin string) (string, error) {
	cmd := goCommand(bin, "env", "GOROOT")
	cmd.Env = withoutGoRoot(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// goCommand runs a toolchain entry point. CreateProcess cannot execute a .bat,
// so a shim has to go through cmd.
func goCommand(bin string, args ...string) *exec.Cmd {
	switch strings.ToLower(filepath.Ext(bin)) {
	case ".bat", ".cmd":
		return exec.Command("cmd", append([]string{"/c", bin}, args...)...) //nolint:gosec // bin came from PATH
	default:
		return exec.Command(bin, args...) //nolint:gosec // bin came from PATH
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

// sameDir compares paths as the filesystem would, so a link compares equal to
// what it points at.
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
