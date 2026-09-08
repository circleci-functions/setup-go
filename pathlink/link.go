package pathlink

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// linkInto points dir/go and dir/gofmt at goroot's copies. When elevate is set
// the links are made with sudo, which is what replacing a preinstalled
// toolchain in a root-owned directory takes.
//
// A symlink rather than a copy: a Go toolchain locates GOROOT from its own
// executable path, resolving symlinks on the way, so a symlinked go finds
// goroot while a copy would look for a toolchain beside the copy.
func linkInto(dir, goroot string, elevate bool) error {
	if !elevate {
		if err := os.MkdirAll(dir, 0755); err != nil { //nolint:gosec // a directory on PATH is readable by design
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	for _, name := range linked {
		src := filepath.Join(goroot, "bin", exeName(name))
		dst := filepath.Join(dir, exeName(name))
		if err := linkFile(src, dst, elevate); err != nil {
			return fmt.Errorf("link %s: %w", dst, err)
		}
	}
	return nil
}

func linkFile(src, dst string, elevate bool) error {
	// PATH already points at the toolchain itself, so there is nothing to
	// link and every reason not to: a symlink onto its own target would leave
	// the job with no working go at all.
	if src == dst {
		return nil
	}
	if elevate {
		return sudoSymlink(src, dst)
	}
	_ = os.Remove(dst)
	err := os.Symlink(src, dst)
	if err == nil {
		return nil
	}
	// Windows withholds symlink creation from unprivileged processes; a shim
	// forwarding to the absolute path keeps GOROOT detection intact.
	if runtime.GOOS == "windows" {
		return writeShims(src, dst)
	}
	return err
}

// sudoSymlink replaces a root-owned entry on PATH. -n so a job without
// passwordless sudo fails here rather than blocking on a password prompt no
// one is there to answer.
func sudoSymlink(src, dst string) error {
	if out, err := exec.Command("sudo", "-n", "ln", "-sf", src, dst).CombinedOutput(); err != nil {
		return fmt.Errorf("sudo ln: %s", firstLine(out))
	}
	return nil
}

// writeShims stands in for a symlink on Windows, where creating one needs a
// privilege a job does not always hold. Two shims, because a job's steps do not
// all run in the same shell: a .bat for cmd and PowerShell, and an
// extensionless script for the Git Bash that a bash-shelled step uses.
func writeShims(src, dst string) error {
	base := dst[:len(dst)-len(filepath.Ext(dst))]

	bat := fmt.Sprintf("@echo off\r\n\"%s\" %%*\r\n", src)
	if err := os.WriteFile(base+".bat", []byte(bat), 0755); err != nil { //nolint:gosec // must be executable
		return err
	}

	sh := fmt.Sprintf("#!/bin/sh\nexec \"%s\" \"$@\"\n", filepath.ToSlash(src))
	return os.WriteFile(base, []byte(sh), 0755) //nolint:gosec // must be executable
}

// reportGoPathBin makes what `go install` writes reachable by name, or explains
// why it is not. GOPATH/bin cannot hold a link the way a PATH directory can —
// the binaries do not exist yet — so the directory itself has to be somewhere
// PATH already looks.
//
// Best effort by design: the toolchain resolving is the guarantee this package
// makes, and a job that never runs `go install` does not care about this.
func reportGoPathBin(goPath, linkedDir string, dirs []string, elevated bool) {
	if goPath == "" {
		return
	}
	bin := filepath.Join(goPath, "bin")
	if onPath(bin, dirs) {
		return
	}

	// A real directory already there cannot be pointed elsewhere without
	// moving a job's own files, so say so rather than guess.
	if _, err := os.Lstat(bin); err == nil {
		fmt.Fprintf(os.Stderr,
			"setup-go: %s is not on PATH, so binaries from `go install` need their full path\n", bin)
		return
	}

	// A directory that took elevation to link into is no good here: `go
	// install` runs as the job's own user and would fail to write to it.
	if elevated {
		fmt.Fprintf(os.Stderr,
			"setup-go: %s is not on PATH, so binaries from `go install` need their full path\n", bin)
		return
	}

	// Nothing there yet: make it the directory `go` was linked into, which PATH
	// does hold, so `go install` writes straight onto PATH.
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

func firstLine(b []byte) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return line
}
