package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
	cmdConfig  = "config"
	cmdPublish = "publish"
	fmtJSON    = "json"
	flagFormat = "--format"
	formatYAML = "yaml"
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

func TestExecuteKeepsStdoutAsOneJSONDocumentWhenAJSONCommandFails(t *testing.T) {
	t.Parallel()

	v1File := filepath.Join(t.TempDir(), "v1.yaml")
	require.NoError(t, os.WriteFile(v1File, []byte("port: 1031\n"), 0o600))

	tests := []struct {
		name     string
		wantCode string
		args     []string
		wantExit int
	}{
		{
			name:     "invalid bootstrap file",
			args:     []string{cmdConfig, "print", "--effective", v1File, "-o", fmtJSON},
			wantCode: "invalid",
			wantExit: 2,
		},
		{
			name:     "validate of an invalid file",
			args:     []string{cmdConfig, "validate", v1File, "-o", fmtJSON},
			wantCode: "",
			wantExit: 2,
		},
		{
			name:     "publish without a directory",
			args:     []string{cmdPublish, "-o", fmtJSON},
			wantCode: "usage",
			wantExit: 1,
		},
		{
			name:     "publish without a target",
			args:     []string{cmdPublish, t.TempDir(), "-o", fmtJSON},
			wantCode: "usage",
			wantExit: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			code := execute(tt.args, &stdout, &stderr)
			assert.Equal(t, tt.wantExit, code)

			dec := json.NewDecoder(&stdout)

			var doc map[string]any

			require.NoError(t, dec.Decode(&doc), "stdout: %q", stdout.String())
			assert.False(t, dec.More(), "stdout holds one JSON value and nothing after it")

			if tt.wantCode != "" {
				errObj, ok := doc["error"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, tt.wantCode, errObj["code"])
			}

			assert.Contains(t, pterm.RemoveColorFromString(stderr.String()), "error executing command")
		})
	}
}

func TestExecuteKeepsTheErrorLineOfOtherCommandsOnStdout(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	code := execute(
		[]string{"lint", filepath.Join(t.TempDir(), "missing.yaml"), flagFormat, formatYAML},
		&stdout,
		&stderr,
	)

	assert.Equal(t, 1, code)
	assert.Contains(t, pterm.RemoveColorFromString(stdout.String()), "error executing command")
	assert.NotContains(t, stderr.String(), "error executing command")
}

func TestExecuteSucceeds(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	assert.Equal(t, 0, execute([]string{"--version"}, &stdout, &stderr))
	assert.Contains(t, stdout.String(), "semaphore-cli")
}
