package valkeysegment_test

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

func TestNewRequiresContextTimeoutOfAGoRedisClient(t *testing.T) {
	t.Parallel()

	const addr = "127.0.0.1:1" // never dialed: go-redis connects on the first command

	tests := []struct {
		client  func() valkeysegment.Client
		name    string
		wantErr bool
	}{
		{
			name:    "a node client without it",
			client:  func() valkeysegment.Client { return redis.NewClient(&redis.Options{Addr: addr}) },
			wantErr: true,
		},
		{
			name: "a node client with it",
			client: func() valkeysegment.Client {
				return redis.NewClient(&redis.Options{Addr: addr, ContextTimeoutEnabled: true})
			},
		},
		{
			name: "a cluster client without it",
			client: func() valkeysegment.Client {
				return redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{addr}})
			},
			wantErr: true,
		},
		{
			name: "a cluster client with it",
			client: func() valkeysegment.Client {
				return redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{addr}, ContextTimeoutEnabled: true})
			},
		},
		{
			name:   "a client that does not expose its options",
			client: func() valkeysegment.Client { return &fakeStore{} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prov, err := valkeysegment.New(tt.client(), &valkeysegment.Options{})

			if tt.wantErr {
				require.ErrorIs(t, err, valkeysegment.ErrContextTimeoutDisabled)
				require.ErrorIs(t, err, valkeysegment.ErrInvalidOptions)
				assert.Nil(t, prov)

				return
			}

			require.NoError(t, err)
			assert.NotNil(t, prov)
		})
	}
}

// silentServer accepts connections and reads them, and never answers.
func silentServer(t *testing.T) string {
	t.Helper()

	lis, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = lis.Close() })

	go func() {
		for {
			conn, acceptErr := lis.Accept()
			if acceptErr != nil {
				return
			}

			go func() {
				defer conn.Close()

				_, _ = io.Copy(io.Discard, conn) // ends when the client closes the connection
			}()
		}
	}()

	return lis.Addr().String()
}

// A client that does not honour the deadline of the context keeps the call on the socket until its own read
// timeout. Dial sets ContextTimeoutEnabled, so the call ends at the timeout of the provider: the read timeout of this
// client is an hour, and the test waits for the call under a bound, never for a measured time.
func TestDialEndsACallAtItsDeadlineOnTheSocket(t *testing.T) {
	t.Parallel()

	redisOpts := &redis.UniversalOptions{
		Addrs:           []string{silentServer(t)},
		Protocol:        2,
		DisableIdentity: true,
		ReadTimeout:     time.Hour,
		MaxRetries:      -1,
	}
	prov, err := valkeysegment.Dial(redisOpts, &valkeysegment.Options{
		Timeout:  50 * time.Millisecond,
		Segments: map[string]valkeysegment.SegmentOptions{segBeta: {}},
	})
	require.NoError(t, err)

	t.Cleanup(func() { _ = prov.Close() })

	assert.False(t, redisOpts.ContextTimeoutEnabled, "Dial changes a copy of the options of the caller")

	lookups := []segment.Lookup{lk(segBeta, idAlice)}
	done := make(chan error, 1)

	go func() { done <- prov.Contains(context.Background(), lookups) }()

	select {
	case err = <-done:
		require.ErrorIs(t, err, valkeysegment.ErrTimeout)
		assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))
	case <-boundedCtx(t).Done():
		require.FailNow(t, "the call did not end at its deadline")
	}
}

func TestACompletedReplySurvivesALaterTransportError(t *testing.T) {
	t.Parallel()

	keys := map[string]bool{flagPrefix + idAlice: true}
	sets := failureSetBeta()

	runFailureCases(t, []failureCase{
		{
			name:        "keys layout: the connection closes after the first reply",
			store:       &fakeStore{keys: keys, exec: func(context.Context) error { return errDial }, completed: 1},
			lookups:     []segment.Lookup{lk("f", idAlice), lk("f", "bob")},
			wantStates:  []segment.State{segment.Member, segment.Unknown},
			wantClass:   valkeysegment.ClassUnavailable,
			wantSentry:  valkeysegment.ErrUnavailable,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: msgUnavailable1of2,
		},
		{
			name:        "keys layout: it closes before any reply",
			store:       &fakeStore{keys: keys, exec: func(context.Context) error { return errDial }},
			lookups:     []segment.Lookup{lk("f", idAlice), lk("f", "bob")},
			wantStates:  []segment.State{segment.Unknown, segment.Unknown},
			wantClass:   valkeysegment.ClassUnavailable,
			wantSentry:  valkeysegment.ErrUnavailable,
			wantFailed:  2,
			wantPipes:   1,
			wantMessage: "valkey segment: unavailable: 2 of 2 lookups failed",
		},
		{
			name:        "set layout: the members answered, the key check did not",
			store:       &fakeStore{sets: sets, exec: func(context.Context) error { return errDial }, completed: 1},
			lookups:     []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")},
			wantStates:  []segment.State{segment.Member, segment.Unknown},
			wantClass:   valkeysegment.ClassUnavailable,
			wantSentry:  valkeysegment.ErrUnavailable,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: msgUnavailable1of2,
		},
	})
}
