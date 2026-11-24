package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fastRun keeps a run to about a second: 100 ms samples, a one sample warm-up, and a heap tolerance that
// cannot flake (the verdict logic itself is tested in the loadtest package).
func fastRun() []string {
	return []string{
		"-duration", "1s",
		"-interval", "100ms",
		"-warmup", "1",
		"-workers", "2",
		"-heap-tolerance", "100",
	}
}

func TestRun(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout []string
		wantStderr string
	}{
		{name: "help", args: []string{"-h"}, wantCode: 0, wantStderr: "Usage of soak"},
		{name: "unknown flag", args: []string{"-nope"}, wantCode: 2, wantStderr: "flag provided but not defined"},
		{name: "unknown target", args: append([]string{"-target", "bogus"}, fastRun()...), wantCode: 2,
			wantStderr: `unknown target "bogus"`},
		{name: "unknown exporter", args: append([]string{"-exporter", "bogus"}, fastRun()...), wantCode: 2,
			wantStderr: "bogus"},
		{name: "unknown endpoint", args: append([]string{"-target", "relay", "-endpoint", "bogus"}, fastRun()...),
			wantCode: 2, wantStderr: `unknown endpoint "bogus"`},
		{name: "unknown protocol", args: append([]string{"-target", "relay", "-protocol", "quic"}, fastRun()...),
			wantCode: 2, wantStderr: `unknown protocol "quic"`},
		{name: "exporter that the endpoint never exercises", wantCode: 2, wantStderr: "does not emit events",
			args: append([]string{"-target", "relay", "-endpoint", "ofrep-bulk", "-exporter", "slow"}, fastRun()...)},
		{name: "library target", wantCode: 0,
			args:       append([]string{"-target", "library"}, fastRun()...),
			wantStdout: []string{"# target=library exporter=discard workers=2", "PASS memory-stable", "window ("}},
		{name: "relay bulk endpoint over h2c", wantCode: 0,
			args: append([]string{"-target", "relay", "-endpoint", "ofrep-bulk", "-protocol", "h2c",
				"-exporter", "discard"}, fastRun()...),
			wantStdout: []string{"endpoint=ofrep-bulk protocol=h2c", "PASS memory-stable", "flags/s"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), tt.args, &stdout, &stderr)
			assert.Equal(t, tt.wantCode, code, "stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
			for _, w := range tt.wantStdout {
				assert.Contains(t, stdout.String(), w)
			}
			assert.Contains(t, stderr.String(), tt.wantStderr)
		})
	}
}

func TestRunInterrupted(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	code := run(ctx, append([]string{"-target", "library"}, fastRun()...), &stdout, &stderr)
	assert.Equal(t, 130, code)
	assert.Contains(t, stderr.String(), "interrupted")
}

// TestRunCPUProfile is the only test that starts a CPU profile (there is one per process).
func TestRunCPUProfile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cpu.pprof")
	var stdout, stderr bytes.Buffer
	code := run(
		context.Background(),
		append([]string{"-target", "library", "-cpuprofile", path}, fastRun()...),
		&stdout,
		&stderr,
	)
	require.Equal(t, 0, code, stderr.String())
	st, err := os.Stat(path)
	require.NoError(t, err)
	assert.NotZero(t, st.Size())

	code = run(
		context.Background(),
		[]string{"-cpuprofile", filepath.Join(t.TempDir(), "missing", "cpu.pprof")},
		&stdout,
		&stderr,
	)
	assert.Equal(t, 2, code, "an unwritable profile path is a usage error")
}
