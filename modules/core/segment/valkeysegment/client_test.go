package valkeysegment_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
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

// resp is the RESP form of a command: what go-redis writes for it.
func resp(args ...string) string {
	var out strings.Builder

	fmt.Fprintf(&out, "*%d\r\n", len(args))

	for _, arg := range args {
		fmt.Fprintf(&out, "$%d\r\n%s\r\n", len(arg), arg)
	}

	return out.String()
}

// loopback is a store on a loopback port that refuses the HELLO of go-redis, reads exactly the request that the test
// expects, hands the bytes it
// got to the test, and then writes the reply (nothing when it is empty) and closes the connection when closeAfter is
// set. It never answers a command by itself.
func loopback(t *testing.T, request, reply string, closeAfter bool) (string, <-chan string) {
	t.Helper()

	lis, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = lis.Close() })

	received := make(chan string, 4)

	go func() {
		for {
			conn, acceptErr := lis.Accept()
			if acceptErr != nil {
				return
			}

			go func() {
				defer conn.Close()

				// go-redis opens with HELLO: refuse it as an old store does, and it goes on without
				hello := make([]byte, len(resp("hello", "2")))

				_, helloErr := io.ReadFull(conn, hello)
				if helloErr != nil {
					return
				}

				_, _ = io.WriteString(conn, "-ERR unknown command 'hello'\r\n")

				buf := make([]byte, len(request))

				n, readErr := io.ReadFull(conn, buf)
				received <- string(buf[:n])

				if readErr != nil {
					return
				}

				_, _ = io.WriteString(conn, reply)

				if !closeAfter {
					_, _ = io.Copy(io.Discard, conn) // ends when the client closes the connection
				}
			}()
		}
	}()

	return lis.Addr().String(), received
}

// dialLoopback is a provider that owns a go-redis client for the loopback store: no identity commands, no retry
// and a socket read timeout of an hour, so only the deadline of the call can end a call that waits.
func dialLoopback(t *testing.T, addr string, timeout time.Duration) *valkeysegment.Provider {
	t.Helper()

	prov, err := valkeysegment.Dial(&redis.UniversalOptions{
		Addrs:           []string{addr},
		Protocol:        2,
		DisableIdentity: true,
		ReadTimeout:     time.Hour,
		MaxRetries:      -1,
	}, &valkeysegment.Options{
		Timeout: timeout,
		Segments: map[string]valkeysegment.SegmentOptions{
			segBeta: {},
			"f":     {Layout: valkeysegment.LayoutKeys, Key: flagPrefix},
		},
	})
	require.NoError(t, err)

	t.Cleanup(func() { _ = prov.Close() })

	return prov
}

// A client that does not honour the deadline of the context keeps the call on the socket until its own read
// timeout. Dial sets ContextTimeoutEnabled, so the call ends at the timeout of the provider: the read timeout of this
// client is an hour, and the test waits for the call under a bound, never for a measured time. The store refuses the
// HELLO, reads the membership commands right after it (nothing else came between), and never answers.
func TestDialEndsACallAtItsDeadlineOnTheSocket(t *testing.T) {
	t.Parallel()

	request := resp("smismember", keyBeta, idAlice) + resp("exists", keyBeta)
	addr, received := loopback(t, request, "", false)
	prov := dialLoopback(t, addr, 50*time.Millisecond)
	lookups := []segment.Lookup{lk(segBeta, idAlice)}
	done := make(chan error, 1)

	go func() { done <- prov.Contains(context.Background(), lookups) }()

	select {
	case got := <-received:
		assert.Equal(t, request, got)
	case <-boundedCtx(t).Done():
		require.FailNow(t, "the store did not receive the membership commands")
	}

	select {
	case err := <-done:
		require.ErrorIs(t, err, valkeysegment.ErrTimeout)
		assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))
	case <-boundedCtx(t).Done():
		require.FailNow(t, "the call did not end at its deadline")
	}
}

