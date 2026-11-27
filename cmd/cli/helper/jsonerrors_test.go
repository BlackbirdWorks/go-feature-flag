package helper_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/pterm/pterm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

const (
	combinedEq     = "-vo=json"
	combinedO      = "-nvo"
	flagConfigLong = "--config"
	flagOutput     = "--output"
	flagOutputJSON = "--output=json"
	jsonValue      = "json"
	shortEqJSON    = "-o=json"
	shortJSON      = "-ojson"
	shortText      = "-otext"
	textValue      = "text"
	unknownArg     = "--unknown"
	badToken       = "---bad"
	subCmdName     = "sub"
	otherSubCmd    = "other"
	eqLiteralVal   = "=literal"
)

var errRun = errors.New("run failed")

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
		{name: "value of --config is not output", args: []string{"--config=-otext"}, wantFound: false},
		{name: "value of -c is not output", args: []string{"-c-ojson"}, wantFound: false},
		{
			name:      "missing --config value followed by -o json",
			args:      []string{flagConfigLong, "-o", jsonValue},
			wantValue: jsonValue,
			wantFound: true,
		},
		{
			name:      "missing -c value followed by -ojson",
			args:      []string{"-c", shortJSON},
			wantValue: jsonValue,
			wantFound: true,
		},
		{
			name:      "stops at double dash",
			args:      []string{flagOutputJSON, "--", "-o", textValue},
			wantValue: jsonValue,
			wantFound: true,
		},
		{name: "missing value at end", args: []string{"-o"}, wantFound: false},
		{
			name:      "missing output value followed by --output=json",
			args:      []string{flagOutput, flagOutputJSON},
			wantValue: jsonValue,
			wantFound: true,
		},
		{
			name:      "missing short output value followed by -ojson",
			args:      []string{"-o", shortJSON},
			wantValue: jsonValue,
			wantFound: true,
		},
		{
			name:      "missing output value followed by -- does not swallow delimiter",
			args:      []string{flagOutput, "--", flagOutputJSON},
			wantFound: false,
		},
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
		DryRun bool `name:"dry"`
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

func FuzzScanOutputFlag(f *testing.F) {
	var cmd struct {
		Output  string `default:"text" name:"output" short:"o"`
		Config  string `name:"config"  short:"c"`
		DryRun  bool   `name:"dry"     short:"n"`
		Verbose bool   `name:"verbose" short:"v"`
	}

	parser, err := kong.New(&cmd)
	require.NoError(f, err)

	flags := parser.Model.Flags

	for _, seed := range []string{
		"-o\njson",
		"--output=json",
		"---bad\n-ojson",
		"--config\n-otext",
		"-nvo\njson\n--\n-o\ntext",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, joined string) {
		args := strings.Split(joined, "\n")

		value, found := helper.ScanOutputFlag(flags, args)
		if found {
			assert.Contains(t, joined, value, "returned value must come from the arguments")
		}

		prefixed, prefixedFound := helper.ScanOutputFlag(flags, append([]string{badToken}, args...))
		assert.Equal(t, value, prefixed, "a malformed token first changes nothing")
		assert.Equal(t, found, prefixedFound)
	})
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
			name:        "a marked plain error has the default code",
			err:         helper.MarkReported(errRun),
			wantMessage: "run failed",
			wantCode:    helper.ExitFailure,
			wantIs:      true,
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

			out := helper.Output{}
			out.Add("the line", tt.level)
			require.NoError(t, out.PrintLines(&stdout, &stderr))

			got, other := stdout.String(), stderr.String()
			if !tt.wantStdout {
				got, other = other, got
			}

			assert.Contains(t, pterm.RemoveColorFromString(got), "the line")
			assert.Empty(t, other)
		})
	}
}

type failingWriter struct{}

var errWrite = errors.New("disk full")

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

func TestPrintLinesReturnsWriteError(t *testing.T) {
	t.Parallel()

	out := helper.Output{}
	out.Add("line", helper.DefaultLevel)
	require.ErrorIs(t, out.PrintLines(failingWriter{}, failingWriter{}), errWrite)
}

func TestParseAndFlags(t *testing.T) {
	t.Parallel()

	var cmd struct {
		Sub struct {
			Other struct{} `cmd:"" help:"Other subcommand"`
			Leaf  struct {
				Arg string `arg:"" name:"arg"`
			} `cmd:"" default:"withargs" hidden:""`
		} `cmd:""`
		Output string          `default:"text" name:"output" short:"o"`
		Help   helper.HelpFlag `name:"help"    short:"h"`
		Run    bool            `name:"run"`
	}

	var out bytes.Buffer

	parser, err := kong.New(
		&cmd,
		kong.Writers(&out, &out),
		kong.NoDefaultHelp(),
		kong.PostBuild(helper.RegisterHelpFlag),
		kong.KindMapper(reflect.String, kong.MapperFunc(helper.DecodeString)),
	)
	require.Error(t, helper.RegisterHelpFlag(nil))
	require.NoError(t, err)
	assert.NotNil(t, parser.Model.HelpFlag)

	kctx, exited, err := helper.Parse(parser, []string{"--help"})
	require.NoError(t, err)
	assert.True(t, exited)
	assert.Nil(t, kctx)
	assert.Contains(t, out.String(), "--output")
	assert.Contains(t, out.String(), `Run "`+parser.Model.Name+` <command> --help"`)

	out.Reset()

	kctx, exited, err = helper.Parse(parser, []string{"--help=false", "-o=json", "--run", subCmdName, otherSubCmd})
	require.NoError(t, err)
	assert.False(t, exited)
	require.NotNil(t, kctx)
	assert.Empty(t, out.String())
	assert.True(t, cmd.Run)

	for _, testCase := range []struct {
		want string
		args []string
	}{
		{args: []string{"-o=json", subCmdName, otherSubCmd}, want: jsonValue},
		{args: []string{shortJSON, subCmdName, otherSubCmd}, want: jsonValue},
		{args: []string{"-o", jsonValue, subCmdName, otherSubCmd}, want: jsonValue},
		{args: []string{"-o", eqLiteralVal, subCmdName, otherSubCmd}, want: eqLiteralVal},
		{args: []string{"--output=" + eqLiteralVal, subCmdName, otherSubCmd}, want: eqLiteralVal},
	} {
		cmd.Output = ""
		_, exited, err = helper.Parse(parser, testCase.args)
		require.NoError(t, err)
		assert.False(t, exited)
		assert.Equal(t, testCase.want, cmd.Output)
	}

	_, exited, err = helper.Parse(parser, []string{flagOutput})
	require.Error(t, err)
	assert.False(t, exited)

	_, exited, err = helper.Parse(parser, []string{"--nope"})
	require.Error(t, err)
	assert.False(t, exited)
}
