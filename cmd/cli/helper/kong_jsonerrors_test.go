package helper_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/pterm/pterm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

func kongTestFlags(t *testing.T) []*kong.Flag {
	t.Helper()

	var cmd struct {
		Output   string `default:"text"  name:"output" short:"o"`
		Config   string `name:"config"   short:"c"`
		Endpoint string `name:"endpoint"`
		DryRun   bool   `name:"dry"      short:"n"`
		Verbose  bool   `name:"verbose"  short:"v"`
	}

	parser, err := kong.New(&cmd)
	require.NoError(t, err)

	return parser.Model.Flags
}

func TestScanOutputFlag(t *testing.T) {
	t.Parallel()

	flags := kongTestFlags(t)

	tests := []struct {
		name      string
		wantValue string
		args      []string
		wantFound bool
	}{
		{name: "empty args"},
		{name: "no output flag", args: []string{"--dry", unknownArg}},
		{name: "long flag with space", args: []string{flagOutput, jsonValue}, wantValue: jsonValue, wantFound: true},
		{name: "long flag with equals", args: []string{flagOutputJSON}, wantValue: jsonValue, wantFound: true},
		{name: "long flag with empty equals", args: []string{"--output="}, wantValue: "", wantFound: true},
		{name: "short flag with space", args: []string{"-o", jsonValue}, wantValue: jsonValue, wantFound: true},
		{name: "short flag attached", args: []string{shortJSON}, wantValue: jsonValue, wantFound: true},
		{name: "short flag with equals", args: []string{shortEqJSON}, wantValue: jsonValue, wantFound: true},
		{
			name:      "combined short bool then o",
			args:      []string{combinedO, jsonValue},
			wantValue: jsonValue,
			wantFound: true,
		},
		{name: "combined short bool then o=json", args: []string{combinedEq}, wantValue: jsonValue, wantFound: true},
		{
			name:      "last wins after unknown flag",
			args:      []string{unknownArg, flagOutputJSON, "-o", textValue},
			wantValue: textValue,
			wantFound: true,
		},
		{
			name:      "output after malformed token",
			args:      []string{badToken, shortJSON},
			wantValue: jsonValue,
			wantFound: true,
		},
		{name: "value of --config is not output", args: []string{flagConfigLong, shortText}, wantFound: false},
		{name: "value of -c is not output", args: []string{"-c", shortJSON}, wantFound: false},
		{
			name:      "stops at double dash",
			args:      []string{flagOutputJSON, "--", "-o", textValue},
			wantValue: jsonValue,
			wantFound: true,
		},
		{name: "missing value at end", args: []string{"-o"}, wantFound: false},
		{name: "unknown short flag skipped", args: []string{"-x", shortJSON}, wantValue: jsonValue, wantFound: true},
		{
			name:      "bool short with equals stops cluster",
			args:      []string{"-n=true", shortJSON},
			wantValue: jsonValue,
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, found := helper.ScanOutputFlag(flags, tt.args)
			assert.Equal(t, tt.wantFound, found)
			assert.Equal(t, tt.wantValue, got)
		})
	}
}

func TestScanOutputFlagWithoutOutput(t *testing.T) {
	t.Parallel()

	var cmd struct {
		DryRun bool `name:"dry-run"`
	}

	parser, err := kong.New(&cmd)
	require.NoError(t, err)

	got, found := helper.ScanOutputFlag(parser.Model.Flags, []string{"-o", jsonValue})
	assert.False(t, found)
	assert.Empty(t, got)

	got, found = helper.ScanOutputFlag(nil, []string{"-o", jsonValue})
	assert.False(t, found)
	assert.Empty(t, got)
}

func TestReportJSONError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err        error
		details    func(error) []string
		name       string
		wantStdout string
		wantExit   int
		enabled    bool
		wantNil    bool
	}{
		{name: "nil error stays nil", enabled: true, wantNil: true},
		{name: "disabled leaves stdout empty", enabled: false, err: errRun, wantExit: helper.ExitFailure},
		{
			name:       "usage error gets usage code",
			enabled:    true,
			err:        errors.Join(helper.ErrUsage, errRun),
			details:    func(error) []string { return []string{"d1"} },
			wantExit:   helper.ExitFailure,
			wantStdout: `{"error":{"code":"usage","message":"usage\nrun failed","details":["d1"]}}`,
		},
		{
			name:       "exit error keeps its code",
			enabled:    true,
			err:        helper.WithExitCode(errRun, helper.ExitConflict),
			wantExit:   helper.ExitConflict,
			wantStdout: `{"error":{"code":"conflict","message":"run failed","details":[]}}`,
		},
		{
			name:     "reported error writes nothing",
			enabled:  true,
			err:      helper.WithExitCode(helper.MarkReported(errRun), helper.ExitVerify),
			wantExit: helper.ExitVerify,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer

			gotErr := helper.ReportJSONError(&stdout, tt.enabled, tt.details, tt.err)
			if tt.wantNil {
				require.NoError(t, gotErr)
				assert.Empty(t, stdout.String())

				return
			}

			require.Error(t, gotErr)
			assert.Equal(t, tt.wantExit, helper.ExitCodeOf(gotErr))

			if tt.wantStdout == "" {
				assert.Empty(t, stdout.String())
			} else {
				assert.JSONEq(t, tt.wantStdout, stdout.String())
			}
		})
	}
}

func TestPrintLinesTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		level      helper.Level
		wantStdout bool
	}{
		{name: "status goes to stdout", level: helper.StatusLevel, wantStdout: true},
		{name: "default goes to stdout", level: helper.DefaultLevel, wantStdout: true},
		{name: "info goes to stderr", level: helper.InfoLevel},
		{name: "warning goes to stderr", level: helper.WarnLevel},
		{name: "error goes to stderr", level: helper.ErrorLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			out := helper.Output{}
			out.Add("hello", tt.level)
			out.PrintLinesTo(&stdout, &stderr)

			target, other := stdout.String(), stderr.String()
			if !tt.wantStdout {
				target, other = other, target
			}

			assert.Contains(t, pterm.RemoveColorFromString(target), "hello")
			assert.Empty(t, other)
		})
	}
}
