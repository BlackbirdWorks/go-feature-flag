package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pterm/pterm"
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
	errUnknownFlagStderr     = "  ERROR   error executing command: unknown flag: --not-a-flag\n"
)

// runCLIClean runs the CLI in a subprocess with an explicit minimal environment so that host variables (including
// COLUMNS or AWS configuration) never leak into golden output assertions.
func runCLIClean(t *testing.T, extraEnv []string, args ...string) (string, string, int) {
	t.Helper()

	cmd := exec.CommandContext(
		t.Context(),
		os.Args[0],
		append([]string{"-test.run=^TestHelperProcess$", "--"}, args...)...,
	)

	const baseEnvCount = 8

	env := make([]string, 0, baseEnvCount+len(extraEnv))
	env = append(env,
		helperEnv+"=1",
		"NO_COLOR=1",
		"GOCOVERDIR="+t.TempDir(),
		"AWS_ACCESS_KEY_ID=test",
		"AWS_SECRET_ACCESS_KEY=test",
		"AWS_EC2_METADATA_DISABLED=true",
		"AWS_CONFIG_FILE="+os.DevNull,
		"AWS_SHARED_CREDENTIALS_FILE="+os.DevNull,
	)
	env = append(env, extraEnv...)
	cmd.Env = env

	var stdout, stderr bytes.Buffer

	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()

	code := 0
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		code = exitErr.ExitCode()
	} else {
		require.NoError(t, err)
	}

	return pterm.RemoveColorFromString(stdout.String()), pterm.RemoveColorFromString(stderr.String()), code
}

func readGolden(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(goldenDir, name))
	require.NoError(t, err)

	return string(data)
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
	}{
		{name: "root COLUMNS=40", columns: "COLUMNS=40", args: []string{helpFlag}, wantGolden: helpRootGolden},
		{name: "root COLUMNS=80", columns: "COLUMNS=80", args: []string{helpFlag}, wantGolden: helpRootGolden},
		{name: "root COLUMNS=200", columns: "COLUMNS=200", args: []string{helpFlag}, wantGolden: helpRootGolden},
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
		},
		{
			name:       "config validate COLUMNS=200",
			columns:    "COLUMNS=200",
			args:       []string{cmdConfig, cmdValidate, helpFlag},
			wantGolden: helpConfigValidateGolden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, code := runCLIClean(t, []string{tt.columns}, tt.args...)
			assert.Equal(t, 0, code)
			assert.Empty(t, stderr)
			assert.Equal(t, readGolden(t, tt.wantGolden), stdout)
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
			wantStderr: "  ERROR   error executing command: usage: unknown flag: --not-a-flag\n",
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
			wantStderr: "  ERROR   error executing command: usage: unknown flag: --not-a-flag\n",
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
			args:       []string{cmdGenerate, cmdManifest, flagConfig, "evaluate/testdata/flag.goff.yaml"},
			wantGolden: "err_missing_flag_generate_manifest.golden",
			wantExit:   1,
		},
		{
			name: "missing subcommand on generate",
			args: []string{cmdGenerate},
			wantStderr: "  ERROR   you must specify a subcommand (e.g., manifest)\n" +
				"  ERROR   error executing command: no subcommand provided\n",
			wantExit: 1,
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
