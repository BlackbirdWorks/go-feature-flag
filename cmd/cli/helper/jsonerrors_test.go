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
	combinedO      = "-nvo"
	bogusValue     = "bogus"
	flagOutputJSON = "--output=json"
	badToken       = "---bad"
	flagConfigLong = "--config"
	shortJSON      = "-ojson"
	emptyName      = "--=x"
	tripleDash     = "---x"
	groupName      = "config"
	leafName       = "validate"
	unknownArg     = "--unknown"
	shortText      = "-otext"
	flagEndpoint   = "--endpoint"
	flagOutput     = "--output"
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
	flags.StringP(groupName, "c", "", "")
	flags.String("endpoint", "", "")
	flags.BoolP("dry-run", "n", false, "")
	flags.BoolP("verbose", "v", false, "")
	flags.IntP("port", "p", 0, "")
	flags.StringP("output", "o", textValue, "")

	return flags
}

// rawOutputRow is one case of the read of the output flag: the arguments and what the read must return.
type rawOutputRow struct {
	name      string
	wantValue string
	args      []string
	wantFound bool
}

// rawOutputRows are the cases of TestRawOutputIn; they are the seeds of FuzzRawOutput too.
func rawOutputRows() []rawOutputRow {
	return []rawOutputRow{
		{name: "no arguments"},
		{name: "no output flag", args: []string{pubName, flagEndpoint, "http://x", "-c", "file"}},
		{name: "short flag", args: []string{pubName, "-o", jsonValue}, wantValue: jsonValue, wantFound: true},
		{name: "short flag attached", args: []string{shortJSON}, wantValue: jsonValue, wantFound: true},
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
			args:      []string{flagOutput, jsonValue, flagConfigLong, shortText, "--not-a-flag"},
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
			name: "combined shorthands ending in o with the value after", args: []string{combinedO, jsonValue},
			wantValue: jsonValue, wantFound: true,
		},
		{name: "combined shorthands ending in c take the next argument", args: []string{"-nvc", shortJSON}},
		{name: "a positional json", args: []string{pubName, jsonValue}},
		{name: "a positional that looks like a flag value", args: []string{"verify", "-n", jsonValue}},
		{name: "stops at the double dash", args: []string{pubName, "--", "-o", jsonValue}},
		{
			name: "the selection before the double dash stays", args: []string{"-o", textValue, "--", flagOutputJSON},
			wantValue: textValue, wantFound: true,
		},
		{name: "a dangling output flag", args: []string{pubName, "-o"}},
		{name: "a dangling long output flag", args: []string{pubName, flagOutput}},
		{
			name:      "an unknown shorthand in a run is skipped as pflag skips it",
			args:      []string{"-xojson"},
			wantValue: jsonValue,
			wantFound: true,
		},
		{
			name:      "a malformed flag before the selection",
			args:      []string{"---dry-run", flagOutputJSON},
			wantValue: jsonValue,
			wantFound: true,
		},
		{
			name:      "a flag with an empty name before the selection",
			args:      []string{emptyName, "-o", jsonValue},
			wantValue: jsonValue,
			wantFound: true,
		},
		{
			name:      "a lone dash is a positional",
			args:      []string{"-", "-o", jsonValue},
			wantValue: jsonValue,
			wantFound: true,
		},
		{name: "a lone dash and nothing else", args: []string{"-"}},
		{
			name:      "a malformed token as the value of --output is the value",
			args:      []string{"--output", tripleDash},
			wantValue: tripleDash,
			wantFound: true,
		},
		{
			name:      "a malformed token as the value of another flag is that value",
			args:      []string{flagConfigLong, tripleDash, "-o", jsonValue},
			wantValue: jsonValue,
			wantFound: true,
		},
		{
			name: "a malformed token after the double dash is a positional",
			args: []string{"--", tripleDash, flagOutputJSON},
		},
		{
			name:      "a malformed token after a selection and the double dash",
			args:      []string{"-o", textValue, "--", tripleDash},
			wantValue: textValue,
			wantFound: true,
		},
		{
			name:      "a malformed token between two selections",
			args:      []string{"-o", textValue, tripleDash, "-o", jsonValue},
			wantValue: jsonValue,
			wantFound: true,
		},
		{
			name:      "an invalid value for a typed flag before the selection",
			args:      []string{"--port", "abc", "-o", jsonValue},
			wantValue: jsonValue,
			wantFound: true,
		},
		{
			name:      "an invalid value for a typed shorthand before the selection",
			args:      []string{"-p", "abc", "-o", jsonValue},
			wantValue: jsonValue,
			wantFound: true,
		},
		{
			name: "an invalid output value followed by a valid one", args: []string{"-o", bogusValue, "-o", jsonValue},
			wantValue: jsonValue, wantFound: true,
		},
		{
			name:      "an invalid output value and nothing after it",
			args:      []string{"-o", bogusValue},
			wantValue: bogusValue,
			wantFound: true,
		},
		{name: "another flag that starts with -o", args: []string{"-opt"}, wantValue: "pt", wantFound: true},
	}
}

