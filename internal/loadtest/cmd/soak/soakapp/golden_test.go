package soakapp_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/loadtest/cmd/soak/soakapp"
)

func TestHelpGolden(t *testing.T) {
	t.Parallel()

	golden, err := os.ReadFile(filepath.Join("testdata", "help.golden"))
	require.NoError(t, err)

	tests := []struct {
		name string
		args []string
	}{
		{name: "long help", args: []string{"--help"}},
		{name: "short help", args: []string{"-h"}},
		{name: "legacy single-dash help", args: []string{"-help"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			code := soakapp.Main(t.Context(), tt.args, &stdout, &stderr)
			assert.Equal(t, 0, code)
			assert.Empty(t, stderr.String())
			assert.Equal(t, unwrapped(string(golden)), unwrapped(stdout.String()))
		})
	}
}

func TestUnknownFlagGolden(t *testing.T) {
	t.Parallel()

	golden, err := os.ReadFile(filepath.Join("testdata", "unknown_flag.golden"))
	require.NoError(t, err)

	helpGolden, err := os.ReadFile(filepath.Join("testdata", "help.golden"))
	require.NoError(t, err)

	tests := []struct {
		name     string
		wantText string
		args     []string
	}{
		{name: "double-dash unknown flag", args: []string{"--nope"}, wantText: string(golden)},
		{
			name:     "single-dash unknown flag",
			args:     []string{"-nope"},
			wantText: "soak: invalid command line: unknown flag -n, did you mean \"-h\"?\n\n" + string(helpGolden),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			code := soakapp.Main(t.Context(), tt.args, &stdout, &stderr)
			assert.Equal(t, 2, code)
			assert.Empty(t, stdout.String())
			assert.Equal(t, unwrapped(tt.wantText), unwrapped(stderr.String()))
		})
	}
}

// unwrapped collapses every run of white space, so that terminal width wrapping does not affect comparisons.
func unwrapped(text string) string { return strings.Join(strings.Fields(text), " ") }
