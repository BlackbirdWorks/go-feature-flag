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
	tests := []struct {
		name       string
		legacy     bool
		leveled    *slog.Logger
		wantNil    bool
		wantSame   bool
		wantOutput string
	}{
		{name: "the leveled logger wins", legacy: true, leveled: leveled, wantSame: true},
		{name: "no logger", wantNil: true},
		{name: "the legacy logger is seen as a slog handler", legacy: true, wantOutput: "msg=hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			config := Config{LeveledLogger: tt.leveled}
			if tt.legacy {
				config.Logger = log.New(&buf, "", 0)
			}

			got := config.serviceLogger()

			switch {
			case tt.wantNil:
				assert.Nil(t, got)
			case tt.wantSame:
				assert.Same(t, leveled, got)
			default:
				got.Info("hello")
				assert.Contains(t, buf.String(), tt.wantOutput)
			}
		})
	}
}

func TestConfig_serviceLogger_FollowsTheLegacyLogger(t *testing.T) {
	t.Parallel()

	var first, second bytes.Buffer
	legacy := log.New(&first, "", 0)
	logger := (&Config{Logger: legacy}).serviceLogger()

	logger.Info("one")
	legacy.SetOutput(&second)
	logger.Info("two")

	assert.Contains(t, first.String(), "msg=one")
	assert.NotContains(t, first.String(), "msg=two")
	assert.Contains(t, second.String(), "msg=two")
}
