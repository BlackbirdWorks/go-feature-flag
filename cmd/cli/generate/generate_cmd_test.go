package generate_test

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/pterm/pterm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/generate"
	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

const cmdManifest = "manifest"

func TestGenerateCmd(t *testing.T) {
	t.Parallel()

	manifestOut := filepath.Join(t.TempDir(), "manifest.json")

	tests := []struct {
		name        string
		expectedErr string
		expectedOut string
		args        []string
		wantErr     bool
	}{
		{
			name:        "no subcommand provided",
			args:        []string{},
			wantErr:     true,
			expectedErr: `expected "manifest"`,
		},
		{
			name:        "root help",
			args:        []string{"--help"},
			wantErr:     false,
			expectedOut: cmdManifest,
		},
		{
			name:        "manifest subcommand help",
			args:        []string{cmdManifest, "--help"},
			wantErr:     false,
			expectedOut: "flag_manifest_destination",
		},
		{
			name: "manifest subcommand executes",
			args: []string{
				cmdManifest,
				"--config=manifest/testdata/input/flag.goff.yaml",
				"--flag_manifest_destination",
				manifestOut,
			},
			wantErr:     false,
			expectedOut: "Manifest has been created",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			err := generate.Run(tt.args, &helper.Streams{Stdout: &stdout, Stderr: &stderr})

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				require.NoError(t, err)
			}

			if tt.expectedOut != "" {
				assert.Contains(t, pterm.RemoveColorFromString(stdout.String()), tt.expectedOut)
			}

			assert.Empty(t, stderr.String())
		})
	}
}
