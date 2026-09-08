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

// DefaultBinDir is the traditional place for a locally installed binary, and
// the last preference on PATH. Windows has no equivalent, so there it is not
// offered at all.
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

// ChooseDir picks the directory to link into, in order of preference rather
// than PATH order: a job's PATH holds directories that belong to other tools,
// and a version manager rewrites its shims whenever it rehashes, taking a link
// left there with it. preferred names the directories conventionally meant for
// this — GOPATH/bin, a user bin directory, /usr/local/bin — and any of them
// that is on PATH is a better bet than whatever happens to come first.
//
// Whatever it picks has to come before the `go` already on PATH, or the link
// would be shadowed by it; failing that it takes over that `go` where it
// stands, which needs elevation if the directory holding it is root-owned.
//
// existing is the index the choice must get ahead of, or -1 when PATH holds no
// go at all — in which case anything on PATH will do.
func ChooseDir(dirs []string, existing int, preferred []string, writable func(string) bool) (dir string, elevate bool) {
	// -1 means the directory is not on PATH at all, so a link there resolves
	// for nobody.
	ahead := func(i int) bool { return i >= 0 && (existing < 0 || i < existing) }

	for _, p := range preferred {
		if i := indexOfDir(dirs, p); ahead(i) && writable(p) {
			return p, false
		}
	}
	// Taking over the `go` already on PATH before falling back to scanning it:
	// that directory is a toolchain's by definition, where an arbitrary
	// writable entry belongs to whatever tool put it there.
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

// PreferredDirs returns the directories worth linking into ahead of anything
// else PATH offers, best first. GOPATH/bin comes first because the Go
// toolchain already writes there, so nothing else is going to reorganise it.
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

// GoPathBin returns the bin directory `go install` writes to, or "".
//
// GOPATH is a list, and the machine executor sets two entries. `go install`
// uses the first, and so does this: joining "bin" onto the whole list would
// name a directory that is nobody's.
func GoPathBin(goPath string) string {
	for _, p := range filepath.SplitList(goPath) {
		if p != "" {
			return filepath.Join(p, "bin")
		}
	}
	return ""
}

// managed reports whether dir belongs to a tool that rewrites its contents.
// Every version manager that shims executables — pyenv, rbenv, asdf, goenv —
// names the directory it rewrites "shims".
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

// resolution is the `go` PATH resolves to, and what it says about itself.
type resolution struct {
	dir  string // the PATH entry it was found in
	bin  string // the executable PATH resolves to
	root string // the GOROOT it reports
	err  error  // why it could not be asked
}

// resolveGo finds the `go` a later step would run and asks where its GOROOT
// is. It reports false when PATH holds no go at all.
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

// explain says why the resolved go is not the one wanted, in the terms needed
// to tell a shadowed link from a toolchain that will not answer.
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
	cmd := goCommand(bin, "env", "GOROOT")
	cmd.Env = withoutGoRoot(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// goCommand runs a toolchain entry point. CreateProcess cannot execute a .bat
// directly, so a shim written by writeShims has to go through cmd.
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
