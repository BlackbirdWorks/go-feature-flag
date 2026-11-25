package ffclient

import (
	"bytes"
	"log"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	legacy2 = "[legacy] "
)

func TestConfig_serviceLogger(t *testing.T) {
	t.Parallel()

	leveled := slog.New(slog.DiscardHandler)

	tests := []struct {
		leveled    *slog.Logger
		name       string
		wantOutput string
		legacy     bool
		wantNil    bool
		wantSame   bool
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

	tests := []struct {
		name         string
		prefix       string
		wantFirst    string
		wantSecond   string
		wantNotFirst string
		flags        int
		redirect     bool
	}{
		{name: "same output", prefix: "", flags: 0, wantFirst: "msg=one"},
		{name: "the prefix of the legacy logger", prefix: legacy2, flags: 0, wantFirst: legacy2},
		{
			name:      "the flags of the legacy logger",
			prefix:    legacy2,
			flags:     log.Lmsgprefix,
			wantFirst: "[legacy] time=",
		},
		{
			name:         "SetOutput is followed",
			prefix:       "",
			flags:        0,
			redirect:     true,
			wantFirst:    "msg=one",
			wantSecond:   "msg=two",
			wantNotFirst: "msg=two",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var first, second bytes.Buffer

			legacy := log.New(&first, tt.prefix, tt.flags)
			logger := (&Config{Logger: legacy}).serviceLogger()

			logger.Info("one")

			if tt.redirect {
				legacy.SetOutput(&second)
			}

			logger.Info("two")

			assert.Contains(t, first.String(), tt.wantFirst)

			if tt.wantSecond != "" {
				assert.Contains(t, second.String(), tt.wantSecond)
			}

			if tt.wantNotFirst != "" {
				assert.NotContains(t, first.String(), tt.wantNotFirst)
			}
		})
	}
}
