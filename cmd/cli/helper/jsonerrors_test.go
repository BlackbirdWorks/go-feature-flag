package helper_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

const (
	subName  = "sub"
	jsonFlag = "--json"
)

var errRun = errors.New("run failed")

// newTree is a command with one subcommand, wired to the JSON error mechanism like a real command group.
func newTree(jsonMode *bool, runErr error) *cobra.Command {
	root := &cobra.Command{Use: "root", SilenceErrors: true, SilenceUsage: true, Args: cobra.NoArgs}
	root.PersistentFlags().BoolVar(jsonMode, "json", false, "")
	root.AddCommand(&cobra.Command{
		Use: subName, SilenceErrors: true, SilenceUsage: true, Args: cobra.ExactArgs(1),
		RunE: func(*cobra.Command, []string) error { return runErr },
	})
	root.AddCommand(&cobra.Command{
		Use: "done", SilenceErrors: true, SilenceUsage: true,
		RunE: func(*cobra.Command, []string) error {
			return helper.WithExitCode(helper.MarkReported(errRun), helper.ExitVerify)
		},
	})

	helper.JSONErrors(root, helper.JSONErrorOptions{
		Enabled: func() bool { return *jsonMode },
		Details: func(error) []string { return []string{"detail"} },
	})

	return root
}

func TestJSONErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		runErr     error
		name       string
		wantStdout string
		args       []string
		wantExit   int
	}{
		{
			name:       "argument error in JSON mode",
			args:       []string{jsonFlag, subName},
			wantExit:   helper.ExitFailure,
			wantStdout: `{"error":{"code":"usage","message":"usage: accepts 1 arg(s), received 0","details":["detail"]}}`,
		},
		{
			name: "flag error in JSON mode", args: []string{jsonFlag, subName, "--nope"}, wantExit: helper.ExitFailure,
			wantStdout: `{"error":{"code":"usage","message":"usage: unknown flag: --nope","details":["detail"]}}`,
		},
		{
			name:       "failure of the command keeps its code",
			args:       []string{jsonFlag, subName, "x"},
			wantExit:   helper.ExitConflict,
			runErr:     helper.WithExitCode(errRun, helper.ExitConflict),
			wantStdout: `{"error":{"code":"conflict","message":"run failed","details":["detail"]}}`,
		},
		{
			name:       "plain failure exits 1",
			args:       []string{jsonFlag, subName, "x"},
			wantExit:   helper.ExitFailure,
			runErr:     errRun,
			wantStdout: `{"error":{"code":"failure","message":"run failed","details":["detail"]}}`,
		},
		{name: "text mode writes nothing on stdout", args: []string{subName}, wantExit: helper.ExitFailure},
		{
			name:     "text mode failure of the command",
			args:     []string{subName, "x"},
			wantExit: helper.ExitFailure,
			runErr:   errRun,
		},
		{
			name:     "a reported error gets no second document",
			args:     []string{jsonFlag, "done"},
			wantExit: helper.ExitVerify,
		},
		{name: "success", args: []string{jsonFlag, subName, "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var jsonMode bool

			root := newTree(&jsonMode, tt.runErr)

			var stdout bytes.Buffer

			root.SetOut(&stdout)
			root.SetArgs(tt.args)

			err := root.ExecuteContext(t.Context())

			if tt.wantExit == 0 {
				require.NoError(t, err)
				assert.Empty(t, stdout.String())

				return
			}

			require.Error(t, err)
			assert.Equal(t, tt.wantExit, helper.ExitCodeOf(err))

			if tt.wantStdout == "" {
				assert.Empty(t, stdout.String())
			} else {
				assert.JSONEq(t, tt.wantStdout, stdout.String())
			}
		})
	}
}

func TestMarkReportedKeepsTheErrorAndItsCode(t *testing.T) {
	t.Parallel()

	require.NoError(t, helper.MarkReported(nil))

	err := helper.WithExitCode(helper.MarkReported(errRun), helper.ExitVerify)
	require.ErrorIs(t, err, errRun)
	assert.Equal(t, helper.ExitVerify, helper.ExitCodeOf(err))
	assert.Equal(t, "run failed", err.Error())
}

func TestPrintLinesRoutesResultsToStdoutAndDiagnosticsToStderr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		level      helper.Level
		wantStdout bool
	}{
		{name: "status is a result", level: helper.StatusLevel, wantStdout: true},
		{name: "default is a result", level: helper.DefaultLevel, wantStdout: true},
		{name: "info is a diagnostic", level: helper.InfoLevel},
		{name: "warning is a diagnostic", level: helper.WarnLevel},
		{name: "error is a diagnostic", level: helper.ErrorLevel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			cmd := &cobra.Command{}
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)

			out := helper.Output{}
			out.Add("the line", tt.level)
			out.PrintLines(cmd)

			got, other := stdout.String(), stderr.String()
			if !tt.wantStdout {
				got, other = other, got
			}

			assert.Contains(t, pterm.RemoveColorFromString(got), "the line")
			assert.Empty(t, other)
		})
	}
}
