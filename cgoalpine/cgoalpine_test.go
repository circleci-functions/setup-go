package cgoalpine_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/setup-go/cgoalpine"
)

func writeOSRelease(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "os-release")
	assert.NilError(t, os.WriteFile(path, []byte(content), 0600))
	return path
}

func TestIsAlpine_Alpine(t *testing.T) {
	path := writeOSRelease(t, "NAME=\"Alpine Linux\"\nID=alpine\nVERSION_ID=3.20.0\n")

	got, err := cgoalpine.IsAlpine(path)
	assert.NilError(t, err)
	assert.Assert(t, got)
}

func TestIsAlpine_QuotedID(t *testing.T) {
	path := writeOSRelease(t, "ID=\"alpine\"\n")

	got, err := cgoalpine.IsAlpine(path)
	assert.NilError(t, err)
	assert.Assert(t, got)
}

func TestIsAlpine_OtherDistro(t *testing.T) {
	path := writeOSRelease(t, "NAME=\"Ubuntu\"\nID=ubuntu\nVERSION_ID=\"22.04\"\n")

	got, err := cgoalpine.IsAlpine(path)
	assert.NilError(t, err)
	assert.Assert(t, !got)
}

func TestIsAlpine_NoIDField(t *testing.T) {
	path := writeOSRelease(t, "NAME=\"Mystery\"\n")

	got, err := cgoalpine.IsAlpine(path)
	assert.NilError(t, err)
	assert.Assert(t, !got)
}

func TestIsAlpine_MissingFile(t *testing.T) {
	got, err := cgoalpine.IsAlpine(filepath.Join(t.TempDir(), "does-not-exist"))
	assert.NilError(t, err)
	assert.Assert(t, !got)
}

func TestInstallToolchain_AlpineInvokesApk(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "apk-args")
	stub := "#!/bin/sh\necho \"$@\" > " + callLog + "\n"
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "apk"), []byte(stub), 0750))

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	osReleasePath := writeOSRelease(t, "ID=alpine\n")
	assert.NilError(t, cgoalpine.InstallToolchain(context.Background(), osReleasePath))

	got, err := os.ReadFile(callLog)
	assert.NilError(t, err)
	assert.Equal(t, string(got), "add gcc musl-dev\n")
}

func TestInstallToolchain_NonAlpineSkipsApk(t *testing.T) {
	osReleasePath := writeOSRelease(t, "ID=ubuntu\n")
	assert.NilError(t, cgoalpine.InstallToolchain(context.Background(), osReleasePath))
}
