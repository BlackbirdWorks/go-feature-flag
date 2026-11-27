package redissegment_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
)

func TestNewRequiresContextTimeoutOfAGoRedisClient(t *testing.T) {
	t.Parallel()

	const addr = "127.0.0.1:1" // never dialed: go-redis connects on the first command

	tests := []struct {
		client  func() redissegment.Client
		name    string
		wantErr bool
	}{
		{
			name:    "a node client without it",
			client:  func() redissegment.Client { return redis.NewClient(&redis.Options{Addr: addr}) },
			wantErr: true,
		},
		{
			name: "a node client with it",
			client: func() redissegment.Client {
				return redis.NewClient(&redis.Options{Addr: addr, ContextTimeoutEnabled: true})
			},
		},
		{
			name: "a cluster client without it",
			client: func() redissegment.Client {
				return redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{addr}})
			},
			wantErr: true,
		},
		{
			name: "a cluster client with it",
			client: func() redissegment.Client {
				return redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{addr}, ContextTimeoutEnabled: true})
			},
		},
		{
			name:   "a client that does not expose its options",
			client: func() redissegment.Client { return &fakeStore{} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prov, err := redissegment.New(tt.client(), &redissegment.Options{})

			if tt.wantErr {
				require.ErrorIs(t, err, redissegment.ErrContextTimeoutDisabled)
				require.ErrorIs(t, err, redissegment.ErrInvalidOptions)
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
func loopback(t *testing.T, request, reply string, closeAfter, warm bool) (string, <-chan string) {
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

				if warm { // a first command that the store answers, so the connection is set up and pooled
					ping := make([]byte, len(resp("ping")))

					_, pingErr := io.ReadFull(conn, ping)
					if pingErr != nil {
						return
					}

					_, _ = io.WriteString(conn, "+PONG\r\n")
				}

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
func dialLoopback(t *testing.T, addr string, timeout time.Duration) *redissegment.Provider {
	t.Helper()

	prov, err := redissegment.Dial(&redis.UniversalOptions{
		Addrs:           []string{addr},
		Protocol:        2,
		DisableIdentity: true,
		ReadTimeout:     time.Hour,
		MaxRetries:      -1,
	}, &redissegment.Options{
		Timeout: timeout,
		Segments: map[string]redissegment.SegmentOptions{
			segBeta: {},
			"f":     {Layout: redissegment.LayoutKeys, Key: flagPrefix},
		},
	})
	require.NoError(t, err)

	t.Cleanup(func() { _ = prov.Close() })

	return prov
}

// A client that does not honour the deadline of the context keeps the call on the socket until its own read
// timeout. With ContextTimeoutEnabled the call ends at the timeout of the provider: the read timeout of this client
// is an hour, so only the deadline can end it. The connection is set up first by a command that the store answers
// (the dial and the handshake are done and the connection is pooled: pipelines have a pool of their own, of one),
// so the short deadline of the call covers the pipeline only. The store reads exactly the membership commands, and
// never answers them. The test waits under a failure bound, never for a measured time.
func TestAClientEndsACallAtItsDeadlineOnTheSocket(t *testing.T) {
	t.Parallel()

	request := resp("smismember", keyBeta, idAlice) + resp("exists", keyBeta)
	addr, received := loopback(t, request, "", false, true)
	client := redis.NewClient(&redis.Options{
		Addr:                  addr,
		Protocol:              2,
		DisableIdentity:       true,
		ContextTimeoutEnabled: true,
		ReadTimeout:           time.Hour,
		MaxRetries:            -1,
		PoolSize:              1,
		PipelinePoolSize:      1, // pipelines have a pool of their own: one connection, so the warm-up one is reused
	})

	t.Cleanup(func() { _ = client.Close() })

	warm := client.Pipeline()
	pong := warm.Do(boundedCtx(t), "ping")

	_, warmErr := warm.Exec(boundedCtx(t))

	require.NoError(t, warmErr, "the warm-up sets the connection up")
	require.Equal(t, "PONG", pong.Val())

	prov, err := redissegment.New(client, &redissegment.Options{
		Timeout:  50 * time.Millisecond,
		Segments: map[string]redissegment.SegmentOptions{segBeta: {}},
	})
	require.NoError(t, err)

	lookups := []segment.Lookup{lk(segBeta, idAlice)}
	done := make(chan error, 1)

	go func() { done <- prov.Contains(context.Background(), lookups) }()

	select {
	case err = <-done:
		require.ErrorIs(t, err, redissegment.ErrTimeout)
		assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))
	case <-boundedCtx(t).Done():
		require.FailNow(t, "the call did not end at its deadline")
	}

	select {
	case got := <-received:
		assert.Equal(t, request, got)
	case <-boundedCtx(t).Done():
		require.FailNow(t, "the store did not receive the membership commands")
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

			addr, received := loopback(t, tt.request, tt.reply, true, false)
			prov := dialLoopback(t, addr, boundTimeout)

			err := prov.Contains(boundedCtx(t), tt.lookups)

			require.ErrorIs(t, err, redissegment.ErrUnavailable)
			require.NotErrorIs(t, err, redissegment.ErrProtocol)
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
			wantClass:   redissegment.ClassUnavailable,
			wantSentry:  redissegment.ErrUnavailable,
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
			wantClass:   redissegment.ClassUnavailable,
			wantSentry:  redissegment.ErrUnavailable,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: msgUnavailable1of2,
		},
		{
			name: "a client that leaves the completed replies unstamped: a parse error trusts nothing",
			store: &fakeStore{
				keys: keys, completed: 1, unstamped: true,
				exec: func(context.Context) error {
					return errParse()
				},
			},
			lookups:     []segment.Lookup{lk("f", idAlice), lk("f", "bob")},
			wantStates:  []segment.State{segment.Unknown, segment.Unknown},
			wantClass:   redissegment.ClassUnavailable,
			wantSentry:  redissegment.ErrUnavailable,
			wantFailed:  2,
			wantPipes:   1,
			wantMessage: msgUnavailable2of2,
		},
		{
			name: "a pool timeout on one node keeps the replies of the others",
			store: &fakeStore{
				keys:      keys,
				exec:      func(context.Context) error { return redis.ErrPoolTimeout },
				completed: 1,
				unstamped: true,
			},
			lookups:     []segment.Lookup{lk("f", idAlice), lk("f", "bob")},
			wantStates:  []segment.State{segment.Member, segment.Unknown},
			wantClass:   redissegment.ClassUnavailable,
			wantSentry:  redissegment.ErrUnavailable,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: msgUnavailable1of2,
		},
		{
			name: "a server error first and a pool that is exhausted later: the healthy replies stay",
			store: &fakeStore{
				keys:      keys,
				exec:      func(context.Context) error { return serverError{msg: msgWrongType} },
				completed: 3,
				unstamped: true,
				reply: func(args []any) *fakeReply {
					switch args[1] {
					case flagPrefix + "zed":
						return &fakeReply{err: serverError{msg: msgWrongType}}
					case flagPrefix + "bob":
						return &fakeReply{err: redis.ErrPoolExhausted}
					}

					return nil
				},
			},
			lookups:     []segment.Lookup{lk("f", "zed"), lk("f", idAlice), lk("f", "bob")},
			wantStates:  []segment.State{segment.Unknown, segment.Member, segment.Unknown},
			wantClass:   redissegment.ClassUnavailable,
			wantSentry:  redissegment.ErrUnavailable,
			wantFailed:  2,
			wantPipes:   1,
			wantMessage: "redis segment: unavailable: 2 of 3 lookups failed",
		},
		{
			name: "a closed client on one node keeps the replies of the others",
			store: &fakeStore{
				keys: keys, exec: func(context.Context) error { return redis.ErrClosed }, completed: 1, unstamped: true,
			},
			lookups:     []segment.Lookup{lk("f", idAlice), lk("f", "bob")},
			wantStates:  []segment.State{segment.Member, segment.Unknown},
			wantClass:   redissegment.ClassUnavailable,
			wantSentry:  redissegment.ErrUnavailable,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: msgUnavailable1of2,
		},
		{
			name: "a transport error first and a parse error on a later command: nothing is trusted",
			store: &fakeStore{
				keys: keys, exec: func(context.Context) error { return errDial }, completed: 2, unstamped: true,
				reply: func(args []any) *fakeReply {
					if args[1] == flagPrefix+"bob" {
						return &fakeReply{err: errParse()}
					}

					return nil
				},
			},
			lookups:     []segment.Lookup{lk("f", idAlice), lk("f", "bob")},
			wantStates:  []segment.State{segment.Unknown, segment.Unknown},
			wantClass:   redissegment.ClassUnavailable,
			wantSentry:  redissegment.ErrUnavailable,
			wantFailed:  2,
			wantPipes:   1,
			wantMessage: msgUnavailable2of2,
		},
		{
			name:        "keys layout: it closes before any reply",
			store:       &fakeStore{keys: keys, exec: func(context.Context) error { return errDial }},
			lookups:     []segment.Lookup{lk("f", idAlice), lk("f", "bob")},
			wantStates:  []segment.State{segment.Unknown, segment.Unknown},
			wantClass:   redissegment.ClassUnavailable,
			wantSentry:  redissegment.ErrUnavailable,
			wantFailed:  2,
			wantPipes:   1,
			wantMessage: msgUnavailable2of2,
		},
		{
			name:        "set layout: the members answered, the key check did not",
			store:       &fakeStore{sets: sets, exec: func(context.Context) error { return errDial }, completed: 1},
			lookups:     []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")},
			wantStates:  []segment.State{segment.Member, segment.Unknown},
			wantClass:   redissegment.ClassUnavailable,
			wantSentry:  redissegment.ErrUnavailable,
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

			prov, err := redissegment.Dial(&tt.opts, &redissegment.Options{})

			require.ErrorIs(t, err, redissegment.ErrSocketDeadlineDisabled)
			require.ErrorIs(t, err, redissegment.ErrInvalidOptions)
			assert.Nil(t, prov)
		})
	}
}

