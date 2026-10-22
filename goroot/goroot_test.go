package goroot_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/setup-go/goroot"
)

// fakeGoRoot builds a GOROOT whose go reports it, so a test can assert which
// toolchain resolves, not just which file exists.
func fakeGoRoot(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stand-in for a toolchain is not executable on Windows")
	}

	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	assert.NilError(t, os.MkdirAll(bin, 0750))
	for _, name := range []string{"go", "gofmt"} {
		script := "#!/bin/sh\n[ \"$1 $2\" = 'env GOROOT' ] && echo " + root + "\nexit 0\n"
		assert.NilError(t, os.WriteFile(filepath.Join(bin, name), []byte(script), 0755))
	}
	return root
}

func TestLink_PrefersGoPathBin(t *testing.T) {
	root := fakeGoRoot(t)
	goPath, preinstalled := t.TempDir(), t.TempDir()
	early := filepath.Join(goPath, "bin")
	assert.NilError(t, os.MkdirAll(early, 0750))
	assert.NilError(t, os.WriteFile(filepath.Join(preinstalled, "go"), []byte("#!/bin/sh\n"), 0755))

	got, err := goroot.Link(goroot.Options{
		GoRoot: root,
		GoPath: goPath,
		Path:   strings.Join([]string{early, preinstalled}, string(os.PathListSeparator)),
	})
	assert.NilError(t, err)
	assert.Equal(t, got.Dir, early)
	assert.Assert(t, got.Verified)

	for _, name := range []string{"go", "gofmt"} {
		target, err := os.Readlink(filepath.Join(early, name))
		assert.NilError(t, err)
		assert.Equal(t, target, filepath.Join(root, "bin", name))
	}
}

// Linking first asks whatever go PATH already holds for its GOROOT. That is
// often the image's older toolchain, which must not switch to the version the
// job's go.mod asks for: it would download that toolchain into the module
// cache, silently, just to answer.
func TestLink_AsksThePreinstalledGoWithoutSwitchingToolchains(t *testing.T) {
	root := fakeGoRoot(t)
	goPath, preinstalled := t.TempDir(), t.TempDir()
	early := filepath.Join(goPath, "bin")
	assert.NilError(t, os.MkdirAll(early, 0750))
	seen := filepath.Join(t.TempDir(), "gotoolchain")
	script := "#!/bin/sh\necho \"$GOTOOLCHAIN\" > " + seen + "\n"
	assert.NilError(t, os.WriteFile(filepath.Join(preinstalled, "go"), []byte(script), 0755))
	t.Setenv("GOTOOLCHAIN", "auto")

	_, err := goroot.Link(goroot.Options{
		GoRoot: root,
		GoPath: goPath,
		Path:   strings.Join([]string{preinstalled, early}, string(os.PathListSeparator)),
	})
	assert.NilError(t, err)

	got, err := os.ReadFile(seen)
	assert.NilError(t, err)
	assert.Equal(t, strings.TrimSpace(string(got)), "local")
}

// Verification asks the resolved toolchain for its GOROOT rather than assuming
// the link it just made is what answers.
func TestLink_FailsWhenTheResolvedToolchainIsNotTheOneInstalled(t *testing.T) {
	root := fakeGoRoot(t)
	elsewhere := t.TempDir()
	script := "#!/bin/sh\n[ \"$1 $2\" = 'env GOROOT' ] && echo " + elsewhere + "\nexit 0\n"
	assert.NilError(t, os.WriteFile(filepath.Join(root, "bin", "go"), []byte(script), 0755))

	_, err := goroot.Link(goroot.Options{GoRoot: root, Path: t.TempDir()})
	assert.ErrorContains(t, err, "has GOROOT ")
	assert.ErrorContains(t, err, elsewhere)
}

func TestLink_RequiresGoRoot(t *testing.T) {
	_, err := goroot.Link(goroot.Options{})
	assert.ErrorContains(t, err, "GoRoot is required")
}

// GOPATH/bin cannot hold links for binaries that do not exist yet, so a
// missing one becomes the directory PATH does hold.
func TestLink_PointsAMissingGoPathBinAtThePathDirectory(t *testing.T) {
	root := fakeGoRoot(t)
	early := t.TempDir()
	goPath := filepath.Join(t.TempDir(), "gopath")

	got, err := goroot.Link(goroot.Options{GoRoot: root, GoPath: goPath, Path: early})
	assert.NilError(t, err)

	target, err := os.Readlink(filepath.Join(goPath, "bin"))
	assert.NilError(t, err)
	assert.Equal(t, target, got.Dir)
}

// Under --detect-only the toolchain is already on PATH, from a directory that
// belongs to Homebrew or a distro package. GOPATH/bin must not become that
// directory, or `go install` writes among their binaries.
func TestLink_LeavesGoPathBinAloneForAToolchainAlreadyOnPath(t *testing.T) {
	root := fakeGoRoot(t)
	goPath := filepath.Join(t.TempDir(), "gopath")
	bin := filepath.Join(root, "bin")

	got, err := goroot.Link(goroot.Options{GoRoot: root, GoPath: goPath, Path: bin})
	assert.NilError(t, err)
	assert.Equal(t, got.Dir, bin)

	_, err = os.Lstat(filepath.Join(goPath, "bin"))
	assert.Assert(t, os.IsNotExist(err), "GOPATH/bin must not point at the toolchain's directory")
}

func TestLink_LeavesAnExistingGoPathBinAlone(t *testing.T) {
	root := fakeGoRoot(t)
	goPath := t.TempDir()
	bin := filepath.Join(goPath, "bin")
	assert.NilError(t, os.MkdirAll(bin, 0750))

	_, err := goroot.Link(goroot.Options{GoRoot: root, GoPath: goPath, Path: t.TempDir()})
	assert.NilError(t, err)

	fi, err := os.Lstat(bin)
	assert.NilError(t, err)
	assert.Assert(t, fi.IsDir() && fi.Mode()&os.ModeSymlink == 0, "a job's own GOPATH/bin must not be replaced")
}

// The machine executor sets two GOPATH entries; `go install` uses the first.
func TestGoPathBin_TakesTheFirstEntryOfAList(t *testing.T) {
	list := strings.Join([]string{"/home/u/.go_workspace", "/usr/local/go_workspace"},
		string(os.PathListSeparator))

	assert.Equal(t, goroot.GoPathBin(list), filepath.Join("/home/u/.go_workspace", "bin"))
	assert.Equal(t, goroot.GoPathBin(""), "")
}
