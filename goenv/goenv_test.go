package goenv_test

import (
	"encoding/json"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/setup-go/goenv"
)

// TestLoad runs the real `go env -json` (no network access involved) and
// checks a couple of fields against known-good values, to catch e.g. the
// exec/JSON-decode plumbing breaking.
func TestLoad(t *testing.T) {
	e, err := goenv.Load()
	assert.NilError(t, err)
	assert.Assert(t, e.GOMODCACHE != "", "expected GOMODCACHE to be set")
}

// TestLoad_GoNotOnPath verifies Load surfaces a clear error rather than
// panicking or returning a zero-value GoEnv when `go` isn't on PATH.
func TestLoad_GoNotOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := goenv.Load()
	assert.ErrorContains(t, err, "go env -json")
}

// TestGoEnv_JSONUnmarshal is a table test over encoding/json's behavior
// against the GoEnv struct tags, so a typo'd tag (which would silently zero
// that field against the real `go env -json` output) fails a test instead of
// only showing up at runtime.
func TestGoEnv_JSONUnmarshal(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  goenv.GoEnv
	}{
		{
			name:  "PopulatesKnownFields",
			input: `{"GOMODCACHE":"/home/x/go/pkg/mod","GOPATH":"/home/x/go"}`,
			want:  goenv.GoEnv{GOMODCACHE: "/home/x/go/pkg/mod", GOPATH: "/home/x/go"},
		},
		{
			name:  "IgnoresUnknownFields",
			input: `{"GOMODCACHE":"/home/x/go/pkg/mod","SOME_FUTURE_GO_VAR":"value"}`,
			want:  goenv.GoEnv{GOMODCACHE: "/home/x/go/pkg/mod"},
		},
		{
			name:  "EmptyObject",
			input: `{}`,
			want:  goenv.GoEnv{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got goenv.GoEnv
			assert.NilError(t, json.Unmarshal([]byte(tt.input), &got))
			assert.DeepEqual(t, got, tt.want)
		})
	}
}
