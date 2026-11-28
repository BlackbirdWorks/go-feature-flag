//go:build docker

package corehttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	testcontainerredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

const (
	valkeyImage = "docker.io/valkey/valkey:8-alpine"
	eventually  = 8 * time.Second
	poll        = 25 * time.Millisecond

	valkeySegDoc = segHeaderYAML + "  - name: beta\n    members: [alice]\n" +
		"  - name: audience\n    connection: audiences\n    mode: lookup\n    key: aud\n" +
		"    version: {key: aud-version, interval: 100ms}\n"
	valkeySegDocMoved = segHeaderYAML + "  - name: beta\n    members: [alice]\n" +
		"  - name: audience\n    connection: audiences\n    mode: lookup\n    key: aud-moved\n" +
		"    version: {key: aud-version, interval: 100ms}\n"

	// The clock of this package is frozen: no breaker (its cooldown is read from the clock) and cached answers that
	// never expire, so only the version key can change an answer.
	resolverSection = "segments:\n  resolver:\n    callTimeout: 1s\n    default: {noBreaker: true}\n"
)

// outage is a TCP forwarder between the relay and the store, with a listener that is open from the start: setDown
// cuts the connections and refuses the new ones, which is a store that went away, and setDown(false) is the store
// back at the same address.
type outage struct {
	conns    map[net.Conn]struct{}
	listener net.Listener
	target   string
	wg       sync.WaitGroup
	mu       sync.Mutex
	down     bool
}

func newOutage(tb testing.TB, target string) *outage {
	tb.Helper()

	listener, err := (&net.ListenConfig{}).Listen(tb.Context(), "tcp", "127.0.0.1:0")
	require.NoError(tb, err)

	out := &outage{listener: listener, target: target, conns: map[net.Conn]struct{}{}}

	out.wg.Go(out.accept)
	tb.Cleanup(func() {
		_ = listener.Close()

		out.setDown(true)
		out.wg.Wait()
	})

	return out
}

func (o *outage) address() string { return o.listener.Addr().String() }

func (o *outage) track(conn net.Conn) bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.down {
		return false
	}

	o.conns[conn] = struct{}{}

	return true
}

func (o *outage) setDown(down bool) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.down = down

	if down {
		for conn := range o.conns {
			_ = conn.Close()
		}
	}
}

func (o *outage) accept() {
	for {
		client, err := o.listener.Accept()
		if err != nil {
			return
		}

		upstream, err := (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "tcp", o.target)
		if err != nil || !o.track(client) || !o.track(upstream) {
			_ = client.Close()

			if upstream != nil {
				_ = upstream.Close()
			}

			continue
		}

		o.wg.Go(func() {
			_, _ = io.Copy(upstream, client)
			_ = upstream.Close()
		})
		o.wg.Go(func() {
			_, _ = io.Copy(client, upstream)
			_ = client.Close()
		})
	}
}

func startValkey(tb testing.TB) *valkeyStore {
	tb.Helper()

	container, err := testcontainerredis.Run(tb.Context(), valkeyImage)
	testcontainers.CleanupContainer(tb, container)
	require.NoError(tb, err)

	address, err := container.Endpoint(tb.Context(), "")
	require.NoError(tb, err)

	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{address}})
	require.NoError(tb, err)
	tb.Cleanup(client.Close)

	return &valkeyStore{client: client, address: address}
}

// valkeyStore is a real store and a client of it that writes the members and the version keys.
type valkeyStore struct {
	client  valkey.Client
	address string
}

func (s *valkeyStore) add(tb testing.TB, key, member string) {
	tb.Helper()

	require.NoError(tb, s.client.Do(tb.Context(), s.client.B().Sadd().Key(key).Member(member).Build()).Error())
}

func (s *valkeyStore) setVersion(tb testing.TB, value string) {
	tb.Helper()

	require.NoError(tb, s.client.Do(tb.Context(), s.client.B().Set().Key("aud-version").Value(value).Build()).Error())
}

func valkeyRig(tb testing.TB, address string) *v2 {
	tb.Helper()

	rig := newV2With(tb, &v2Options{
		connections: "  audiences: {type: valkey, addresses: [\"" + address + "\"], forceSingleClient: true, " +
			"lookupTimeout: 300ms, timeouts: {connect: 500ms}}\n",
		extra: resolverSection, cold: true,
	})
	rig.flags[dynFlagsObject] = []byte(dynFlagsYAML)
	rig.segments = map[string][]byte{segObjectName: []byte(valkeySegDoc)}
	rig.sets = valkeyRigSets()

	rig.publish(tb)
	rig.poll(tb)

	return rig
}

func valkeyRigSets() []controlplane.FlagSet {
	return []controlplane.FlagSet{{
		Name: setName, Sources: []string{flagsObject, dynFlagsObject}, SegmentSources: []string{segObjectName},
		APIKeys: []string{tenantKey},
	}}
}

