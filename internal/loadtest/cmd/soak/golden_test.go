package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoldenBaseline(t *testing.T) {
	t.Parallel()

	defaultWorkers := "(default " + strconv.Itoa(runtime.GOMAXPROCS(0)) + ")"

	tests := []struct {
		name       string
		goldenFile string
		args       []string
		wantCode   int
	}{
		{
			name:       "help long flag",
			args:       []string{"--help"},
			goldenFile: "help.golden",
			wantCode:   0,
		},
		{
			name:       "help short flag",
			args:       []string{"-h"},
			goldenFile: "help.golden",
			wantCode:   0,
		},
		{
			name:       "unknown flag",
			args:       []string{"--nope"},
			goldenFile: "unknown_flag.golden",
			wantCode:   exitUsage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			wantBytes, err := os.ReadFile(filepath.Join("testdata", tt.goldenFile))
			require.NoError(t, err)

			var stdout, stderr bytes.Buffer

			code := run(context.Background(), tt.args, &stdout, &stderr)
			assert.Equal(t, tt.wantCode, code)
			assert.Empty(t, stdout.String())

			got := strings.ReplaceAll(stderr.String(), defaultWorkers, "(default <GOMAXPROCS>)")
			assert.Equal(t, string(wantBytes), got)
		})
	}
}
