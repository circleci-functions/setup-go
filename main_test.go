package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExpandHome covers the spellings a config author can write, including the
// ones kong passes through unexpanded because no shell touched them.
func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"slash", "~/.cache/setup-go", filepath.Join(home, ".cache", "setup-go")},
		{"bare tilde", "~", home},
		{"native separator", "~" + string(filepath.Separator) + "builds", filepath.Join(home, "builds")},
		{"absolute path", filepath.Join("/tmp", "go"), filepath.Join("/tmp", "go")},
		{"other user's home is not expanded", "~someone/go", "~someone/go"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expandHome(tt.input); got != tt.want {
				t.Errorf("expandHome(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
