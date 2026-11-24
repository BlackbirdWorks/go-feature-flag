package main

import (
	"bytes"
	"testing"

	"github.com/pterm/pterm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCmdExist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantOut string
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
