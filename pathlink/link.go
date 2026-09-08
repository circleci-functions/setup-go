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

	// Anything already there is moved aside rather than deleted, so a failure
	// below leaves the executor with the toolchain it had. Taking over the
	// directory a preinstalled toolchain lives in means dst is that
	// toolchain's own binary.
	restore, err := moveAside(dst)
	if err != nil {
		return err
	}

	// Windows gets shims rather than a symlink. A Go toolchain works out its
	// GOROOT from where its own executable sits, and taking over a
	// preinstalled toolchain means linking into that toolchain's bin
	// directory — so a symlink there resolves GOROOT to the old toolchain
	// beside it. A shim invoking the absolute path cannot: it runs the real
	// binary, in its own GOROOT. It is also found by name whatever the shell,
	// and needs no symlink privilege.
	//
	// moveAside has taken any competing go.exe out of the way, which PATHEXT
	// would otherwise prefer to a .bat.
	link := os.Symlink
	if runtime.GOOS == "windows" {
		link = writeShims
	}
	if err := link(src, dst); err != nil {
		return errors.Join(err, restore.undo())
	}
	return restore.commit()
}

// backup is a file moved out of the way, restorable until committed.
type backup struct{ from, to string }

func moveAside(path string) (backup, error) {
	if _, err := os.Lstat(path); err != nil {
		return backup{}, nil
	}
	aside := path + ".setup-go-old"
	_ = os.Remove(aside)
	if err := os.Rename(path, aside); err != nil {
		return backup{}, fmt.Errorf("move %s aside: %w", path, err)
	}
	return backup{from: aside, to: path}, nil
}

// commit discards the backup, leaving the link in place.
func (b backup) commit() error {
	if b.from != "" {
		_ = os.Remove(b.from)
	}
	return nil
}

// undo puts the original back, for a failure that would otherwise leave the
// job with no go at all.
func (b backup) undo() error {
	if b.from == "" {
		return nil
	}
	if err := os.Rename(b.from, b.to); err != nil {
		return fmt.Errorf("restore %s: %w", b.to, err)
	}
	return nil
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
	bin := GoPathBin(goPath)
	if bin == "" {
		return
	}
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
