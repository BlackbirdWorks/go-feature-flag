package cliapp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	helpFlag                 = "--help"
	flagEffective            = "--effective"
	cmdEvaluate              = "evaluate"
	cmdLint                  = "lint"
	cmdGenerate              = "generate"
	cmdManifest              = "manifest"
	cmdPrint                 = "print"
	goldenDir                = "testdata/golden"
	helpRootGolden           = "help_root.golden"
	helpConfigValidateGolden = "help_config_validate.golden"
	errUnknownFlagStderr     = "  ERROR   error executing command: usage: unknown flag --not-a-flag\n"
)

func readGolden(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(goldenDir, name))
	require.NoError(t, err)

	return string(data)
}

func unwrapped(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func TestHelpGolden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		wantGolden string
		args       []string
	}{
		{name: "root", args: []string{helpFlag}, wantGolden: helpRootGolden},
		{name: "evaluate help", args: []string{cmdEvaluate, helpFlag}, wantGolden: "help_evaluate.golden"},
		{name: "lint help", args: []string{cmdLint, helpFlag}, wantGolden: "help_lint.golden"},
		{name: "generate", args: []string{cmdGenerate, helpFlag}, wantGolden: "help_generate.golden"},
		{
			name:       "generate manifest",
			args:       []string{cmdGenerate, cmdManifest, helpFlag},
			wantGolden: "help_generate_manifest.golden",
		},
		{name: "config", args: []string{cmdConfig, helpFlag}, wantGolden: "help_config.golden"},
		{
			name:       "config validate",
			args:       []string{cmdConfig, cmdValidate, helpFlag},
			wantGolden: helpConfigValidateGolden,
		},
		{
			name:       "config print",
			args:       []string{cmdConfig, cmdPrint, helpFlag},
			wantGolden: "help_config_print.golden",
		},
		{name: "publish", args: []string{cmdPublish, helpFlag}, wantGolden: "help_publish.golden"},
		{
			name:       "publish verify",
			args:       []string{cmdPublish, cmdVerify, helpFlag},
			wantGolden: "help_publish_verify.golden",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, code := runCLIClean(t, nil, tt.args...)
			assert.Equal(t, 0, code)
			assert.Empty(t, stderr)
			assert.Equal(t, readGolden(t, tt.wantGolden), stdout)
		})
	}
}

func TestHelpIndependentOfTerminalWidth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		columns    string
		wantGolden string
		args       []string
		exact      bool
	}{
		{name: "root COLUMNS=40", columns: "COLUMNS=40", args: []string{helpFlag}, wantGolden: helpRootGolden},
		{
			name:       "root COLUMNS=80",
			columns:    "COLUMNS=80",
			args:       []string{helpFlag},
			wantGolden: helpRootGolden,
			exact:      true,
		},
		{
			name:       "root COLUMNS=200",
			columns:    "COLUMNS=200",
			args:       []string{helpFlag},
			wantGolden: helpRootGolden,
			exact:      true,
		},
		{
			name:       "config validate COLUMNS=40",
			columns:    "COLUMNS=40",
			args:       []string{cmdConfig, cmdValidate, helpFlag},
			wantGolden: helpConfigValidateGolden,
		},
		{
			name:       "config validate COLUMNS=80",
			columns:    "COLUMNS=80",
			args:       []string{cmdConfig, cmdValidate, helpFlag},
			wantGolden: helpConfigValidateGolden,
			exact:      true,
		},
		{
			name:       "config validate COLUMNS=200",
			columns:    "COLUMNS=200",
			args:       []string{cmdConfig, cmdValidate, helpFlag},
			wantGolden: helpConfigValidateGolden,
			exact:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, code := runCLIClean(t, []string{tt.columns}, tt.args...)
			assert.Equal(t, 0, code)
			assert.Empty(t, stderr)

			want := readGolden(t, tt.wantGolden)
			if tt.exact {
				assert.Equal(t, want, stdout)
			} else {
				assert.Equal(t, unwrapped(want), unwrapped(stdout))
			}
		})
	}
}

func TestUsageErrorsGolden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		wantGolden string
		wantStderr string
		args       []string
		wantExit   int
	}{
		{
			name:       "unknown flag on root",
			args:       []string{unknownFlag},
			wantGolden: "err_unknown_flag_root.golden",
			wantExit:   1,
		},
		{
			name:       "unknown flag on config validate",
			args:       []string{cmdConfig, cmdValidate, unknownFlag},
			wantGolden: "err_unknown_flag_config_validate.golden",
			wantExit:   1,
		},
		{
			name:       "unknown flag on config print",
			args:       []string{cmdConfig, cmdPrint, unknownFlag},
			wantStderr: errUnknownFlagStderr,
			wantExit:   1,
		},
		{
			name:       "unknown flag on evaluate",
			args:       []string{cmdEvaluate, unknownFlag},
			wantStderr: errUnknownFlagStderr,
			wantExit:   1,
		},
		{
			name:       "unknown flag on lint",
			args:       []string{cmdLint, unknownFlag},
			wantStderr: errUnknownFlagStderr,
			wantExit:   1,
		},
		{
			name:       "unknown flag on generate manifest",
			args:       []string{cmdGenerate, cmdManifest, unknownFlag},
			wantStderr: errUnknownFlagStderr,
			wantExit:   1,
		},
		{
			name:       "unknown flag on publish",
			args:       []string{cmdPublish, unknownFlag},
			wantGolden: "err_unknown_flag_publish.golden",
			wantExit:   1,
		},
		{
			name:       "unknown flag on publish verify",
			args:       []string{cmdPublish, cmdVerify, unknownFlag},
			wantStderr: errUnknownFlagStderr,
			wantExit:   1,
		},
		{
			name:       "missing file arg on config validate",
			args:       []string{cmdConfig, cmdValidate},
			wantGolden: "err_missing_arg_config_validate.golden",
			wantExit:   1,
		},
		{
			name:       "missing file arg on config print",
			args:       []string{cmdConfig, cmdPrint, flagEffective},
			wantGolden: "err_missing_arg_config_print.golden",
			wantExit:   1,
		},
		{
			name:       "missing --effective flag on config print",
			args:       []string{cmdConfig, cmdPrint, "somefile.yaml"},
			wantStderr: "  ERROR   error executing command: usage: --effective is required\n",
			wantExit:   1,
		},
		{
			name:       "missing dir arg on publish",
			args:       []string{cmdPublish},
			wantGolden: "err_missing_arg_publish.golden",
			wantExit:   1,
		},
		{
			name:       "missing required flag on generate manifest",
			args:       []string{cmdGenerate, cmdManifest, flagConfig, "../evaluate/testdata/flag.goff.yaml"},
			wantGolden: "err_missing_flag_generate_manifest.golden",
			wantExit:   1,
		},
		{
			name:       "missing subcommand on generate",
			args:       []string{cmdGenerate},
			wantStderr: "  ERROR   error executing command: usage: expected \"manifest\"\n",
			wantExit:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, code := runCLIClean(t, nil, tt.args...)
			assert.Equal(t, tt.wantExit, code)
			assert.Empty(t, stdout)

			wantStderr := tt.wantStderr
			if tt.wantGolden != "" {
				wantStderr = readGolden(t, tt.wantGolden)
			}

			assert.Equal(t, wantStderr, stderr)
		})
	}
}