func TestNewSocketTimeoutsOfBothClientKinds(t *testing.T) {
	t.Parallel()

	const addr = "127.0.0.1:1" // never dialed

	tests := []struct {
		name         string
		read, write  time.Duration
		wantNodeErr  bool
		wantClustErr bool
	}{
		{name: "both default", read: 0, write: 0},
		{name: "both positive", read: time.Second, write: time.Second},
		// go-redis reads a -1 as "default" for both kinds, and keeps the -2 of a cluster client as it is
		{name: "read -1", read: -1},
		{name: "write -1", read: time.Second, write: -1},
		{name: "read -2", read: -2, wantNodeErr: true, wantClustErr: true},
		{name: "write -2", read: time.Second, write: -2, wantNodeErr: true, wantClustErr: true},
		{name: "write follows a read of -2", read: -2, write: 0, wantNodeErr: true, wantClustErr: true},
	}

	for _, tt := range tests {
		for _, kind := range []string{"node", "cluster"} {
			t.Run(tt.name+" "+kind, func(t *testing.T) {
				t.Parallel()

				var client redissegment.Client

				wantErr := tt.wantNodeErr

				if kind == "node" {
					node := redis.NewClient(&redis.Options{
						Addr: addr, ContextTimeoutEnabled: true, ReadTimeout: tt.read, WriteTimeout: tt.write,
					})

					t.Cleanup(func() { _ = node.Close() })

					client = node
				} else {
					clust := redis.NewClusterClient(&redis.ClusterOptions{
						Addrs: []string{
							addr,
						},
						ContextTimeoutEnabled: true,
						ReadTimeout:           tt.read,
						WriteTimeout:          tt.write,
					})

					t.Cleanup(func() { _ = clust.Close() })

					client, wantErr = clust, tt.wantClustErr
				}

				_, err := redissegment.New(client, &redissegment.Options{})

				if wantErr {
					require.ErrorIs(t, err, redissegment.ErrSocketDeadlineDisabled)
					require.ErrorIs(t, err, redissegment.ErrInvalidOptions)

					return
				}

				require.NoError(t, err)
			})
		}
	}
}
