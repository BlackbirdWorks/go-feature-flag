package main

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
)

type recordingAPIServer struct{ calls *[]string }

func (s recordingAPIServer) Stop(_ context.Context) { *s.calls = append(*s.calls, "api.Stop") }

type recordingFlagsetManager struct {
	service.FlagsetManager
	calls *[]string
}

func (m recordingFlagsetManager) Close() { *m.calls = append(*m.calls, "flagsets.Close") }

func TestShutdown(t *testing.T) {
	t.Parallel()

	var calls []string
	shutdown(
		zap.NewNop(),
		recordingAPIServer{calls: &calls},
		stream.NewWebsocketService(),
		stream.NewSSEService(),
		recordingFlagsetManager{calls: &calls},
	)
	// The flagsets must be closed (flushing the exporters) after the server stopped accepting requests.
	assert.Equal(t, []string{"api.Stop", "flagsets.Close"}, calls)
}

func TestVersionRequested(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "no argument", args: nil, want: false},
		{name: "long flag", args: []string{"--version"}, want: true},
		{name: "short flag", args: []string{"-v"}, want: true},
		{name: "after the config flag", args: []string{"--config=/x.yaml", "--version"}, want: true},
		{name: "config only", args: []string{"--config=/x.yaml"}, want: false},
		{name: "unknown flags are ignored", args: []string{"--nope", "-v"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, versionRequested(tt.args))
		})
	}
}

func TestVersionText(t *testing.T) {
	t.Parallel()

	got := versionText()
	assert.True(t, strings.HasPrefix(got, "semaphore "), got)
	assert.Contains(t, got, "\nfips140: false\n")
}
