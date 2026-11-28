package segmentstore_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/segmentstore"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

const (
	connFast       = "fast"
	kindRedis      = "redis"
	segAudience    = "audience"
	connCache      = "cache"
	kindFile       = "file"
	memberAlice    = "alice"
	stateError     = "error"
	stateConnected = "connected"
	fileDoc        = `version: 2
connections:
  control: {type: file, path: /tmp/control}
  cache: {type: redis, addresses: ["127.0.0.1:1"], password: {fromEnv: SEGPASS}, tls: {enable: true}}
  fast: {type: valkey, addresses: ["127.0.0.1:1"], forceSingleClient: true, timeouts: {connect: 200ms}}
  tuned: {type: redis, addresses: ["127.0.0.1:1"], db: 2, maxRetries: -1, poolSize: 4, lookupTimeout: 50ms,
    keyPrefix: "p:", atomic: true, timeouts: {connect: 100ms, read: 100ms, write: 100ms}}
  nopass: {type: redis, addresses: ["127.0.0.1:1"], password: {fromEnv: MISSING}}
  goodca:
    type: valkey
    addresses: ["127.0.0.1:1"]
    forceSingleClient: true
    tls: {enable: true, serverName: x, caBundleFile: /ca.pem}
    timeouts: {connect: 200ms}
  notpem: {type: redis, addresses: ["127.0.0.1:1"], tls: {enable: true, caBundleFile: /notpem}}
  badca: {type: redis, addresses: ["127.0.0.1:1"], tls: {enable: true, caBundleFile: /no/such/ca.pem}}
`
	waitFor = 10 * time.Second
	tick    = 5 * time.Millisecond
)

var errDial = errors.New("dial refused")

// fakeHandle is a connected store over an in-memory source.
type fakeHandle struct {
	source      *segment.MemorySource
	bindErr     error
	closeErr    error
	closed      atomic.Bool
	binds       atomic.Int32
	noVersioner bool
}

func (h *fakeHandle) Bind(segment.MissingKey, []flagruntime.BoundDefinition) (*flagruntime.Binding, error) {
	h.binds.Add(1)

	if h.bindErr != nil {
		return nil, h.bindErr
	}

	if h.noVersioner {
		return &flagruntime.Binding{Provider: h.source}, nil
	}

	return &flagruntime.Binding{Provider: h.source, Versioner: h.source}, nil
}

func (h *fakeHandle) Close() error {
	h.closed.Store(true)

	return h.closeErr
}

// secretEnv has the password of the connections, and nothing else.
func secretEnv(name string) (string, bool) {
	if name == "SEGPASS" {
		return "secret", true
	}

	return "", false
}

// readFile serves the CA bundles of the test: a real certificate, and a file that is not PEM.
func readFile(t *testing.T) func(string) ([]byte, error) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"}, IsCA: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	return func(name string) ([]byte, error) {
		switch name {
		case "/ca.pem":
			return pemBytes, nil
		case "/notpem":
			return []byte("not a certificate"), nil
		}

		return nil, errDial
	}
}

func parseDoc(t *testing.T) *bootstrap.Config {
	t.Helper()

	cfg, err := bootstrap.Parse([]byte(fileDoc), nil, bootstrap.LoadOptions{LookupEnv: secretEnv})
	require.NoError(t, err)

	return cfg
}

func stateOf(conns *segmentstore.Connections, name string) string {
	for _, state := range conns.States() {
		if state.Name == name {
			return state.State
		}
	}

	return ""
}

// Bind marks a connection as needed and refuses a name that cannot serve segments.
func TestConnectionsBind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		conn      string
		wantErr   error
		wantState string
	}{
		{name: kindRedis, conn: connCache, wantState: "connecting"},
		{name: "unknown", conn: "nowhere", wantErr: segmentstore.ErrUnknownConnection, wantState: "idle"},
		{name: kindFile, conn: "control", wantErr: segmentstore.ErrCannotServe, wantState: "idle"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conns := segmentstore.NewConnections(parseDoc(t), segmentstore.Options{})
			binding, err := conns.Bind(tt.conn, "", nil)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, binding)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, binding.Provider)
			}

			assert.Equal(t, tt.wantState, stateOf(conns, connCache), "only a bound connection is needed")
		})
	}
}

