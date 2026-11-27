package soakapp_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/loadtest/cmd/soak/soakapp"
)

const (
	bogus        = "bogus"
	endpointFlag = "--endpoint"
	exporterFlag = "--exporter"
	library      = "library"
	passVerdict  = "PASS memory-stable"
	relayName    = "relay"
	targetFlag   = "--target"
)

// fastRun keeps a run to about a second: 100 ms samples, a one sample warm-up, and a heap tolerance that
// cannot flake (the verdict logic itself is tested in the loadtest package).
func fastRun() []string {
	return []string{
		"--duration", "1s",
		"--interval", "100ms",
		"--warmup", "1",
		"--workers", "2",
		"--heap-tolerance", "100",
	}
}

func TestMain_Runs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		wantStderr string
		args       []string
		wantStdout []string
		wantCode   int
	}{
		{
			name:       "unknown target",
			args:       append([]string{targetFlag, bogus}, fastRun()...),
			wantCode:   2,
			wantStderr: `unknown target "bogus"`,
		},
		{
			name:       "unknown exporter",
			args:       append([]string{exporterFlag, bogus}, fastRun()...),
			wantCode:   2,
			wantStderr: bogus,
		},
		{
			name:       "unknown endpoint",
			args:       append([]string{targetFlag, relayName, endpointFlag, bogus}, fastRun()...),
			wantCode:   2,
			wantStderr: `unknown endpoint "bogus"`,
		},
		{
			name:       "unknown protocol",
			args:       append([]string{targetFlag, relayName, "--protocol", "quic"}, fastRun()...),
			wantCode:   2,
			wantStderr: `unknown protocol "quic"`,
		},
		{
			name:       "exporter that the endpoint never exercises",
			wantCode:   2,
			wantStderr: "does not emit events",
			args: append(
				[]string{targetFlag, relayName, endpointFlag, "ofrep-bulk", exporterFlag, "slow"},
				fastRun()...),
		},
		{
			name:       "library target",
			wantCode:   0,
			args:       append([]string{targetFlag, library}, fastRun()...),
			wantStdout: []string{"# target=library exporter=discard workers=2", passVerdict, "window ("},
		},
		{
			name: "library target default workers and legacy flags",
			args: []string{
				"-target", library,
				"-duration", "600ms",
				"-interval", "100ms",
				"-warmup", "1",
				"-heap-tolerance", "100",
			},
			wantCode:   0,
			wantStdout: []string{"# target=library exporter=discard", passVerdict},
		},
		{
			name:       "library target fail verdict on evaluation errors",
			wantCode:   1,
			args:       append([]string{targetFlag, library, "--flag", "does-not-exist"}, fastRun()...),
			wantStdout: []string{"FAIL memory-stable"},
		},
		{
			name: "relay bulk endpoint over h2c",
			args: append([]string{
				targetFlag, relayName, endpointFlag, "ofrep-bulk", "--protocol", "h2c",
				exporterFlag, "discard",
			}, fastRun()...),
			wantCode:   0,
			wantStdout: []string{"endpoint=ofrep-bulk protocol=h2c", passVerdict, "flags/s"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			code := soakapp.Main(t.Context(), tt.args, &stdout, &stderr)
			assert.Equal(t, tt.wantCode, code, "stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())

			for _, want := range tt.wantStdout {
				assert.Contains(t, stdout.String(), want)
			}

			assert.Contains(t, stderr.String(), tt.wantStderr)
		})
	}
}

func TestMain_Interrupted(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var stdout, stderr bytes.Buffer

	code := soakapp.Main(ctx, append([]string{targetFlag, library}, fastRun()...), &stdout, &stderr)
	assert.Equal(t, 130, code)
	assert.Contains(t, stderr.String(), "interrupted")
}

// TestMain_CPUProfile is the only test that starts a CPU profile (there is one per process).
func TestMain_CPUProfile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cpu.pprof")

	var stdout, stderr bytes.Buffer

	code := soakapp.Main(
		t.Context(),
		append([]string{targetFlag, library, "--cpuprofile", path}, fastRun()...),
		&stdout,
		&stderr,
	)
	require.Equal(t, 0, code, stderr.String())

	st, err := os.Stat(path)
	require.NoError(t, err)
	assert.NotZero(t, st.Size())

	code = soakapp.Main(
		t.Context(),
		[]string{"--cpuprofile", filepath.Join(t.TempDir(), "missing", "cpu.pprof")},
		&stdout,
		&stderr,
	)
	assert.Equal(t, 2, code, "an unwritable profile path is a usage error")
}
