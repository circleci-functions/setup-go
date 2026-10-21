package goroot

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/circleci-functions/toolkit/pathlink"
)

// reportGoPathBin makes what `go install` writes reachable, or says why it is
// not. Its binaries do not exist yet, so the directory itself has to be
// somewhere PATH already looks. Best effort: only the toolchain resolving is
// guaranteed. An empty linkedDir is one setup-go didn't link into, which it
// leaves alone.
func reportGoPathBin(goPath, linkedDir string, dirs []string) {
	bin := GoPathBin(goPath)
	if bin == "" || onPath(bin, dirs) {
		return
	}

	// Pointing a real directory elsewhere would move a job's own files.
	if _, err := os.Lstat(bin); err == nil {
		fmt.Fprintf(os.Stderr,
			"setup-go: %s is not on PATH, so binaries from `go install` need their full path\n", bin)
		return
	}

	// `go install` runs as the job's own user, so a directory it cannot write
	// to is no good here.
	if linkedDir == "" || !pathlink.Writable(linkedDir) {
		fmt.Fprintf(os.Stderr,
			"setup-go: %s is not on PATH, so binaries from `go install` need their full path\n", bin)
		return
	}

	// Nothing there yet, so point it at the directory PATH does hold.
	if err := os.MkdirAll(filepath.Dir(bin), 0755); err != nil { //nolint:gosec // GOPATH is readable by design
		return
	}
	if err := os.Symlink(linkedDir, bin); err != nil {
		fmt.Fprintf(os.Stderr,
			"setup-go: could not point %s at %s (%v), so binaries from `go install` need their full path\n",
			bin, linkedDir, err)
		return
	}
	fmt.Fprintf(os.Stderr, "setup-go: `go install` writes to %s, via %s\n", linkedDir, bin)
}

func onPath(dir string, dirs []string) bool {
	for _, d := range dirs {
		if sameDir(d, dir) {
			return true
		}
	}
	return false
}
