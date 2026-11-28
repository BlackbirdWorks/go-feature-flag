//go:build docker

package corehttp_test

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

// A definition with no mode key is a snapshot segment.
const snapshotSegDoc = segHeaderYAML + "  - name: beta\n    members: [alice]\n" +
	"  - name: audience\n    connection: audiences\n    key: aud\n" +
	"    version: {key: aud-version, interval: 100ms}\n"

func (s *valkeyStore) del(tb testing.TB, key string) {
	tb.Helper()

	require.NoError(tb, s.client.Do(tb.Context(), s.client.B().Del().Key(key).Build()).Error())
}

func isNotMemberHealthy(b evalBody) bool {
	return !b.Value && b.Metadata[flagruntime.MetadataSegmentStatus] == nil
}

func isStale(b evalBody) bool {
	return b.Value && b.Metadata[flagruntime.MetadataSegmentStatus] == "STALE"
}

func snapshotRig(tb testing.TB, address string) *v2 {
	tb.Helper()

	rig := newV2With(tb, &v2Options{
		connections: "  audiences: {type: valkey, addresses: [\"" + address + "\"], forceSingleClient: true, " +
			"lookupTimeout: 300ms, timeouts: {connect: 500ms}}\n",
		extra: resolverSection, cold: true,
	})
	rig.flags[dynFlagsObject] = []byte(dynFlagsYAML)
	rig.segments = map[string][]byte{segObjectName: []byte(snapshotSegDoc)}
	rig.sets = valkeyRigSets()

	rig.publish(tb)
	rig.poll(tb)

	return rig
}

// A snapshot segment (no mode written) is served from a real Valkey: a change of the set and its version is targeted
// within a refresh interval with no store call on the request path; a store that goes away keeps answering from the
// held set with STALE while the relay stays ready; the store back is healthy again; a deleted set is not a member.
func TestE2E_ValkeySnapshotSegmentLifecycle(t *testing.T) {
	t.Parallel()

	store := startValkey(t)
	proxy := newOutage(t, store.address)
	rig := snapshotRig(t, proxy.address())

	awaitEval(t, rig, "alice", isNotMemberHealthy, "a set that is not in the store has no member")

	store.add(t, "aud", "alice")
	store.setVersion(t, "1")

	awaitEval(t, rig, "alice", isMember, "a member of the stored set is targeted after a refresh")
	assertNotCacheable(t, rig)

	calls := rig.core.Status().Segments.Resolver.Calls

	for range 20 {
		got := evalOFREP(t, rig, "dyn-flag", "alice")
		assert.True(t, isMember(got))
	}

	assert.Equal(t, calls, rig.core.Status().Segments.Resolver.Calls, "no store call on the request path")

	snapshots := infoOf(t, rig).SegmentBindings
	require.NotNil(t, snapshots)
	require.Len(t, snapshots.Snapshots, 1)
	assert.Equal(t, 1, snapshots.Snapshots[0].Loaded)
	assert.Equal(t, 1, snapshots.Snapshots[0].Members)

	proxy.setDown(true)

	awaitEval(t, rig, "alice", isStale, "the held set keeps answering with STALE when the store is gone")
	rig.wantProbe(t, "/readyz", http.StatusOK, bodyServing)

	proxy.setDown(false)

	awaitEval(t, rig, "alice", isMember, "the store back is healthy again")

	store.del(t, "aud")
	store.setVersion(t, "2")

	awaitEval(t, rig, "alice", isNotMemberHealthy, "a deleted set is not a member")
}

// A reload that changes only the flags keeps the binding and its held set: under concurrent load no request fails and
// the member stays a member throughout.
func TestE2E_ValkeySnapshotReloadUnderLoad(t *testing.T) {
	t.Parallel()

	store := startValkey(t)
	rig := snapshotRig(t, store.address)

	store.add(t, "aud", "alice")
	store.setVersion(t, "1")

	awaitEval(t, rig, "alice", isMember, "the segment serves before the load starts")

	var (
		requests, failures atomic.Int64
		stop               = make(chan struct{})
		wg                 sync.WaitGroup
		stopOnce           sync.Once
	)

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
				_, otherErr := tryEval(rig, "static-flag", "alice")

				requests.Add(1)

				if err != nil || otherErr != nil || !isMember(got) {
					failures.Add(1)
				}
			}
		})
	}

	for i := range 5 {
		rig.flags[dynFlagsObject] = []byte(dynFlagsYAML + "extra-" + string(rune('a'+i)) +
			":\n  variations: {on: true, off: false}\n  defaultRule: {name: default, variation: on}\n")
		rig.publish(t)
		rig.poll(t)
	}

	stopWorkers()

	assert.Positive(t, requests.Load(), "the load ran")
	assert.Zero(t, failures.Load(), "no request failed or lost its member across the swaps")
	assert.Equal(t, 1, infoOf(t, rig).SegmentBindings.Live, "the reloads kept the one binding")
}
