package ffclient

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_serviceLogger(t *testing.T) {
	t.Parallel()

	leveled := slog.New(slog.DiscardHandler)

	tests := []struct {
		leveled  *slog.Logger
		name     string
		wantNil  bool
		wantSame bool
	}{
		{name: "the leveled logger", leveled: leveled, wantSame: true},
		{name: "no logger", wantNil: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			config := Config{LeveledLogger: tt.leveled}
			got := config.serviceLogger()

			if tt.wantNil {
				assert.Nil(t, got)

				return
			}

			assert.Same(t, leveled, got)
		})
	}
}

func TestConfig_Initialize_Logger(t *testing.T) {
	t.Parallel()

	leveled := slog.New(slog.DiscardHandler)

	tests := []struct {
		leveled  *slog.Logger
		name     string
		wantSame bool
	}{
		{name: "the leveled logger is kept", leveled: leveled, wantSame: true},
		{name: "a discarding logger when none is configured"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			config := Config{LeveledLogger: tt.leveled}
			config.Initialize()

			require.NotNil(t, config.internalLogger)

			if tt.wantSame {
				assert.Same(t, leveled, config.internalLogger)
			}
		})
	}
}
