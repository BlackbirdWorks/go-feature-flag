package ffclient

import (
	"bytes"
	"log"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_serviceLogger(t *testing.T) {
	t.Parallel()

	leveled := slog.New(slog.DiscardHandler)
	var buf bytes.Buffer
	tests := []struct {
		name       string
		config     Config
		wantSame   *slog.Logger
		wantNil    bool
		wantOutput string
	}{
		{
			name:     "the leveled logger wins",
			config:   Config{LeveledLogger: leveled, Logger: log.New(&buf, "", 0)},
			wantSame: leveled,
		},
		{name: "no logger", config: Config{}, wantNil: true},
		{
			name:       "the legacy logger is seen as a slog handler",
			config:     Config{Logger: log.New(&buf, "", 0)},
			wantOutput: "msg=hello",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buf.Reset()
			got := tt.config.serviceLogger()

			switch {
			case tt.wantNil:
				assert.Nil(t, got)
			case tt.wantSame != nil:
				assert.Same(t, tt.wantSame, got)
			default:
				got.Info("hello")
				assert.Contains(t, buf.String(), tt.wantOutput)
			}
		})
	}
}
