package manifest_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pterm/pterm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/generate/manifest"
	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

const (
	infoManifestHasBeenCreated = "INFO: 🎉 Manifest has been created\n"
	flagConfigValid            = "--config=testdata/input/flag.goff.yaml"
	flagManifestDest           = "--flag_manifest_destination"
)

func TestMain(m *testing.M) {
	pterm.DisableStyling()
	pterm.DisableColor()

	m.Run()
}

func TestManifestCmd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		assertError      assert.ErrorAssertionFunc
		name             string
		expectedManifest string
		wantStdout       string
		wantStderr       string
		expectedError    string
		args             []string
	}{
		{
			name:             "should return success if everything is ok",
			args:             []string{flagConfigValid},
			expectedManifest: "testdata/output/flag.goff.json",
			wantStdout:       infoManifestHasBeenCreated,
			assertError:      assert.NoError,
		},
		{
			name:             "should ignore flag without value",
			args:             []string{"--config=testdata/input/flag-no-default.yaml"},
			expectedManifest: "testdata/output/flag-no-default.json",
			wantStdout:       infoManifestHasBeenCreated,
			wantStderr:       "WARNING: flag ignored: no default value provided flag=test-flag\n",
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
			wantStdout:       infoManifestHasBeenCreated,
		},
		{
			name:             "should have float as type if 1 float and int are mixed",
			args:             []string{"--config=testdata/input/flag-float-and-int.yaml"},
			assertError:      assert.NoError,
			expectedManifest: "testdata/output/flag-float-and-int.json",
			wantStdout:       infoManifestHasBeenCreated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpManifest := filepath.Join(t.TempDir(), "manifest.json")
			args := append(append([]string{}, tt.args...), flagManifestDest, tmpManifest)

			var stdout, stderr bytes.Buffer

			err := manifest.Run(args, &helper.Streams{Stdout: &stdout, Stderr: &stderr})
			tt.assertError(t, err)

			if tt.expectedError != "" {
				require.EqualError(t, err, tt.expectedError)
			}

			assert.Equal(t, tt.wantStdout, pterm.RemoveColorFromString(stdout.String()), "stdout is not expected")
			assert.Equal(t, tt.wantStderr, pterm.RemoveColorFromString(stderr.String()), "stderr is not expected")

			if tt.expectedManifest != "" {
				wantManifest, readFileErr := os.ReadFile(tt.expectedManifest)
				require.NoError(t, readFileErr)

				gotManifest, readFileErr := os.ReadFile(tmpManifest)
				require.NoError(t, readFileErr)
				assert.Equal(t, string(wantManifest), string(gotManifest), "manifest is not expected")
			}
		})
	}
}

func TestManifestMissingRequiredFlag(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	err := manifest.Run(
		[]string{flagConfigValid},
		&helper.Streams{Stdout: &stdout, Stderr: &stderr},
	)
	require.ErrorContains(t, err, flagManifestDest)
}

type failingWriter struct{}

var errWrite = errors.New("disk full")

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

func TestManifestCmdEdgeCases(t *testing.T) {
	t.Parallel()

	assert.NotEmpty(t, (manifest.Cmd{}).Help())

	dir := t.TempDir()
	outFile := filepath.Join(dir, "manifest.json")

	tests := []struct {
		wantErr assert.ErrorAssertionFunc
		stdout  io.Writer
		name    string
		wantOut string
		wantMsg string
		args    []string
		isDisk  bool
	}{
		{
			name:    "help flag prints usage and exits nil",
			args:    []string{"--help"},
			stdout:  &bytes.Buffer{},
			wantErr: assert.NoError,
			wantOut: flagManifestDest,
		},
		{
			name:    "missing config file returns load error",
			args:    []string{"--config=testdata/input/does-not-exist.yaml", flagManifestDest, outFile},
			stdout:  &bytes.Buffer{},
			wantErr: assert.Error,
			wantMsg: "loading the configuration of the manifest",
		},
		{
			name:    "destination directory returns write error",
			args:    []string{flagConfigValid, flagManifestDest, dir},
			stdout:  &bytes.Buffer{},
			wantErr: assert.Error,
			wantMsg: "writing the flag manifest",
		},
		{
			name:    "stdout write failure returns error",
			args:    []string{flagConfigValid, flagManifestDest, outFile},
			stdout:  failingWriter{},
			wantErr: assert.Error,
			isDisk:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stderr bytes.Buffer

			err := manifest.Run(tt.args, &helper.Streams{Stdout: tt.stdout, Stderr: &stderr})
			tt.wantErr(t, err)

			if tt.wantMsg != "" {
				require.ErrorContains(t, err, tt.wantMsg)
			}

			if tt.isDisk {
				require.ErrorIs(t, err, errWrite)
			}

			if buf, ok := tt.stdout.(*bytes.Buffer); ok && tt.wantOut != "" {
				assert.Contains(t, buf.String(), tt.wantOut)
			}
		})
	}
}