// TestRepliesOnTheWire runs the provider on a real go-redis client against stores that answer with the bytes of
// the test, and checks what a reply cut short or malformed is worth.
func TestRepliesOnTheWire(t *testing.T) {
	t.Parallel()

	bothKeys := resp("exists", flagPrefix+idAlice) + resp("exists", flagPrefix+"bob")
	setPipe := resp("smismember", keyBeta, idAlice, "zed") + resp("exists", keyBeta)

	tests := []struct {
		name       string
		request    string
		reply      string
		lookups    []segment.Lookup
		wantStates []segment.State
	}{
		{
			name:       "a malformed integer is not a negative answer",
			request:    resp("exists", flagPrefix+idAlice),
			reply:      ":invalid\r\n",
			lookups:    []segment.Lookup{lk("f", idAlice)},
			wantStates: []segment.State{segment.Unknown},
		},
		{
			name:       "a malformed integer after a valid reply: nothing of the batch is trusted",
			request:    bothKeys,
			reply:      ":1\r\n:invalid\r\n",
			lookups:    []segment.Lookup{lk("f", idAlice), lk("f", "bob")},
			wantStates: []segment.State{segment.Unknown, segment.Unknown},
		},
		{
			name:       "a valid reply and then the connection closes: the answer is kept",
			request:    bothKeys,
			reply:      ":1\r\n",
			lookups:    []segment.Lookup{lk("f", idAlice), lk("f", "bob")},
			wantStates: []segment.State{segment.Member, segment.Unknown},
		},
		{
			name:       "a truncated list is a transport failure",
			request:    setPipe,
			reply:      "*2\r\n:1\r\n",
			lookups:    []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")},
			wantStates: []segment.State{segment.Unknown, segment.Unknown},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			addr, received := loopback(t, tt.request, tt.reply, true)
			prov := dialLoopback(t, addr, boundTimeout)

			err := prov.Contains(boundedCtx(t), tt.lookups)

			require.ErrorIs(t, err, valkeysegment.ErrUnavailable)
			require.NotErrorIs(t, err, valkeysegment.ErrProtocol)
			assert.Equal(t, tt.wantStates, states(tt.lookups))

			select {
			case got := <-received:
				assert.Equal(t, tt.request, got)
			case <-boundedCtx(t).Done():
				require.FailNow(t, "the store did not receive the request")
			}
		})
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
			name: "a client that leaves the completed replies unstamped: a transport error keeps them",
			store: &fakeStore{
				keys: keys, exec: func(context.Context) error { return errDial }, completed: 1, unstamped: true,
			},
			lookups:     []segment.Lookup{lk("f", idAlice), lk("f", "bob")},
			wantStates:  []segment.State{segment.Member, segment.Unknown},
			wantClass:   valkeysegment.ClassUnavailable,
			wantSentry:  valkeysegment.ErrUnavailable,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: msgUnavailable1of2,
		},
		{
			name: "a client that leaves the completed replies unstamped: a parse error trusts nothing",
			store: &fakeStore{
				keys: keys, completed: 1, unstamped: true,
				exec: func(context.Context) error {
					return &strconv.NumError{Func: "ParseInt", Num: "invalid", Err: strconv.ErrSyntax}
				},
			},
			lookups:     []segment.Lookup{lk("f", idAlice), lk("f", "bob")},
			wantStates:  []segment.State{segment.Unknown, segment.Unknown},
			wantClass:   valkeysegment.ClassUnavailable,
			wantSentry:  valkeysegment.ErrUnavailable,
			wantFailed:  2,
			wantPipes:   1,
			wantMessage: msgUnavailable2of2,
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
			wantMessage: msgUnavailable2of2,
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

func TestDialRefusesDisabledSocketDeadlines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts redis.UniversalOptions
	}{
		{name: "read", opts: redis.UniversalOptions{Addrs: []string{"127.0.0.1:1"}, ReadTimeout: -2}},
		{name: "write", opts: redis.UniversalOptions{Addrs: []string{"127.0.0.1:1"}, WriteTimeout: -2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prov, err := valkeysegment.Dial(&tt.opts, &valkeysegment.Options{})

			require.ErrorIs(t, err, valkeysegment.ErrSocketDeadlineDisabled)
			require.ErrorIs(t, err, valkeysegment.ErrInvalidOptions)
			assert.Nil(t, prov)
		})
	}
}