// A connection that is needed is dialled by the loop, never by Bind: the lookups are Unknown with ErrNotConnected
// until it is up, a failing dial is retried and logged once, the lookups work when it succeeds, and Shutdown closes
// the client and stops the loop with the context.
func TestConnectionsRun(t *testing.T) {
	t.Parallel()

	var (
		allow  atomic.Bool
		handle = &fakeHandle{source: segment.NewMemorySource(map[string][]string{segAudience: {memberAlice}})}
	)

	cfg := parseDoc(t)
	conns := segmentstore.NewConnections(cfg, segmentstore.Options{
		RetryInterval: tick,
		Dialers: map[string]segmentstore.Dialer{
			connCache: func(context.Context) (segmentstore.Handle, error) {
				if !allow.Load() {
					return nil, errDial
				}

				return handle, nil
			},
		},
	})

	binding, err := conns.Bind(connCache, segment.MissingNotMember, []flagruntime.BoundDefinition{{Name: segAudience}})
	require.NoError(t, err)

	lookups := []segment.Lookup{{Segment: segAudience, ID: memberAlice}}
	require.ErrorIs(t, binding.Provider.Contains(t.Context(), lookups), segmentstore.ErrNotConnected)
	assert.Equal(t, segment.Unknown, lookups[0].State)

	_, err = binding.Versioner.Version(t.Context(), segAudience)
	require.ErrorIs(t, err, segmentstore.ErrNotConnected)

	reporter, ok := binding.Provider.(segment.HealthReporter)
	require.True(t, ok)
	assert.Equal(t, "unavailable", reporter.Health().Failure)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		conns.Run(ctx)
	}()

	require.Eventually(t, func() bool { return stateOf(conns, connCache) == stateError }, waitFor, tick,
		"a failing dial is an error state, retried")

	allow.Store(true)

	require.Eventually(t, func() bool { return stateOf(conns, connCache) == stateConnected }, waitFor, tick,
		"the dial is retried until it works")

	require.NoError(t, binding.Provider.Contains(t.Context(), lookups))
	assert.Equal(t, segment.Member, lookups[0].State)
	assert.Equal(t, int32(1), handle.binds.Load(), "the client is bound once for the generation")

	version, err := binding.Versioner.Version(t.Context(), segAudience)
	require.NoError(t, err)
	assert.NotEmpty(t, version)

	cancel()

	select {
	case <-done:
	case <-time.After(waitFor):
		assert.Fail(t, "the loop did not stop when its context ended")
	}

	require.NoError(t, conns.Shutdown(t.Context()))
	assert.True(t, handle.closed.Load(), "the client is closed by Shutdown")
	assert.Equal(t, "closed", stateOf(conns, connCache), "the client is gone after Shutdown")
	assert.Equal(t, "closed", stateOf(conns, connFast))
}

