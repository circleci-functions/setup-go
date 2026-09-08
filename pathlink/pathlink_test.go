package pathlink_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/setup-go/pathlink"
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

// The cimg/go case: GOPATH/bin is on PATH ahead of the image's toolchain, so
// that one is shadowed rather than disturbed.
func TestEnsure_ShadowsThePreinstalledGoFromAPreferredDir(t *testing.T) {
	goroot := fakeGoRoot(t)
	goPath, preinstalled := t.TempDir(), t.TempDir()
	early := filepath.Join(goPath, "bin")
	assert.NilError(t, os.MkdirAll(early, 0750))
	assert.NilError(t, os.WriteFile(filepath.Join(preinstalled, "go"), []byte("#!/bin/sh\n"), 0755))

	got, err := pathlink.Ensure(pathlink.Options{
		GoRoot: goroot,
		GoPath: goPath,
		Path:   strings.Join([]string{early, preinstalled}, string(os.PathListSeparator)),
	})
	assert.NilError(t, err)
	assert.Equal(t, got.Dir, early, "expected the link to go into GOPATH/bin, ahead of the preinstalled go")
	assert.Assert(t, got.Verified)

	target, err := os.Readlink(filepath.Join(early, "go"))
	assert.NilError(t, err)
	assert.Equal(t, target, filepath.Join(goroot, "bin", "go"))

	_, err = os.Stat(filepath.Join(early, "gofmt"))
	assert.NilError(t, err, "gofmt must be linked alongside go")

	fi, err := os.Lstat(filepath.Join(preinstalled, "go"))
	assert.NilError(t, err)
	assert.Assert(t, fi.Mode()&os.ModeSymlink == 0, "the preinstalled go must be left alone")
}

// With nothing preferred on PATH, the toolchain already there is taken over:
// the writable earlier entry belongs to some other tool.
func TestEnsure_TakesOverThePreinstalledGoWhenNothingIsPreferred(t *testing.T) {
	goroot := fakeGoRoot(t)
	unrelated, preinstalled := t.TempDir(), t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(preinstalled, "go"), []byte("#!/bin/sh\n"), 0755))

	got, err := pathlink.Ensure(pathlink.Options{
		GoRoot: goroot,
		Path:   strings.Join([]string{unrelated, preinstalled}, string(os.PathListSeparator)),
	})
	assert.NilError(t, err)
	assert.Equal(t, got.Dir, preinstalled)

	target, err := os.Readlink(filepath.Join(preinstalled, "go"))
	assert.NilError(t, err)
	assert.Equal(t, target, filepath.Join(goroot, "bin", "go"))

	_, err = os.Stat(filepath.Join(unrelated, "go"))
	assert.Assert(t, os.IsNotExist(err), "an unrelated directory on PATH must be left alone")
}

// Verification asks the resolved toolchain for its GOROOT rather than assuming
// the link it just made is what answers.
func TestEnsure_FailsWhenTheResolvedToolchainIsNotTheOneInstalled(t *testing.T) {
	goroot := fakeGoRoot(t)
	elsewhere := t.TempDir()
	script := "#!/bin/sh\n[ \"$1 $2\" = 'env GOROOT' ] && echo " + elsewhere + "\nexit 0\n"
	assert.NilError(t, os.WriteFile(filepath.Join(goroot, "bin", "go"), []byte(script), 0755))

	_, err := pathlink.Ensure(pathlink.Options{GoRoot: goroot, Path: t.TempDir()})
	// Without both, a shadowed link and a toolchain that will not answer look
	// the same in the log.
	assert.ErrorContains(t, err, "PATH resolves go to ")
	assert.ErrorContains(t, err, "whose GOROOT is ")
	assert.ErrorContains(t, err, elsewhere)
}

func TestEnsure_BinDirSkipsDiscoveryAndVerification(t *testing.T) {
	goroot := fakeGoRoot(t)
	binDir := t.TempDir()

	got, err := pathlink.Ensure(pathlink.Options{
		GoRoot: goroot,
		BinDir: binDir,
		Path:   t.TempDir(),
	})
	assert.NilError(t, err)
	assert.Equal(t, got.Dir, binDir)
	assert.Assert(t, !got.Verified, "a caller-chosen directory need not be on PATH, so nothing was verified")

	target, err := os.Readlink(filepath.Join(binDir, "go"))
	assert.NilError(t, err)
	assert.Equal(t, target, filepath.Join(goroot, "bin", "go"))
}

// Already resolvable — --detect-only, or a second run in one job.
func TestEnsure_NoopWhenPathAlreadyResolvesToTheToolchain(t *testing.T) {
	goroot := fakeGoRoot(t)

	got, err := pathlink.Ensure(pathlink.Options{GoRoot: goroot, Path: filepath.Join(goroot, "bin")})
	assert.NilError(t, err)
	assert.Equal(t, got.Dir, filepath.Join(goroot, "bin"))
	assert.Assert(t, got.Verified)
}

