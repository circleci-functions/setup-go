package goenv

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// GoEnv holds all variables reported by `go env`.
// Unknown or future variables are silently ignored by the JSON unmarshaller.
type GoEnv struct {
	GOCACHE    string `json:"GOCACHE"`
	GOENV      string `json:"GOENV"`
	GOMODCACHE string `json:"GOMODCACHE"`
	GOPATH     string `json:"GOPATH"`
	GOVERSION  string `json:"GOVERSION"`
}

// Load runs `go env -json` using the go on PATH.
func Load() (*GoEnv, error) {
	return LoadFrom(context.Background(), "go")
}

// LoadFrom runs `go env -json` using a specific go binary. Callers that have
// just installed a toolchain must use this: the install writes its PATH exports
// for later steps, so the new go is not yet resolvable by name.
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
