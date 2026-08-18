//go:build acceptance

package setupgo_test

import (
	"os"
	"regexp"
	"testing"

	"gotest.tools/v3/assert"
)

var bashEnvPathExport = regexp.MustCompile(`(?m)^export PATH=(.+):\$PATH$`)

// parseBashEnvPATH extracts the PATH prefix from the last `export
// PATH=<prefix>:$PATH` line jobenv.WriteEnv appended to bashEnvFile. Good
// enough today because this suite only ever installs one Go version per
// scenario; a scenario installing two *different* versions back-to-back
// would need to fold every export line in file order, not just the last.
func parseBashEnvPATH(t *testing.T, bashEnvFile string) string {
	t.Helper()
	content, err := os.ReadFile(bashEnvFile) //nolint:gosec
	assert.NilError(t, err)

	matches := bashEnvPathExport.FindAllStringSubmatch(string(content), -1)
	assert.Assert(t, len(matches) > 0, "expected an exported PATH in %s, got:\n%s", bashEnvFile, content)

	return matches[len(matches)-1][1]
}
