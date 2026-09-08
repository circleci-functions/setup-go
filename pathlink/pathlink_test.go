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
// toolchain PATH resolves to rather than only which file exists.
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

func TestEnsure_LinksIntoAWritableDirAheadOfTheExistingGo(t *testing.T) {
	goroot := fakeGoRoot(t)
	early, preinstalled := t.TempDir(), t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(preinstalled, "go"), []byte("#!/bin/sh\n"), 0755))

	got, err := pathlink.Ensure(pathlink.Options{
		GoRoot: goroot,
		Path:   strings.Join([]string{early, preinstalled}, string(os.PathListSeparator)),
	})
	assert.NilError(t, err)
	assert.Equal(t, got.Dir, early, "expected the link to go ahead of the preinstalled go")
	assert.Assert(t, got.Verified)

	target, err := os.Readlink(filepath.Join(early, "go"))
	assert.NilError(t, err)
	assert.Equal(t, target, filepath.Join(goroot, "bin", "go"))

	_, err = os.Stat(filepath.Join(early, "gofmt"))
	assert.NilError(t, err, "gofmt must be linked alongside go")

	// The preinstalled toolchain is shadowed, not replaced.
	fi, err := os.Lstat(filepath.Join(preinstalled, "go"))
	assert.NilError(t, err)
	assert.Assert(t, fi.Mode()&os.ModeSymlink == 0, "the preinstalled go must be left alone")
}

// Verification asks the toolchain PATH resolves where its GOROOT is, rather
// than assuming the link it just made is the one that answers. A toolchain
// reporting some other GOROOT stands in for anything that gets in the way:
// PATHEXT, a directory that turns out not to be searched, a stale shim.
func TestEnsure_FailsWhenTheResolvedToolchainIsNotTheOneInstalled(t *testing.T) {
	goroot := fakeGoRoot(t)
	elsewhere := t.TempDir()
	script := "#!/bin/sh\n[ \"$1 $2\" = 'env GOROOT' ] && echo " + elsewhere + "\nexit 0\n"
	assert.NilError(t, os.WriteFile(filepath.Join(goroot, "bin", "go"), []byte(script), 0755))

	_, err := pathlink.Ensure(pathlink.Options{GoRoot: goroot, Path: t.TempDir()})
	assert.ErrorContains(t, err, "PATH still resolves go elsewhere")
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

// A toolchain already resolvable — --detect-only, or a second run in one job —
// needs no link at all.
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

// GOPATH/bin cannot hold links for binaries that do not exist yet, so when it
// is missing it becomes the directory the toolchain was linked into — which
// PATH does hold, so `go install` writes straight onto PATH.
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
		writable    func(string) bool
		wantDir     string
		wantElevate bool
	}{
		{
			name:     "earliest writable entry ahead of the existing go",
			existing: 2,
			writable: writable("/second", "/third"),
			wantDir:  "/second",
		},
		{
			name:        "nothing writable ahead of it, so replace it in place",
			existing:    1,
			writable:    writable("/third"),
			wantDir:     "/second",
			wantElevate: true,
		},
		{
			name:     "no go on PATH at all, so any writable entry will do",
			existing: -1,
			writable: writable("/third"),
			wantDir:  "/third",
		},
		{
			name:        "no go and nothing writable",
			existing:    -1,
			writable:    writable(),
			wantDir:     pathlink.DefaultBinDir,
			wantElevate: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir, elevate := pathlink.ChooseDir(dirs, tc.existing, tc.writable)
			assert.Equal(t, dir, tc.wantDir)
			assert.Equal(t, elevate, tc.wantElevate)
		})
	}
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
