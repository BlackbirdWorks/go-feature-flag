package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCmdExist(t *testing.T) {
	t.Parallel()

	t.Run("lint command should exist", func(t *testing.T) {
		t.Parallel()

		cmd := initRootCmd()
		assert.NotNil(t, cmd)
		cmd.SetArgs([]string{"lint", "linter/testdata/valid.yaml", "--format", "yaml"})
		err := cmd.Execute()
		require.NoError(t, err)
	})
	t.Run("evaluate command should exist", func(t *testing.T) {
		t.Parallel()

		cmd := initRootCmd()
		assert.NotNil(t, cmd)
		cmd.SetArgs(
			[]string{
				"evaluate",
				"--config",
				"evaluate/testdata/flag.goff.yaml",
				"--ctx",
				`{"targetingKey": "user-123"}`,
				"--format",
				"yaml",
			},
		)
		err := cmd.Execute()
		require.NoError(t, err)
	})
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
