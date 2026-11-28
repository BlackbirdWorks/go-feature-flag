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
	source *segment.MemorySource
	closed atomic.Bool
	binds  atomic.Int32
}

func (h *fakeHandle) Bind(segment.MissingKey, []flagruntime.BoundDefinition) (*flagruntime.Binding, error) {
	h.binds.Add(1)

	return &flagruntime.Binding{Provider: h.source, Versioner: h.source}, nil
}

func (h *fakeHandle) Close() error {
	h.closed.Store(true)

	return nil
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
	assert.Equal(t, "idle", stateOf(conns, "fast"))
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
		{name: "valkey unreachable", conn: "fast", wantState: stateError},
		{name: "unreadable CA bundle", conn: "badca", wantState: stateError},
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
			}

			cancel()
			<-done
			require.NoError(t, conns.Shutdown(t.Context()))
		})
	}
}
