package manifest_test

import (
	"os"
	"testing"

	"github.com/pterm/pterm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/generate/manifest"
)

const (
	infoManifestHasBeenCreated = "INFO: 🎉 Manifest has been created\n"
)

func TestManifestCmd(t *testing.T) {
	t.Parallel()

	pterm.DisableStyling()
	pterm.DisableColor()

	tests := []struct {
		assertError      assert.ErrorAssertionFunc
		name             string
		expectedManifest string
		expectedOutput   string
		expectedError    string
		args             []string
	}{
		{
			name:             "should return success if everything is ok",
			args:             []string{"--config=testdata/input/flag.goff.yaml"},
			expectedManifest: "testdata/output/flag.goff.json",
			expectedOutput:   infoManifestHasBeenCreated,
			assertError:      assert.NoError,
		},
		{
			name:             "should ignore flag without value",
			args:             []string{"--config=testdata/input/flag-no-default.yaml"},
			expectedManifest: "testdata/output/flag-no-default.json",
			expectedOutput:   "WARNING: flag ignored: no default value provided flag=test-flag\nINFO: 🎉 Manifest has been created\n",
			assertError:      assert.NoError,
		},
		{
			name:        "should error if flag type is invalid",
			args:        []string{"--config=testdata/input/flag-invalid-flag-type.yaml"},
			assertError: assert.Error,
			expectedError: "generating the definitions of the manifest: invalid configuration for flag test-flag: " +
				"impossible to find type",
		},
		{
			name:             "should have int as type if float with .0 and int are mixed",
			args:             []string{"--config=testdata/input/flag-int-as-float.yaml"},
			assertError:      assert.NoError,
			expectedManifest: "testdata/output/flag-int-as-float.json",
			expectedOutput:   infoManifestHasBeenCreated,
		},
		{
			name:             "should have float as type if 1 float and int are mixed",
			args:             []string{"--config=testdata/input/flag-float-and-int.yaml"},
			assertError:      assert.NoError,
			expectedManifest: "testdata/output/flag-float-and-int.json",
			expectedOutput:   infoManifestHasBeenCreated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpManifest, err := os.CreateTemp(t.TempDir(), "temp")
			require.NoError(t, err)
			os.Remove(tmpManifest.Name()) // delete, we only want the path

			redirectionStd, err := os.CreateTemp(t.TempDir(), "temp")
			require.NoError(t, err)

			defer func() { _ = os.Remove(redirectionStd.Name()) }()

			tt.args = append(tt.args, "--flag_manifest_destination", tmpManifest.Name())

			cmd := manifest.NewManifestCmd()
			cmd.SetErr(redirectionStd)
			cmd.SetOut(redirectionStd)
			cmd.SetArgs(tt.args)

			err = cmd.Execute()
			tt.assertError(t, err)

			if tt.expectedError != "" {
				require.EqualError(t, err, tt.expectedError)
			}

			output, err := os.ReadFile(redirectionStd.Name())
			require.NoError(t, err)
			assert.Equal(t, tt.expectedOutput, string(output), "output is not expected")

			if tt.expectedManifest != "" {
				wantManifest, readFileErr := os.ReadFile(tt.expectedManifest)
				require.NoError(t, readFileErr)
				gotManifest, readFileErr := os.ReadFile(tmpManifest.Name())
				require.NoError(t, readFileErr)
				assert.Equal(t, string(wantManifest), string(gotManifest), "manifest is not expected")
			}
		})
	}
}
