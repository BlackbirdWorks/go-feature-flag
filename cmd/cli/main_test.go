package main

import (
	"bytes"
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
		{name: "config validate", args: []string{"config", "validate", file}, wantOut: file + ": valid\n"},
		{
			name:    "config print",
			args:    []string{"config", "print", "--effective", file, "-o", "json"},
			wantOut: `"bucket": "b"`,
		},
		{
			name:    "publish verify exists",
			args:    []string{"publish", "verify", "--help"},
			wantOut: "Reads the manifest and every listed object",
		},
		{name: "publish exists", args: []string{"publish", "--help"}, wantOut: "--dry-run"},
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