func TestRawOutputIn(t *testing.T) {
	t.Parallel()

	tests := rawOutputRows()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			value, found := helper.RawOutputIn(outputFlags(), tt.args)

			assert.Equal(t, tt.wantValue, value)
			assert.Equal(t, tt.wantFound, found)
		})
	}
}

// outputTree is root > config (persistent -o output, -v verbose) > validate, like the config group of the CLI.
func outputTree() (*cobra.Command, *cobra.Command) {
	root := &cobra.Command{Use: "root", SilenceErrors: true, SilenceUsage: true}
	group := &cobra.Command{Use: groupName, SilenceErrors: true, SilenceUsage: true}
	group.PersistentFlags().StringP("output", "o", textValue, "")
	group.PersistentFlags().BoolP("verbose", "v", false, "")
	group.PersistentFlags().StringP(groupName, "c", "", "")

	validate := &cobra.Command{
		Use:           leafName,
		SilenceErrors: true,
		SilenceUsage:  true,
		Run:           func(*cobra.Command, []string) {},
	}
	group.AddCommand(validate)
	root.AddCommand(group)

	// Cobra merges the persistent flags of the parents into the flags of the command when it parses.
	validate.Flags().AddFlagSet(group.PersistentFlags())

	return root, validate
}

// The command names are removed by Cobra before pflag reads the flags, so the scan must start from what is left.
func TestRawOutputResolvesTheCommandLikeCobra(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		wantValue string
		args      []string
		wantFound bool
	}{
		{
			name:      "combined shorthand before a subcommand",
			args:      []string{groupName, "-vo", leafName, jsonValue, unknownArg},
			wantValue: jsonValue,
			wantFound: true,
		},
		{
			name: "persistent flag before the subcommand, output after it",
			args: []string{groupName, "-v", leafName, "-o", jsonValue}, wantValue: jsonValue, wantFound: true,
		},
		{
			name: "output before the subcommand, an unknown flag after it",
			args: []string{groupName, "-o", jsonValue, leafName, unknownArg}, wantValue: jsonValue, wantFound: true,
		},
		{
			name:      "a value flag missing its value at the end",
			args:      []string{groupName, leafName, "-o", jsonValue, "-c"},
			wantValue: jsonValue,
			wantFound: true,
		},
		{
			name: "the output flag given twice", args: []string{groupName, leafName, "-o", jsonValue, "-o", textValue},
			wantValue: textValue, wantFound: true,
		},
		{
			name:      "a later selection after an unknown flag",
			args:      []string{groupName, leafName, unknownArg, "-o", jsonValue},
			wantValue: jsonValue,
			wantFound: true,
		},
		{name: "no selection", args: []string{groupName, leafName, unknownArg}},
		{
			name:      "arguments that name no command fall back to the failing command",
			args:      []string{"-o", jsonValue},
			wantValue: jsonValue,
			wantFound: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root, validate := outputTree()
			_ = root

			value, found := helper.RawOutput(validate, tt.args)

			assert.Equal(t, tt.wantValue, value)
			assert.Equal(t, tt.wantFound, found)
		})
	}
}

func TestRawOutputWithoutAnOutputFlagNamesNothing(t *testing.T) {
	t.Parallel()

	value, found := helper.RawOutputIn(pflag.NewFlagSet("none", pflag.ContinueOnError), []string{"-o", jsonValue})
	assert.Empty(t, value)
	assert.False(t, found)

	value, found = helper.RawOutputIn(nil, []string{"-o", jsonValue})
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
