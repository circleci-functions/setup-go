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

// linkInto points dir/go and dir/gofmt at goroot's copies, with sudo when
// elevate is set.
//
// A symlink rather than a copy: a toolchain finds GOROOT from its own
// executable path, resolving symlinks, so a copy would look for a toolchain
// beside itself.
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

	// Moved aside rather than deleted: taking over a preinstalled toolchain
	// means dst is its own binary, and a failure below must leave it working.
	restore, err := moveAside(dst)
	if err != nil {
		return err
	}

	// Windows gets shims, not a symlink: taking over a preinstalled toolchain
	// links into its bin directory, where a symlink would resolve GOROOT to the
	// old toolchain beside it. A shim invokes the absolute path instead, needs
	// no symlink privilege, and — since moveAside removed any competing go.exe
	// that PATHEXT would prefer — is found by name in any shell.
	link := os.Symlink
	if runtime.GOOS == "windows" {
		link = writeShims
	}
	if err := link(src, dst); err != nil {
		return errors.Join(err, restore.undo())
	}
	return restore.commit()
}

// backup is a file moved aside, restorable until committed.
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

// undo puts the original back, rather than leaving the job with no go.
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
// passwordless sudo fails rather than blocking on a prompt.
func sudoSymlink(src, dst string) error {
	if out, err := exec.Command("sudo", "-n", "ln", "-sf", src, dst).CombinedOutput(); err != nil {
		return fmt.Errorf("sudo ln: %s", firstLine(out))
	}
	return nil
}

// writeShims writes two, because a job's steps do not all run in the same
// shell: a .bat for cmd and PowerShell, and an extensionless script for Git
// Bash.
func writeShims(src, dst string) error {
	base := dst[:len(dst)-len(filepath.Ext(dst))]

	bat := fmt.Sprintf("@echo off\r\n\"%s\" %%*\r\n", src)
	if err := os.WriteFile(base+".bat", []byte(bat), 0755); err != nil { //nolint:gosec // must be executable
		return err
	}

	sh := fmt.Sprintf("#!/bin/sh\nexec \"%s\" \"$@\"\n", filepath.ToSlash(src))
	return os.WriteFile(base, []byte(sh), 0755) //nolint:gosec // must be executable
}

// reportGoPathBin makes what `go install` writes reachable, or says why it is
// not. Its binaries do not exist yet, so the directory itself has to be
// somewhere PATH already looks. Best effort: only the toolchain resolving is
// guaranteed.
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

	// Pointing a real directory elsewhere would move a job's own files.
	if _, err := os.Lstat(bin); err == nil {
		fmt.Fprintf(os.Stderr,
			"setup-go: %s is not on PATH, so binaries from `go install` need their full path\n", bin)
		return
	}

	// `go install` runs as the job's own user, so a directory that took
	// elevation is no good here.
	if elevated {
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

func firstLine(b []byte) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return line
}
