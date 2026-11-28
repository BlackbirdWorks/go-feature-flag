package valkeysegment_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

const closeBound = 10 * time.Second

// closeClient is a client whose Close ends when the test releases it. It never serves a command.
type closeClient struct {
	valkey.Client

	hold    chan struct{} // Close waits for it to be closed, when it is not nil
	started chan struct{}
	done    chan struct{}
	closes  atomic.Int32
	once    sync.Once
}

func newCloseClient(hang bool) *closeClient {
	c := &closeClient{started: make(chan struct{}), done: make(chan struct{})}
	if hang {
		c.hold = make(chan struct{})
	}

	return c
}

func (*closeClient) Mode() valkey.ClientMode { return valkey.ClientModeStandalone }

func (c *closeClient) Close() {
	if c.closes.Add(1) == 1 {
		close(c.started)
	}

	if c.hold != nil {
		<-c.hold
	}

	if c.closes.Load() == 1 {
		close(c.done)
	}
}

// release lets a held Close end. It is safe to call more than once.
func (c *closeClient) release() {
	if c.hold != nil {
		c.once.Do(func() { close(c.hold) })
	}
}

func (*closeClient) wait(tb testing.TB, ch <-chan struct{}, what string) {
	tb.Helper()

	select {
	case <-ch:
	case <-time.After(closeBound):
		require.Fail(tb, "timed out waiting for "+what)
	}
}

func TestNewOwning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr    error
		name       string
		hang       bool
		endedCtx   bool
		wantCloses int32
	}{
		{name: "a prompt close ends Shutdown without an error", wantCloses: 1},
		{
			name: "a hung close does not hold Shutdown past its deadline", hang: true, endedCtx: true,
			wantErr: context.Canceled, wantCloses: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := newCloseClient(tt.hang)
			t.Cleanup(client.release) // a failed case never leaves the close held

			prov, err := valkeysegment.NewOwning(client, &valkeysegment.Options{})
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			if tt.endedCtx {
				cancel()
			}

			err = prov.Shutdown(ctx)
			if tt.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.wantErr)
			}

			client.wait(t, client.started, "the close to start")

			if tt.hang {
				select {
				case <-client.done:
					require.Fail(t, "the close ended before it was released")
				default:
				}

				client.release() // the late close ends in the background
			}

			client.wait(t, client.done, "the close to end")
			assert.Equal(t, tt.wantCloses, client.closes.Load())

			// a second Shutdown and a Close close nothing again
			require.NoError(t, prov.Shutdown(context.Background()))
			prov.Close()
			assert.Equal(t, tt.wantCloses, client.closes.Load())
		})
	}
}

func TestNewOwningRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		client  valkeysegment.OwnedClient
		wantErr error
		opts    *valkeysegment.Options
		name    string
	}{
		{name: "a nil client", opts: &valkeysegment.Options{}, wantErr: valkeysegment.ErrNilClient},
		{
			name: "invalid options", client: newCloseClient(false),
			opts: &valkeysegment.Options{Timeout: -1}, wantErr: valkeysegment.ErrInvalidOptions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prov, err := valkeysegment.NewOwning(tt.client, tt.opts)

			require.ErrorIs(t, err, tt.wantErr)
			assert.Nil(t, prov)
		})
	}
}

func TestDefaultConnWriteTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call time.Duration
		want time.Duration
	}{
		{name: "zero is the default call timeout, at the floor", call: 0, want: time.Second},
		{name: "a short call timeout is at the floor", call: 50 * time.Millisecond, want: time.Second},
		{name: "ten call timeouts", call: 500 * time.Millisecond, want: 5 * time.Second},
		{name: "capped", call: time.Hour, want: valkeysegment.MaxConnWriteTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, valkeysegment.DefaultConnWriteTimeout(tt.call))
		})
	}
}

func TestOptionsConnWriteTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr error
		name    string
		timeout time.Duration
	}{
		{name: "unset", timeout: 0},
		{name: "the lower bound", timeout: valkeysegment.MinConnWriteTimeout},
		{name: "the upper bound", timeout: valkeysegment.MaxConnWriteTimeout},
		{name: "below the lower bound", timeout: time.Millisecond, wantErr: valkeysegment.ErrInvalidOptions},
		{name: "negative", timeout: -time.Second, wantErr: valkeysegment.ErrInvalidOptions},
		{name: "above the upper bound", timeout: 2 * time.Minute, wantErr: valkeysegment.ErrInvalidOptions},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := loaded(t)
			opts := atomicOptions(nil)
			opts.ConnWriteTimeout = tt.timeout

			prov, err := valkeysegment.Dial(srv.clientOption(), opts)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, prov)

				return
			}

			require.NoError(t, err)
			require.NoError(t, prov.Shutdown(context.Background()))
		})
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		wantCloses int32
	}{
		{name: "the caller owns the client: Shutdown and Close do not close it", wantCloses: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := newCloseClient(false)
			prov, err := valkeysegment.New(client, &valkeysegment.Options{})
			require.NoError(t, err)

			require.NoError(t, prov.Shutdown(context.Background()))
			prov.Close()
			assert.Equal(t, tt.wantCloses, client.closes.Load())
		})
	}
}

func TestDialKeepsTheConnWriteTimeoutOfTheClientOption(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		set       time.Duration
		wantReady bool
	}{
		{name: "set in the client option", set: 30 * time.Second, wantReady: true},
		{name: "not set", wantReady: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := loaded(t)
			opt := srv.clientOption()
			opt.ConnWriteTimeout = tt.set

			prov, err := valkeysegment.Dial(opt, atomicOptions(nil))
			require.NoError(t, err)
			require.NoError(t, prov.Init(context.Background()))
			assert.Equal(t, tt.wantReady, prov.Status().Ready)
			require.NoError(t, prov.Shutdown(context.Background()))
		})
	}
}
