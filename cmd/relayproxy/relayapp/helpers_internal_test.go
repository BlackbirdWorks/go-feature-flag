package relayapp

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

func TestShutdownSettingsOfTheFile(t *testing.T) {
	t.Parallel()

	delay := bootstrap.Delay(5 * time.Second)
	timeout := bootstrap.Duration(time.Minute)

	assert.Equal(t, time.Duration(0), shutdownDelay(nil), "unset is no delay")
	assert.Equal(t, 5*time.Second, shutdownDelay(&delay))
	assert.Equal(t, bootstrap.DefaultShutdownTimeout, shutdownTimeoutOf(nil), "unset is the default")
	assert.Equal(t, time.Minute, shutdownTimeoutOf(&timeout))
}

func TestSleepContext(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	tests := []struct {
		ctx     context.Context //nolint:containedctx // the table gives each row its context
		name    string
		delay   time.Duration
		wantErr bool
	}{
		{name: "the delay passes", ctx: t.Context(), delay: 0},
		{name: "the context ends first", ctx: cancelled, delay: time.Hour, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := sleepContext(tt.ctx, tt.delay)
			if tt.wantErr {
				require.ErrorIs(t, err, context.Canceled)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestForceAfter(t *testing.T) {
	t.Parallel()

	t.Run("the second signal closes force", func(t *testing.T) {
		t.Parallel()

		signals := make(chan os.Signal, 2)
		force := make(chan struct{})

		signals <- syscall.SIGTERM

		signals <- syscall.SIGINT

		forceAfter(t.Context(), signals, force)

		select {
		case <-force:
		default:
			require.FailNow(t, "force was not closed")
		}
	})

	t.Run("the end of the process stops the wait", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		force := make(chan struct{})
		forceAfter(ctx, make(chan os.Signal), force)

		select {
		case <-force:
			require.FailNow(t, "force must stay open")
		default:
		}
	})
}
