package helper_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

func TestOutput_PrintLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		output   helper.Output
		expected string
	}{
		{
			name: "print info level line",
			output: helper.Output{
				Lines: []helper.OutputLine{
					{Text: "Info message", Level: helper.InfoLevel},
				},
			},
			expected: "INFO: Info message\n",
		},
		{
			name: "print warning level line",
			output: helper.Output{
				Lines: []helper.OutputLine{
					{Text: "Warning message", Level: helper.WarnLevel},
				},
			},
			expected: "WARNING: Warning message\n",
		},
		{
			name: "print error level line",
			output: helper.Output{
				Lines: []helper.OutputLine{
					{Text: "Error message", Level: helper.ErrorLevel},
				},
			},
			expected: "ERROR: Error message\n",
		},
		{
			name: "print default level line",
			output: helper.Output{
				Lines: []helper.OutputLine{
					{Text: "Default message", Level: helper.DefaultLevel},
				},
			},
			expected: "Default message\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)

			tt.output.PrintLines(cmd)
			assert.Equal(t, tt.expected, buf.String())
		})
	}
}

func TestPrintFatal(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	var gotCode int
	called := false

	helper.PrintFatal(pterm.Error.WithWriter(&buf), func(code int) {
		gotCode = code
		called = true
	}, errors.New("boom"))

	assert.True(t, called, "the exit function should have been called")
	assert.Equal(t, 1, gotCode)
	assert.Equal(t, "ERROR: error executing command: boom\n", buf.String())
}
