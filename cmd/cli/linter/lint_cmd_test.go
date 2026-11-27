package linter_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/pterm/pterm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/cli/linter"
)

const formatJSON = "--format=json"

func TestMain(m *testing.M) {
	pterm.DisableStyling()
	pterm.DisableColor()

	m.Run()
}

func TestCmdLint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr           assert.ErrorAssertionFunc
		name              string
		expectedStdout    string
		expectedStderr    string
		expectedErrString string
		args              []string
	}{
		{
			name:    "missing configuration file location",
			args:    []string{formatJSON},
			wantErr: assert.Error,
			expectedStderr: "ERROR: impossible to find config file in the default locations [./,/semaphore/,/etc/opt/se" +
				"maphore/,/goff/,/etc/opt/goff/]\n",
			expectedErrString: "invalid Semaphore configuration",
		},
		{
			name:    "invalid configuration",
			args:    []string{"testdata/invalid.json", formatJSON},
			wantErr: assert.Error,
			expectedStderr: "ERROR: testdata/invalid.json: could not parse file (json): decoding the flags: " +
				"invalid character ':' after top-level value\n",
			expectedErrString: "invalid Semaphore configuration",
		},
		{
			name:           "valid configuration",
			args:           []string{"testdata/valid.yaml", "--format=yaml"},
			wantErr:        assert.NoError,
			expectedStdout: "INFO: Valid Semaphore configuration\n",
		},
		{
			name:              "unknown flag returns usage error",
			args:              []string{"--nope"},
			wantErr:           assert.Error,
			expectedErrString: "usage: unknown flag --nope",
		},
		{
			name:    "help exits without error",
			args:    []string{"--help"},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			err := linter.Run(tt.args, &helper.Streams{Stdout: &stdout, Stderr: &stderr})
			tt.wantErr(t, err)

			assert.Equal(t, tt.expectedStderr, pterm.RemoveColorFromString(stderr.String()))

			if tt.expectedStdout != "" {
				assert.Equal(t, tt.expectedStdout, pterm.RemoveColorFromString(stdout.String()))
			}

			if tt.expectedErrString != "" {
				assert.EqualError(t, err, tt.expectedErrString)
			}
		})
	}
}

type failingWriter struct{}

var errWrite = errors.New("disk full")

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

func TestCmdLintWriteErrorsAndHelp(t *testing.T) {
	t.Parallel()

	assert.NotEmpty(t, (linter.Cmd{}).Help())

	tests := []struct {
		name   string
		stdout io.Writer
		stderr io.Writer
		args   []string
	}{
		{
			name:   "valid config stdout write error",
			args:   []string{"testdata/valid.yaml", "--format=yaml"},
			stdout: failingWriter{},
			stderr: io.Discard,
		},
		{
			name:   "invalid config stderr write error",
			args:   []string{"testdata/invalid.json", formatJSON},
			stdout: io.Discard,
			stderr: failingWriter{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := linter.Run(tt.args, &helper.Streams{Stdout: tt.stdout, Stderr: tt.stderr})
			require.ErrorIs(t, err, errWrite)
		})
	}
}