// The real dialers of the kinds: go-redis connects on the first command, so a redis connection is up at once and its
// lookups fail until the store is there; valkey-go connects when the client is created, so an unreachable valkey is
// an error state, retried; a CA bundle that cannot be read is an error state too.
func TestConnectionsDialKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		conn      string
		wantState string
	}{
		{name: kindRedis, conn: connCache, wantState: stateConnected},
		{name: "valkey unreachable", conn: connFast, wantState: stateError},
		{name: "unreadable CA bundle", conn: "badca", wantState: stateError},
		{name: "redis with settings", conn: "tuned", wantState: stateConnected},
		{name: "unset password", conn: "nopass", wantState: stateError},
		{name: "CA bundle that is not PEM", conn: "notpem", wantState: stateError},
		{name: "valkey with a CA bundle", conn: "goodca", wantState: stateError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conns := segmentstore.NewConnections(parseDoc(t), segmentstore.Options{
				RetryInterval: time.Hour,
				Secrets:       bootstrap.LoadOptions{LookupEnv: secretEnv, ReadFile: readFile(t)},
			})

			binding, err := conns.Bind(
				tt.conn,
				segment.MissingNotMember,
				[]flagruntime.BoundDefinition{{Name: segAudience}},
			)
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})

			go func() {
				defer close(done)

				conns.Run(ctx)
			}()

			require.Eventually(t, func() bool { return stateOf(conns, tt.conn) == tt.wantState }, waitFor, tick)

			if tt.wantState == stateConnected {
				lookups := []segment.Lookup{{Segment: segAudience, ID: memberAlice}}
				require.Error(t, binding.Provider.Contains(t.Context(), lookups), "nothing listens on the port")
				assert.Equal(t, segment.Unknown, lookups[0].State)

				_, err = binding.Versioner.Version(t.Context(), segAudience)
				require.Error(t, err, "no version key is configured, and nothing listens")

				reporter, ok := binding.Provider.(segment.HealthReporter)
				require.True(t, ok)
				assert.NotEmpty(t, reporter.Health().Failure, "the failed call is the health of the provider")
			}

			cancel()
			<-done
			require.NoError(t, conns.Shutdown(t.Context()))
		})
	}
}

// A dial that ignores its context does not hold the loop: Run returns when its context ends, and the client that
// arrives later is closed, never left open. The same when Shutdown comes while the dial runs.
func TestConnectionsDialThatBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		stop      func(cancel context.CancelFunc, conns *segmentstore.Connections)
		name      string
		wantState string
	}{
		{
			name: "context ends", wantState: "connecting",
			stop: func(cancel context.CancelFunc, _ *segmentstore.Connections) { cancel() },
		},
		{
			name:      "shutdown",
			wantState: "closed",
			stop:      func(_ context.CancelFunc, conns *segmentstore.Connections) { _ = conns.Shutdown(context.Background()) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				handle  = &fakeHandle{source: segment.NewMemorySource(nil)}
				entered = make(chan struct{})
				release = make(chan struct{})
			)

			conns := segmentstore.NewConnections(parseDoc(t), segmentstore.Options{
				RetryInterval: tick,
				Dialers: map[string]segmentstore.Dialer{
					connCache: func(context.Context) (segmentstore.Handle, error) {
						close(entered)
						<-release // ignores the context, as a client that dials while it is created

						return handle, nil
					},
				},
			})

			_, err := conns.Bind(connCache, segment.MissingNotMember, nil)
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			done := make(chan struct{})

			go func() {
				defer close(done)

				conns.Run(ctx)
			}()

			select {
			case <-entered:
			case <-time.After(waitFor):
				require.FailNow(t, "the dial did not start")
			}

			tt.stop(cancel, conns)
			cancel()

			select {
			case <-done:
			case <-time.After(waitFor):
				require.FailNow(t, "Run did not return while the dial was blocked")
			}

			close(release)

			require.Eventually(t, handle.closed.Load, waitFor, tick, "the client that arrived late is closed")
			assert.Equal(t, tt.wantState, stateOf(conns, connCache))
		})
	}
}

