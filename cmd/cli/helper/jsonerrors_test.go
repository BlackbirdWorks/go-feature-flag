package helper_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

const (
	shortText      = "-otext"
	flagEndpoint   = "--endpoint"
	flagOutput     = "--output"
	flagOutputJSON = "--output=json"
	jsonValue      = "json"
	textValue      = "text"
	subName        = "sub"
	pubName        = "publish"
	jsonFlag       = "--json"
)

var errRun = errors.New("run failed")

// newTree is a command with one subcommand, wired to the JSON error mechanism like a real command group.
func newTree(jsonMode *bool, runErr error) *cobra.Command {
	root := &cobra.Command{Use: "root", SilenceErrors: true, SilenceUsage: true, Args: cobra.NoArgs}
	root.PersistentFlags().BoolVar(jsonMode, jsonValue, false, "")
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

	tests := []struct {
		err         error
		name        string
		wantMessage string
		wantCode    int
		wantNil     bool
		wantIs      bool
	}{
		{name: "nil stays nil", err: nil, wantNil: true},
		{
			name:        "a marked error keeps its message and its code",
			err:         helper.WithExitCode(helper.MarkReported(errRun), helper.ExitVerify),
			wantMessage: "run failed",
			wantCode:    helper.ExitVerify,
			wantIs:      true,
		},
		{
			name: "a marked plain error has the default code", err: helper.MarkReported(errRun),
			wantMessage: "run failed", wantCode: helper.ExitFailure, wantIs: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.wantNil {
				require.NoError(t, helper.MarkReported(tt.err))

				return
			}

			assert.Equal(t, tt.wantMessage, tt.err.Error())
			assert.Equal(t, tt.wantCode, helper.ExitCodeOf(tt.err))
			assert.Equal(t, tt.wantIs, errors.Is(tt.err, errRun))
		})
	}
}

// outputFlags is a flag set like the one of publish: a string flag -c, a boolean -n, a string with no shorthand, and
// the output flag -o.
func outputFlags() *pflag.FlagSet {
	flags := pflag.NewFlagSet("publish", pflag.ContinueOnError)
	flags.StringP("config", "c", "", "")
	flags.String("endpoint", "", "")
	flags.BoolP("dry-run", "n", false, "")
	flags.BoolP("verbose", "v", false, "")
	flags.StringP("output", "o", textValue, "")

	return flags
}

func TestRawOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		wantValue string
		args      []string
		wantFound bool
	}{
		{name: "no arguments"},
		{name: "no output flag", args: []string{pubName, flagEndpoint, "http://x", "-c", "file"}},
		{name: "short flag", args: []string{pubName, "-o", jsonValue}, wantValue: jsonValue, wantFound: true},
		{name: "short flag attached", args: []string{"-ojson"}, wantValue: jsonValue, wantFound: true},
		{name: "short flag with equals", args: []string{"-o=json"}, wantValue: jsonValue, wantFound: true},
		{name: "long flag", args: []string{flagOutput, jsonValue}, wantValue: jsonValue, wantFound: true},
		{name: "long flag with equals", args: []string{flagOutputJSON}, wantValue: jsonValue, wantFound: true},
		{name: "text is a selection too", args: []string{"--output=text"}, wantValue: textValue, wantFound: true},
		{
			name: "the last one wins", args: []string{"-o", jsonValue, "-o", textValue}, wantValue: textValue,
			wantFound: true,
		},
		{
			name: "before an unknown flag", args: []string{"-o", jsonValue, "--no-such-flag"}, wantValue: jsonValue,
			wantFound: true,
		},
		{
			name: "after an unknown flag", args: []string{"--no-such-flag", "-o", textValue}, wantValue: textValue,
			wantFound: true,
		},
		{
			name: "attached after an unknown flag", args: []string{"--not-a-flag", shortText}, wantValue: textValue,
			wantFound: true,
		},
		{
			name:      "-otext is the value of --config",
			args:      []string{flagOutput, jsonValue, "--config", shortText, "--not-a-flag"},
			wantValue: jsonValue,
			wantFound: true,
		},
		{
			name: "-otext is the attached value of --config", args: []string{flagOutput, jsonValue, "--config=-otext"},
			wantValue: jsonValue, wantFound: true,
		},
		{
			name: "-otext is the value of -c", args: []string{flagOutput, jsonValue, "-c", shortText},
			wantValue: jsonValue, wantFound: true,
		},
		{name: "--output is the value of another flag", args: []string{flagEndpoint, flagOutput, "-x"}},
		{name: "--output=json is the value of another flag", args: []string{flagEndpoint, flagOutputJSON}},
		{
			name: "a boolean flag takes no value", args: []string{"-n", "-o", jsonValue, "--dry-run", "-v"},
			wantValue: jsonValue, wantFound: true,
		},
		{
			name: "combined shorthands ending in o with an attached value", args: []string{"-nvojson"},
			wantValue: jsonValue, wantFound: true,
		},
		{
			name: "combined shorthands ending in o with the value after", args: []string{"-nvo", jsonValue},
			wantValue: jsonValue, wantFound: true,
		},
		{name: "combined shorthands ending in c take the next argument", args: []string{"-nvc", "-ojson"}},
		{name: "a positional json", args: []string{pubName, jsonValue}},
		{name: "a positional that looks like a flag value", args: []string{"verify", "-n", jsonValue}},
		{name: "stops at the double dash", args: []string{pubName, "--", "-o", jsonValue}},
		{
			name: "the selection before the double dash stays", args: []string{"-o", textValue, "--", flagOutputJSON},
			wantValue: textValue, wantFound: true,
		},
		{name: "a dangling output flag", args: []string{pubName, "-o"}},
		{name: "a dangling long output flag", args: []string{pubName, flagOutput}},
		{name: "an unknown shorthand run stops there", args: []string{"-xojson"}},
		{name: "another flag that starts with -o", args: []string{"-opt"}, wantValue: "pt", wantFound: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			value, found := helper.RawOutput(outputFlags(), tt.args)

			assert.Equal(t, tt.wantValue, value)
			assert.Equal(t, tt.wantFound, found)
		})
	}
}

func TestRawOutputWithoutAnOutputFlagNamesNothing(t *testing.T) {
	t.Parallel()

	value, found := helper.RawOutput(pflag.NewFlagSet("none", pflag.ContinueOnError), []string{"-o", jsonValue})
	assert.Empty(t, value)
	assert.False(t, found)

	value, found = helper.RawOutput(nil, []string{"-o", jsonValue})
	assert.Empty(t, value)
	assert.False(t, found)
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
