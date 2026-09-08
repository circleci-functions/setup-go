package goenv

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// GoEnv holds the `go env` variables setup-go uses. Other variables reported by
// `go env` are silently ignored by the JSON unmarshaller.
type GoEnv struct {
	GOCACHE    string `json:"GOCACHE"`
	GOMODCACHE string `json:"GOMODCACHE"`
	// GOPATH is whatever the toolchain resolves it to, which respects a GOPATH
	// already set in the job — setup-go neither inspects nor sets it.
	GOPATH string `json:"GOPATH"`
}

// LoadFrom runs `go env -json` using a specific go binary. Callers that have
// just installed a toolchain must pass that toolchain's binary: it is not
// resolvable by name until pathlink has linked it onto PATH.
func LoadFrom(ctx context.Context, goBinary string) (*GoEnv, error) {
	out, err := exec.CommandContext(ctx, goBinary, "env", "-json").Output()
	if err != nil {
		return nil, fmt.Errorf("go env -json: %w", err)
	}
	var e GoEnv
	if err := json.Unmarshal(out, &e); err != nil {
		return nil, fmt.Errorf("parsing go env JSON: %w", err)
	}
	return &e, nil
}