// What a connected store can do wrong: no version source, a provider that reports no health, a bind that fails, a
// client that does not close.
func TestConnectionsBindingEdges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		handle          *fakeHandle
		check           func(t *testing.T, binding *flagruntime.Binding)
		wantShutdownErr error
		name            string
	}{
		{
			name: "no version source", handle: &fakeHandle{noVersioner: true},
			check: func(t *testing.T, binding *flagruntime.Binding) {
				t.Helper()

				_, err := binding.Versioner.Version(t.Context(), segAudience)
				require.ErrorIs(t, err, segment.ErrNoVersion)
			},
		},
		{
			name: "provider without health", handle: &fakeHandle{},
			check: func(t *testing.T, binding *flagruntime.Binding) {
				t.Helper()

				lookups := []segment.Lookup{{Segment: segAudience, ID: memberAlice}}
				require.NoError(t, binding.Provider.Contains(t.Context(), lookups))

				reporter, ok := binding.Provider.(segment.HealthReporter)
				require.True(t, ok)
				assert.Equal(t, segment.Health{Ready: true}, reporter.Health())
			},
		},
		{
			name: "no traffic yet", handle: &fakeHandle{},
			check: func(t *testing.T, binding *flagruntime.Binding) {
				t.Helper()

				reporter, ok := binding.Provider.(segment.HealthReporter)
				require.True(t, ok)
				assert.Equal(t, segment.Health{Ready: true}, reporter.Health(), "a connected store is not failing")
			},
		},
		{
			name: "bind fails", handle: &fakeHandle{bindErr: errDial},
			check: func(t *testing.T, binding *flagruntime.Binding) {
				t.Helper()

				lookups := []segment.Lookup{{Segment: segAudience, ID: memberAlice}}
				require.ErrorIs(t, binding.Provider.Contains(t.Context(), lookups), errDial)
				require.ErrorIs(t, binding.Provider.Contains(t.Context(), lookups), errDial, "the bind is tried again")
			},
		},
		{
			name: "close fails", handle: &fakeHandle{closeErr: errDial}, wantShutdownErr: errDial,
			check: func(*testing.T, *flagruntime.Binding) {},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.handle.source = segment.NewMemorySource(map[string][]string{segAudience: {memberAlice}})
			conns := segmentstore.NewConnections(parseDoc(t), segmentstore.Options{
				RetryInterval: tick,
				Dialers: map[string]segmentstore.Dialer{
					connCache: func(context.Context) (segmentstore.Handle, error) { return tt.handle, nil },
				},
			})

			binding, err := conns.Bind(connCache, segment.MissingNotMember, nil)
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			done := make(chan struct{})

			go func() {
				defer close(done)

				conns.Run(ctx)
			}()

			require.Eventually(t, func() bool { return stateOf(conns, connCache) == stateConnected }, waitFor, tick)
			tt.check(t, binding)
			cancel()

			select {
			case <-done:
			case <-time.After(waitFor):
				require.FailNow(t, "the loop did not stop")
			}

			err = conns.Shutdown(t.Context())
			if tt.wantShutdownErr != nil {
				require.ErrorIs(t, err, tt.wantShutdownErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// The connections are dialled apart: a store that does not answer does not delay the others.
func TestConnectionsDialInParallel(t *testing.T) {
	t.Parallel()

	var (
		release = make(chan struct{})
		entered = make(chan struct{})
		fast    = &fakeHandle{source: segment.NewMemorySource(nil)}
		slow    = &fakeHandle{source: segment.NewMemorySource(nil)}
	)

	conns := segmentstore.NewConnections(parseDoc(t), segmentstore.Options{
		RetryInterval: tick,
		Dialers: map[string]segmentstore.Dialer{
			connCache: func(ctx context.Context) (segmentstore.Handle, error) {
				close(entered)

				select {
				case <-release:
					return slow, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
			connFast: func(context.Context) (segmentstore.Handle, error) { return fast, nil },
		},
	})

	for _, name := range []string{connCache, connFast} {
		_, err := conns.Bind(name, segment.MissingNotMember, nil)
		require.NoError(t, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		conns.Run(ctx)
	}()

	select {
	case <-entered:
	case <-time.After(waitFor):
		require.FailNow(t, "the slow dial did not start")
	}

	require.Eventually(t, func() bool { return stateOf(conns, connFast) == stateConnected }, waitFor, tick,
		"the healthy connection connects while the other dial is blocked")
	assert.Equal(t, "connecting", stateOf(conns, connCache))

	close(release)

	require.Eventually(t, func() bool { return stateOf(conns, connCache) == stateConnected }, waitFor, tick)
	cancel()

	select {
	case <-done:
	case <-time.After(waitFor):
		require.FailNow(t, "the loop did not stop")
	}
}
