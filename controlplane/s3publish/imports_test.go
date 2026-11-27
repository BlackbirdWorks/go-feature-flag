package s3publish_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoEngineAndNoInternalPackagesAreImported asserts that an importer of s3publish does not pull the engine,
// the flag checker or any internal package of the repository: only controlplane, the AWS SDK and the libraries
// they need.
func TestNoEngineAndNoInternalPackagesAreImported(t *testing.T) {
	t.Parallel()

	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps",
		"-f", `{{if not .Standard}}{{.ImportPath}}{{end}}`, ".").Output()
	require.NoError(t, err)

	var own []string

	for path := range strings.FieldsSeq(string(out)) {
		if strings.HasPrefix(path, "github.com/blackbirdworks/semaphore") {
			own = append(own, path)
		}
	}

	assert.ElementsMatch(t, []string{
		"github.com/blackbirdworks/semaphore/controlplane",
		"github.com/blackbirdworks/semaphore/controlplane/s3publish",
	}, own)
}
