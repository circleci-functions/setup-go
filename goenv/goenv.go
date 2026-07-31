package goenv

import (
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

// Load runs `go env -json` and unmarshals the result into a GoEnv struct.
func Load() (*GoEnv, error) {
	out, err := exec.Command("go", "env", "-json").Output()
	if err != nil {
		return nil, fmt.Errorf("go env -json: %w", err)
	}
	var e GoEnv
	if err := json.Unmarshal(out, &e); err != nil {
		return nil, fmt.Errorf("parsing go env JSON: %w", err)
	}
	return &e, nil
}