func TestEnsure_RequiresGoRoot(t *testing.T) {
	_, err := pathlink.Ensure(pathlink.Options{})
	assert.ErrorContains(t, err, "GoRoot is required")
}

// GOPATH/bin cannot hold links for binaries that do not exist yet, so a
// missing one becomes the directory PATH does hold.
func TestEnsure_PointsAMissingGoPathBinAtThePathDirectory(t *testing.T) {
	goroot := fakeGoRoot(t)
	early := t.TempDir()
	goPath := filepath.Join(t.TempDir(), "gopath")

	got, err := pathlink.Ensure(pathlink.Options{GoRoot: goroot, GoPath: goPath, Path: early})
	assert.NilError(t, err)

	target, err := os.Readlink(filepath.Join(goPath, "bin"))
	assert.NilError(t, err)
	assert.Equal(t, target, got.Dir)
}

func TestEnsure_LeavesAnExistingGoPathBinAlone(t *testing.T) {
	goroot := fakeGoRoot(t)
	goPath := t.TempDir()
	bin := filepath.Join(goPath, "bin")
	assert.NilError(t, os.MkdirAll(bin, 0750))

	_, err := pathlink.Ensure(pathlink.Options{GoRoot: goroot, GoPath: goPath, Path: t.TempDir()})
	assert.NilError(t, err)

	fi, err := os.Lstat(bin)
	assert.NilError(t, err)
	assert.Assert(t, fi.IsDir() && fi.Mode()&os.ModeSymlink == 0, "a job's own GOPATH/bin must not be replaced")
}

func TestSearchDirs_DropsEntriesNoLinkCanBeTrusted(t *testing.T) {
	path := strings.Join([]string{"/usr/bin", "", "relative/bin", "/usr/local/bin/"}, string(os.PathListSeparator))
	assert.DeepEqual(t, pathlink.SearchDirs(path), []string{"/usr/bin", "/usr/local/bin"})
}

func TestIndexOfGo(t *testing.T) {
	empty, withGo := t.TempDir(), t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(withGo, "go"), []byte("#!/bin/sh\n"), 0755))

	assert.Equal(t, pathlink.IndexOfGo([]string{empty, withGo}), 1)
	assert.Equal(t, pathlink.IndexOfGo([]string{empty}), -1)
}

func TestChooseDir(t *testing.T) {
	dirs := []string{"/first", "/second", "/third"}
	writable := func(names ...string) func(string) bool {
		return func(d string) bool {
			for _, n := range names {
				if d == n {
					return true
				}
			}
			return false
		}
	}

	tests := []struct {
		name        string
		existing    int
		preferred   []string
		writable    func(string) bool
		wantDir     string
		wantElevate bool
	}{
		{
			name:      "a preferred directory wins over an earlier entry",
			existing:  -1,
			preferred: []string{"/third"},
			writable:  writable("/first", "/third"),
			wantDir:   "/third",
		},
		{
			name:      "a preferred directory that is not on PATH is skipped",
			existing:  -1,
			preferred: []string{"/nowhere", "/second"},
			writable:  writable("/nowhere", "/second"),
			wantDir:   "/second",
		},
		{
			name:      "a preferred directory behind the existing go is skipped",
			existing:  1,
			preferred: []string{"/third", "/first"},
			writable:  writable("/first", "/third"),
			wantDir:   "/first",
		},
		{
			name:     "nothing preferred, so take over the existing go even though an earlier entry is writable",
			existing: 2,
			writable: writable("/second", "/third"),
			wantDir:  "/third",
		},
		{
			name:        "taking over a root-owned directory needs elevation",
			existing:    1,
			writable:    writable("/third"),
			wantDir:     "/second",
			wantElevate: true,
		},
		{
			name:     "no go on PATH at all, so the earliest writable entry",
			existing: -1,
			writable: writable("/second", "/third"),
			wantDir:  "/second",
		},
		{
			name:        "no go on PATH and nothing writable",
			existing:    -1,
			writable:    writable(),
			wantDir:     pathlink.DefaultBinDir,
			wantElevate: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir, elevate := pathlink.ChooseDir(dirs, tc.existing, tc.preferred, tc.writable)
			assert.Equal(t, dir, tc.wantDir)
			assert.Equal(t, elevate, tc.wantElevate)
		})
	}
}

// ~/.pyenv/shims is the first writable entry on the macOS executor's PATH, and
// pyenv rewrites it when it rehashes.
func TestChooseDir_SkipsDirectoriesOwnedByAVersionManager(t *testing.T) {
	dirs := []string{"/Users/distiller/.pyenv/shims", "/Users/distiller/.nvm/bin", "/usr/local/bin"}
	anything := func(string) bool { return true }

	dir, elevate := pathlink.ChooseDir(dirs, -1, nil, anything)
	assert.Equal(t, dir, "/usr/local/bin")
	assert.Assert(t, !elevate)
}

