package goenv

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// GoEnv holds the `go env` variables setup-go uses. Other variables reported by
// `go env` are silently ignored by the JSON unmarshaller.
type GoEnv struct {
	GOCACHE    string `json:"GOCACHE"`
	GOMODCACHE string `json:"GOMODCACHE"`
	// GOPATH as the toolchain resolves it, respecting any already set in the
	// job; setup-go neither inspects nor sets it.
	GOPATH string `json:"GOPATH"`
}

// LoadFrom runs `go env -json` with a specific go binary. A caller that just
// installed a toolchain must pass that binary: it is not resolvable by name
// until pathlink has linked it.
//
// GOTOOLCHAIN=local keeps the answer that binary's own: if the job's go.mod
// asks for a newer Go, the binary must not switch to (and download) it.
func LoadFrom(ctx context.Context, goBinary string) (*GoEnv, error) {
	cmd := exec.CommandContext(ctx, goBinary, "env", "-json")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go env -json: %w", err)
	}
	var e GoEnv
	if err := json.Unmarshal(out, &e); err != nil {
		return nil, fmt.Errorf("parsing go env JSON: %w", err)
	}
	return &e, nil
}