// tryEval is an evaluation that reports failures as errors, for the loops that wait.
func tryEval(rig *v2, flag, key string) (evalBody, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, rig.url+pathOFREPOne+flag,
		strings.NewReader(`{"context":{"targetingKey":"`+key+`"}}`))
	if err != nil {
		return evalBody{}, fmt.Errorf("request: %w", err)
	}

	req.Header.Set(xAPIKey, tenantKey)
	req.Header.Set(contentType, "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return evalBody{}, fmt.Errorf("evaluate: %w", err)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return evalBody{}, fmt.Errorf("read: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return evalBody{}, fmt.Errorf("status %d: %s: %w", resp.StatusCode, body, errNotOK)
	}

	var got evalBody

	err = json.Unmarshal(body, &got)

	return got, err
}

var errNotOK = errors.New("the status is not 200")

func awaitEval(t *testing.T, rig *v2, key string, want func(evalBody) bool, msg string) evalBody {
	t.Helper()

	var last atomic.Pointer[evalBody]

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		got, err := tryEval(rig, "dyn-flag", key)
		if !assert.NoError(collect, err, msg) {
			return
		}

		last.Store(&got)
		assert.True(
			collect,
			want(got),
			"%s: last answer %+v, resolver %+v",
			msg,
			got,
			rig.core.Status().Segments.Resolver,
		)
	}, eventually, poll, msg)

	return *last.Load()
}

func isMember(b evalBody) bool {
	return b.Value && b.Metadata[flagruntime.MetadataSegmentStatus] == nil
}

func isStoreError(b evalBody) bool {
	return !b.Value && b.Metadata[flagruntime.MetadataSegmentStatus] == "STORE_ERROR"
}

// A published dynamic segment is served from a real Valkey: members target through OFREP and the answer of a healthy
// store carries no status and no cacheable key; a change of the version key is seen before the cached answer would
// expire; a store that goes away answers not a member with STORE_ERROR, the relay stays ready and the other flags
// are unaffected; the store back recovers the segment.
func TestE2E_ValkeyDynamicSegmentLifecycle(t *testing.T) {
	t.Parallel()

	store := startValkey(t)
	proxy := newOutage(t, store.address)
	rig := valkeyRig(t, proxy.address())

	store.add(t, "aud", "alice")

	awaitEval(t, rig, "alice", isMember, "a member of the stored set is targeted")

	bob := awaitEval(t, rig, "bob", func(b evalBody) bool {
		return !b.Value && b.Metadata[flagruntime.MetadataSegmentStatus] == nil
	}, "a value that is not in the set is not a member, with a healthy status")
	assert.NotContains(t, bob.Metadata, flagruntime.MetadataCacheable)
	assertNotCacheable(t, rig)

	// bob was cached as not a member for 30s: only the version key can make him one before that
	store.add(t, "aud", "bob")
	store.setVersion(t, "2")

	awaitEval(t, rig, "bob", isMember, "a version bump invalidates the cached answer")

	// the store goes away: a value that was never asked has no cached answer
	proxy.setDown(true)

	awaitEval(t, rig, "carol", isStoreError, "a store that is gone answers STORE_ERROR")
	rig.wantProbe(t, "/readyz", http.StatusOK, bodyServing)

	static := evalOFREP(t, rig, "static-flag", "alice")
	assert.True(t, static.Value, "the flags that use no dynamic segment are unaffected")
	assert.NotContains(t, static.Metadata, flagruntime.MetadataSegmentStatus)

	cfg := infoOf(t, rig)
	require.Len(t, cfg.SegmentStores, 1)

	counts, _ := segmentsInfo(t, cfg)
	assert.Equal(t, 1, counts.DynamicStates.StoreError)

	// the store is back
	proxy.setDown(false)

	awaitEval(t, rig, "dave", func(b evalBody) bool {
		return !b.Value && b.Metadata[flagruntime.MetadataSegmentStatus] == nil
	}, "the segment recovers when the store is back")

	cfg = infoOf(t, rig)
	assert.Equal(t, "connected", cfg.SegmentStores[0].State)
}

// A reload that changes the definition swaps the generation atomically: under concurrent load no request fails and
// the member of both generations is a member throughout.
func TestE2E_ValkeyReloadUnderLoad(t *testing.T) {
	t.Parallel()

	store := startValkey(t)
	rig := valkeyRig(t, store.address)

	for _, key := range []string{"aud", "aud-moved"} {
		store.add(t, key, "alice")
	}

	awaitEval(t, rig, "alice", isMember, "the segment serves before the load starts")

	var (
		requests, failures atomic.Int64
		stop               = make(chan struct{})
		wg                 sync.WaitGroup
		stopOnce           sync.Once
	)

	// idempotent: it runs before the final assertions, and from the cleanup when a fatal assertion ends the test early
	stopWorkers := func() {
		stopOnce.Do(func() { close(stop) })
		wg.Wait()
	}

	t.Cleanup(stopWorkers)

	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}

				got, err := tryEval(rig, "dyn-flag", "alice")

				requests.Add(1)

				if err != nil || !isMember(got) {
					failures.Add(1)
				}
			}
		})
	}

	for i := range 5 {
		doc := valkeySegDoc
		if i%2 == 0 {
			doc = valkeySegDocMoved
		}

		rig.segments[segObjectName] = []byte(doc)
		rig.publish(t)
		rig.poll(t)
	}

	stopWorkers()

	assert.Positive(t, requests.Load(), "the load ran")
	assert.Zero(t, failures.Load(), "no request failed or lost its member across the swaps")
}
