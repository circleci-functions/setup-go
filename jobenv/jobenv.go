// Package jobenv detects the installed Go toolchain's path and adds it to
// $BASH_ENV.
package jobenv

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/circleci-functions/toolkit/bashenv"
)

// DetectGoPath runs `go env GOPATH` using goBinary and returns the detected
// path. `go env` itself respects any GOPATH already set in the environment,
// so this reflects the effective GOPATH without setup-go inspecting or
// setting the variable itself.
func DetectGoPath(ctx context.Context, goBinary string) (string, error) {
	out, err := exec.CommandContext(ctx, goBinary, "env", "GOPATH").Output()
	if err != nil {
		return "", fmt.Errorf("go env: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// toUnixPath converts a native path to the Unix-style path that MSYS2/Git Bash
// expects in $BASH_ENV. On non-Windows it is a no-op.
// Example: C:\Users\foo → /c/Users/foo
func toUnixPath(path string) string {
	if runtime.GOOS != "windows" {
		return path
	}
	p := filepath.ToSlash(path)
	if len(p) >= 2 && p[1] == ':' {
		drive := strings.ToLower(string(p[0]))
		p = "/" + drive + p[2:]
	}
	return p
}

// WriteEnv prepends goRootBin and goPath's bin directory to PATH in
// $BASH_ENV, so subsequent job steps can find the installed go binary and
// anything installed via `go install`.
func WriteEnv(goPath, goRootBin string) error {
	return bashenv.AddToPath(fmt.Sprintf("%s:%s", toUnixPath(goRootBin), toUnixPath(filepath.Join(goPath, "bin"))))
}
