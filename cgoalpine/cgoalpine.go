// Package cgoalpine detects Alpine Linux and installs the C toolchain
// (gcc, musl-dev) that CGO_ENABLED=1 builds need but stock Alpine images
// don't ship.
package cgoalpine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// DefaultOSReleasePath is the standard location of the os-release file.
const DefaultOSReleasePath = "/etc/os-release"

// IsAlpine reports whether osReleasePath identifies the host as Alpine Linux,
// per the os-release(5) ID field. A missing file is treated as "not Alpine"
// rather than an error, since hosts without one (e.g. macOS) obviously aren't.
func IsAlpine(osReleasePath string) (bool, error) {
	f, err := os.Open(osReleasePath) //nolint:gosec // osReleasePath is trusted configuration
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open %s: %w", osReleasePath, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		id, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), "ID=")
		if !ok {
			continue
		}
		return strings.Trim(id, `"`) == "alpine", nil
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("read %s: %w", osReleasePath, err)
	}
	return false, nil
}

// InstallToolchain installs gcc and musl-dev via apk, the C toolchain cgo
// needs to compile on Alpine. It's a no-op on non-Alpine hosts, per
// osReleasePath. It shells out to "apk" on PATH.
func InstallToolchain(ctx context.Context, osReleasePath string) error {
	isAlpine, err := IsAlpine(osReleasePath)
	if err != nil {
		return err
	}

	if !isAlpine {
		return nil
	}

	cmd := exec.CommandContext(ctx, "apk", "add", "gcc", "musl-dev")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("apk add gcc musl-dev: %w", err)
	}

	return nil
}
