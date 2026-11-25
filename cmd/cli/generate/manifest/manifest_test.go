package manifest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/generate/manifest"
)

func TestNewManifest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		config         string
		wantErr        string
		wantManifest   string
		setDestination bool
	}{
		{
			name:           "should error if config file does not exist",
			config:         "testdata/invalid.yaml",
			setDestination: true,
			wantErr:        "impossible to find config file testdata/invalid.yaml",
		},
		{
			name:           "should generate the manifest if config file exists",
			config:         "testdata/input/flag.goff.yaml",
			setDestination: true,
			wantManifest:   "testdata/output/flag.goff.json",
		},
		{
			name:           "should error if no destination provided",
			config:         "testdata/input/flag.goff.yaml",
			setDestination: false,
			wantErr:        "--flag_manifest_destination is mandatory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			destination := ""
			if tt.setDestination {
				destination = filepath.Join(t.TempDir(), "manifest.json")
			}

			mfst, err := manifest.NewManifest(tt.config, "", destination)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)

			_, err = mfst.Generate()
			require.NoError(t, err)
			want, err := os.ReadFile(tt.wantManifest)
			require.NoError(t, err)
			got, err := os.ReadFile(destination)
			require.NoError(t, err)
			assert.Equal(t, string(want), string(got))
		})
	}
}