// Nor is a shim directory reached through the preference list.
func TestChooseDir_TakesOverTheExistingGoRatherThanAShimDir(t *testing.T) {
	dirs := []string{"/home/u/.pyenv/shims", "/usr/local/go/bin"}
	anything := func(string) bool { return true }

	dir, _ := pathlink.ChooseDir(dirs, 1, nil, anything)
	assert.Equal(t, dir, "/usr/local/go/bin")
}

func TestPreferredDirs_PutsGoPathBinFirst(t *testing.T) {
	got := pathlink.PreferredDirs("/home/u/go")
	assert.Equal(t, got[0], filepath.Join("/home/u/go", "bin"))
	assert.Equal(t, got[len(got)-1], pathlink.DefaultBinDir)
}

// The machine executor sets two GOPATH entries; `go install` uses the first.
func TestGoPathBin_TakesTheFirstEntryOfAList(t *testing.T) {
	list := strings.Join([]string{"/home/u/.go_workspace", "/usr/local/go_workspace"},
		string(os.PathListSeparator))

	assert.Equal(t, pathlink.GoPathBin(list), filepath.Join("/home/u/.go_workspace", "bin"))
	assert.Equal(t, pathlink.GoPathBin(""), "")
}

// What the Windows executor did when nothing was preferred: C:\tools\ruby34\bin
// was simply the first writable entry.
func TestChooseDir_PrefersTakingOverTheExistingGoToAnUnrelatedDir(t *testing.T) {
	dirs := []string{"/tools/ruby34/bin", "/usr/local/go/bin"}
	anything := func(string) bool { return true }

	dir, _ := pathlink.ChooseDir(dirs, 1, nil, anything)
	assert.Equal(t, dir, "/usr/local/go/bin")
}

func TestWritable(t *testing.T) {
	dir := t.TempDir()
	assert.Assert(t, pathlink.Writable(dir))

	missing := filepath.Join(dir, "not-yet")
	assert.Assert(t, pathlink.Writable(missing), "a PATH entry that can be created counts as writable")

	assert.NilError(t, os.Chmod(dir, 0500))       //nolint:gosec // a directory, and the point is that it is not writable
	t.Cleanup(func() { _ = os.Chmod(dir, 0750) }) //nolint:gosec // restores what t.TempDir needs to clean up
	assert.Assert(t, !pathlink.Writable(dir))
	assert.Assert(t, !pathlink.Writable(filepath.Join(dir, "child")))
}

// Taking over a directory leaves no backup behind once the link is in place.
func TestEnsure_ReplacesTheGoItTakesOver(t *testing.T) {
	goroot := fakeGoRoot(t)
	// A directory holding a go, writable enough to be chosen but not to be
	// written into, so the link fails after the original is moved aside.
	dir := t.TempDir()
	original := filepath.Join(dir, "go")
	assert.NilError(t, os.WriteFile(original, []byte("#!/bin/sh\necho original\n"), 0755))

	_, err := pathlink.Ensure(pathlink.Options{
		GoRoot: goroot,
		BinDir: dir,
		Path:   dir,
	})
	assert.NilError(t, err, "a writable directory should link cleanly")

	// The link replaced it, and no backup is left behind.
	target, err := os.Readlink(original)
	assert.NilError(t, err)
	assert.Equal(t, target, filepath.Join(goroot, "bin", "go"))

	_, err = os.Stat(original + ".setup-go-old")
	assert.Assert(t, os.IsNotExist(err), "the backup must be discarded once the link is in place")
}

// An unwritable directory fails before the go it holds is touched.
func TestEnsure_LeavesTheOriginalIntactWhenItCannotLink(t *testing.T) {
	goroot := fakeGoRoot(t)
	dir := t.TempDir()
	original := filepath.Join(dir, "go")
	assert.NilError(t, os.WriteFile(original, []byte("#!/bin/sh\necho original\n"), 0755))
	assert.NilError(t, os.Chmod(dir, 0500))       //nolint:gosec // the point is that it is not writable
	t.Cleanup(func() { _ = os.Chmod(dir, 0750) }) //nolint:gosec // restores what t.TempDir needs to clean up

	_, err := pathlink.Ensure(pathlink.Options{GoRoot: goroot, BinDir: dir, Path: dir})
	assert.Assert(t, err != nil, "expected linking into a read-only directory to fail")

	got, readErr := os.ReadFile(original) //nolint:gosec // a path this test wrote
	assert.NilError(t, readErr, "the go that was already there must still be there")
	assert.Assert(t, strings.Contains(string(got), "echo original"))
}
