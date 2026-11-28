package soakapp_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/internal/loadtest/cmd/soak/soakapp"
)

const (
	bogus        = "bogus"
	endpointFlag = "--endpoint"
	exporterFlag = "--exporter"
	library      = "library"
	relayName    = "relay"
	targetFlag   = "--target"
)

func TestMain_SetupErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		wantStderr string
		args       []string
		wantCode   int
	}{
		{
			name:       "unknown target",
			args:       []string{targetFlag, bogus},
			wantCode:   2,
			wantStderr: `unknown target "bogus"`,
		},
		{
			name:       "unknown exporter",
			args:       []string{exporterFlag, bogus},
			wantCode:   2,
			wantStderr: bogus,
		},
		{
			name:       "unknown endpoint",
			args:       []string{targetFlag, relayName, endpointFlag, bogus},
			wantCode:   2,
			wantStderr: `unknown endpoint "bogus"`,
		},
		{
			name:       "unknown protocol",
			args:       []string{targetFlag, relayName, "--protocol", "quic"},
			wantCode:   2,
			wantStderr: `unknown protocol "quic"`,
		},
		{
			name:       "exporter that the endpoint never exercises",
			wantCode:   2,
			wantStderr: "does not emit events",
			args:       []string{targetFlag, relayName, endpointFlag, "ofrep-bulk", exporterFlag, "slow"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			code := soakapp.Main(t.Context(), tt.args, &stdout, &stderr)
			assert.Equal(t, tt.wantCode, code, "stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
			assert.Empty(t, stdout.String())
			assert.Contains(t, stderr.String(), tt.wantStderr)
		})
	}
}

func TestMain_Interrupted(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var stdout, stderr bytes.Buffer

	code := soakapp.Main(
		ctx,
		[]string{targetFlag, library, "--duration", "5s", "--interval", "5s", "--workers", "1"},
		&stdout,
		&stderr,
	)
	assert.Equal(t, 130, code)
	assert.Contains(t, stderr.String(), "interrupted")
}
