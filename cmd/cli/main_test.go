package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pterm/pterm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCmdExist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantOut string
		args    []string
	}{
		{
			name:    "lint command should exist",
			args:    []string{"lint", "linter/testdata/valid.yaml", "--format", "yaml"},
			wantOut: "  INFO    Valid Semaphore configuration\n",
		},
		{
			name: "evaluate command should exist",
			args: []string{
				"evaluate", "--config", "evaluate/testdata/flag.goff.yaml",
				"--ctx", `{"targetingKey": "user-123"}`, "--format", "yaml",
			},
			wantOut: `Flag --config has been deprecated, Use path instead
{
  "simple-flag": {
    "trackEvents": true,
    "variationType": "B",
    "failed": false,
    "version": "",
    "reason": "STATIC",
    "errorCode": "",
    "value": true,
    "cacheable": true,
    "metadata": {
      "description": "this is a simple feature flag",
      "issue-link": "https://jira.xxx/GOFF-01"
    }
  },
  "test-flag": {
    "trackEvents": true,
    "variationType": "Default",
    "failed": false,
    "version": "",
    "reason": "DEFAULT",
    "errorCode": "",
    "value": false,
    "cacheable": true,
    "metadata": {
      "description": "this is a simple feature flag",
      "issue-link": "https://jira.xxx/GOFF-01"
    }
  },
  "test-flag2": {
    "trackEvents": true,
    "variationType": "Default",
    "failed": false,
    "version": "",
    "reason": "DEFAULT",
    "errorCode": "",
    "value": false,
    "cacheable": true
  }
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := initRootCmd()

			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs(tt.args)
			require.NoError(t, cmd.Execute())
			assert.Equal(t, tt.wantOut, pterm.RemoveColorFromString(out.String()))
		})
	}
}

func TestVersionFlag(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--version", "-v"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			cmd := initRootCmd()
			cmd.SetOut(&out)
			cmd.SetArgs([]string{flag})
			require.NoError(t, cmd.Execute())
			assert.Contains(t, out.String(), "semaphore-cli localdev\ncommit: ")
			assert.Contains(t, out.String(), "\nfips140: false\n")
		})
	}
}

const (
	fatalLine   = "error executing command"
	unknownFlag = "--not-a-flag"
	usageText   = "Usage:"
	cmdConfig   = "config"
	cmdPublish  = "publish"
	fmtJSON     = "json"
	flagFormat  = "--format"
	formatYAML  = "yaml"
)

func TestConfigAndPublishCommandsAreRegistered(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "bootstrap.yaml")
	require.NoError(
		t,
		os.WriteFile(file, []byte("version: 2\nconnections:\n  c: {type: s3, region: us-east-1, bucket: b}\n"), 0o600),
	)

	tests := []struct {
		name    string
		wantOut string
		args    []string
	}{
		{name: "config validate", args: []string{cmdConfig, "validate", file}, wantOut: file + ": valid\n"},
		{
			name:    "config print",
			args:    []string{cmdConfig, "print", "--effective", file, "-o", fmtJSON},
			wantOut: `"bucket": "b"`,
		},
		{
			name:    "publish verify exists",
			args:    []string{cmdPublish, "verify", "--help"},
			wantOut: "Reads the manifest and every listed object",
		},
		{name: "publish exists", args: []string{cmdPublish, "--help"}, wantOut: "--dry-run"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := initRootCmd()

			var out bytes.Buffer

			cmd.SetOut(&out)
			cmd.SetArgs(tt.args)
			require.NoError(t, cmd.Execute())
			assert.Contains(t, pterm.RemoveColorFromString(out.String()), tt.wantOut)
		})
	}
}

// helperEnv makes the test binary act as the command line: TestHelperProcess runs the arguments after "--".
const helperEnv = "SEMAPHORE_CLI_TEST_HELPER"

// TestHelperProcess is not a test: it is the entry point of the process that runCLI starts.
//
//nolint:paralleltest // the helper process runs one command line and exits
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		t.Skip("only runs as the helper process of runCLI")
	}

	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]

			break
		}
	}

	os.Exit(execute(args)) //nolint:revive // the helper process ends with the exit code of the command line
}

// runCLI runs the command line in a process of its own and returns what it wrote on each stream and its exit code.
func runCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()

	cmd := exec.CommandContext(
		t.Context(),
		os.Args[0],
		append([]string{"-test.run=^TestHelperProcess$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), helperEnv+"=1")

	var stdout, stderr bytes.Buffer

	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()

	code := 0
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		code = exitErr.ExitCode()
	} else {
		require.NoError(t, err)
	}

	return stdout.String(), stderr.String(), code
}

func TestWholeProcessStreams(t *testing.T) {
	t.Parallel()

	v1File := filepath.Join(t.TempDir(), "v1.yaml")
	require.NoError(t, os.WriteFile(v1File, []byte("port: 1031\n"), 0o600))

	tests := []struct {
		name          string
		wantStdout    string // a text that stdout holds (empty: stdout must be empty)
		wantStderr    string
		notInStdout   string
		wantJSONError string // the code of the one JSON value that stdout holds
		args          []string
		wantExit      int
	}{
		{
			name: "invalid root flag: usage and Cobra's error on stderr", args: []string{unknownFlag},
			wantStderr: usageText, notInStdout: usageText, wantStdout: fatalLine, wantExit: 1,
		},
		{
			name: "invalid flag of a command in text mode", args: []string{cmdPublish, "verify", unknownFlag},
			wantStderr: "unknown flag: --not-a-flag", notInStdout: usageText, wantExit: 1,
		},
		{
			name: "invalid flag of a command in JSON mode", args: []string{cmdPublish, "-o", fmtJSON, unknownFlag},
			wantStderr: fatalLine, notInStdout: usageText, wantJSONError: "usage", wantExit: 1,
		},
		{
			name: "missing argument in JSON mode", args: []string{cmdPublish, "-o", fmtJSON},
			wantStderr: fatalLine, notInStdout: usageText, wantJSONError: "usage", wantExit: 1,
		},
		{
			name:          "invalid bootstrap file in JSON mode",
			args:          []string{cmdConfig, "print", "--effective", v1File, "-o", fmtJSON},
			wantStderr:    fatalLine,
			notInStdout:   fatalLine,
			wantJSONError: "invalid",
			wantExit:      2,
		},
		{
			name: "invalid bootstrap file in text mode", args: []string{cmdConfig, "validate", v1File},
			wantStderr: "version 2 required", wantExit: 2,
		},
		{
			name:       "another command keeps its error line on stdout",
			args:       []string{"lint", filepath.Join(t.TempDir(), "missing.yaml"), flagFormat, formatYAML},
			wantStdout: fatalLine,
			wantExit:   1,
		},
		{name: "success", args: []string{"--version"}, wantStdout: "semaphore-cli"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, code := runCLI(t, tt.args...)
			stdout, stderr = pterm.RemoveColorFromString(stdout), pterm.RemoveColorFromString(stderr)

			assert.Equal(t, tt.wantExit, code, "stdout %q stderr %q", stdout, stderr)

			if tt.wantStdout == "" && tt.wantJSONError == "" {
				assert.Empty(t, stdout)
			} else {
				assert.Contains(t, stdout, tt.wantStdout)
			}

			assert.Contains(t, stderr, tt.wantStderr)

			if tt.notInStdout != "" {
				assert.NotContains(t, stdout, tt.notInStdout)
			}

			if tt.wantJSONError == "" {
				return
			}

			dec := json.NewDecoder(strings.NewReader(stdout))

			var doc struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}

			require.NoError(t, dec.Decode(&doc), "stdout: %q", stdout)
			assert.False(t, dec.More(), "stdout holds exactly one JSON value")
			assert.Equal(t, tt.wantJSONError, doc.Error.Code)
		})
	}
}
