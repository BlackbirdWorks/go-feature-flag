package controlplane_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDependencyClosure asserts that the production files import only the standard library and yaml.v3.
func TestDependencyClosure(t *testing.T) {
	t.Parallel()

	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps",
		"-f", `{{if not .Standard}}{{.ImportPath}}{{end}}`, ".").Output()
	require.NoError(t, err)

	got := strings.Fields(string(out))
	assert.ElementsMatch(t, []string{
		"github.com/blackbirdworks/semaphore/controlplane",
		"gopkg.in/yaml.v3",
	}, got)
}
