//go:build docker

package segments_test

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/testproxy"
)

// The timeouts under test, kept short through the bootstrap settings (connSettings and chaosResolver).
const (
	lookupTimeout = 300 * time.Millisecond
	// margin is what a request may take beyond its deadline: the HTTP round trip of the harness, the scheduling of a
	// loaded machine running the race detector and the batch that is written before the deadline starts. The 10 runs
	// of each chaos test under -race on a machine with a load of 3 or more stayed far below it (see the pull request).
	margin = 700 * time.Millisecond
	// evalBound is the longest an evaluation may take with the store hung or slow: its lookup deadline plus the margin.
	// The call deadline of the resolver (callTimeout, 1 s) is the outer limit; the connection deadline is the inner one
	// and is the one that ends the call.
	evalBound = lookupTimeout + margin

	lookupFlag   = "lk-flag"
	snapFlag     = "sn-flag"
	serialMarker = "SEMAPHORE_CHAOS_SERIAL"

	chaosFlags = lookupFlag + `:
  variations: {on: true, off: false}
  targeting:
    - {name: aud, query: 'targetingKey in segment("aud")', variation: on}
  defaultRule: {name: default, variation: off}
` + snapFlag + `:
  variations: {on: true, off: false}
  targeting:
    - {name: snap, query: 'targetingKey in segment("snap")', variation: on}
  defaultRule: {name: default, variation: off}
`
	chaosSegments = "  - {name: aud, connection: audiences, mode: lookup, key: aud, " +
		"version: {key: aud-version, interval: 100ms}}\n" +
		"  - {name: snap, connection: audiences, mode: snapshot, key: snap, " +
		"version: {key: snap-version, interval: 100ms}}\n"
)

// chaosResolver: answers are cached for five minutes (so a cached answer outlives every test), the call deadline is
// 1 s, and the breaker opens after 3 failures and tries again after 500 ms; the pause and latency tests raise the
// failure count so that the breaker does not hide the timeouts they measure.
func chaosResolver(breakerFailures int) string {
	return fmt.Sprintf("segments:\n  resolver:\n    callTimeout: 1s\n    default: {ttl: 5m, negativeTtl: 5m, "+
		"breakerFailures: %d, breakerCooldown: 500ms}\n", breakerFailures)
}

type chaos struct {
	*rig

	store *store
	proxy *testproxy.Proxy
	fresh atomic.Int64
}

// startChaos (called by tests that do not run in parallel: the tests measure time against the timeouts, so none runs
// next to another test of the package) starts a store with alice in both sets, a proxy in front of it and a relay
// that reads through the proxy, and waits until both segments answer healthy.
func startChaos(t *testing.T, breakerFailures int) *chaos {
	t.Helper()

	backend := startStore(t)
	backend.add(t, "aud", "alice")
	backend.add(t, "snap", "alice", "bob")

	proxy, err := testproxy.Start(backend.address)
	require.NoError(t, err)
	t.Cleanup(proxy.Close)

	env := &chaos{store: backend, proxy: proxy}
	env.rig = startRig(t, &rigOptions{
		address: proxy.Addr(), resolver: chaosResolver(breakerFailures), flags: chaosFlags, segments: chaosSegments,
	})

	env.awaitAnswer(t, lookupFlag, "alice", rowHealthy+": lookup member", isMember)
	env.awaitAnswer(t, snapFlag, "alice", rowHealthy+": snapshot member", isMember)
	env.rig.assertReady(t)

	// The watcher of the version keys installs the first version it reads, and an answer cached before that is a miss
	// (another version). A membership change that is seen proves that the watcher has run: from here on the answers
	// that a test caches stay valid until their TTL.
	env.awaitAnswer(t, lookupFlag, "probe", "a value that is not in the set", notMember)
	backend.add(t, "aud", "probe")
	env.awaitAnswer(t, lookupFlag, "probe", "the version watcher saw the new version", isMember)

	return env
}

// key is a targeting key that no answer is cached for.
func (env *chaos) key() string { return fmt.Sprintf("key-%d", env.fresh.Add(1)) }

// timed evaluates one flag and returns how long the request took.
func (env *chaos) timed(flag, key string) (answer, time.Duration, error) {
	start := time.Now()
	got, err := env.eval(flag, key)

	return got, time.Since(start), err
}

// storm sends n evaluations of fresh keys from workers goroutines and returns the answers and the longest request.
func (env *chaos) storm(t *testing.T, workers, count int) ([]answer, time.Duration) {
	t.Helper()

	var (
		answers []answer
		slowest time.Duration
	)

	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)

	for range workers {
		wg.Go(func() {
			for range count {
				got, took, err := env.timed(lookupFlag, env.key())

				mu.Lock()

				answers = append(answers, got)
				slowest = max(slowest, took)

				mu.Unlock()

				assert.NoError(t, err)
			}
		})
	}

	wg.Wait()

	return answers, slowest
}

func (env *chaos) awaitBreaker(t *testing.T, state string, want func(float64) bool, msg string) {
	t.Helper()

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		// a closed breaker is the rest: it only changes state when a call reaches it
		_, _ = env.eval(lookupFlag, env.key())

		open, _ := env.metric(t, "semaphore_segment_breaker_state", map[string]string{"state": state})
		assert.True(collect, want(open), "%s: breaker gauge %v", msg, open)
	}, waitFor, tick, msg)
}

func (env *chaos) awaitLog(t *testing.T, line, msg string) {
	t.Helper()

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		assert.Contains(collect, env.logs.String(), line, msg)
	}, waitFor, tick, msg)
}

// awaitStoreErrors waits until /info counts the dynamic segments in store error: want is the count, and state the
// state of the connection that /info shows ("" for any).
func (env *chaos) awaitStoreErrors(t *testing.T, want func(int) bool, msg string) {
	t.Helper()

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		n, state := env.storeErrors(t)
		assert.True(collect, want(n), "%s: /info shows %d segments in store error, connection %q", msg, n, state)
	}, waitFor, tick, msg)
}

// TestChaos_KillStore stops the container under a running relay, and starts it again.
//
// Rows of the outage table: the healthy store, gone with an answer that is not cached, gone with a cached answer,
// breaker open, snapshot loaded then the store is gone, snapshot with the store back, store back.
func TestChaos_KillStore(t *testing.T) {
	t.Setenv(serialMarker, "1") // serial: time is the measure

	env := startChaos(t, 3)

	// the answers that the store gave are cached for five minutes
	for _, key := range []string{"alice", "zed"} {
		_, err := env.eval(lookupFlag, key)
		require.NoError(t, err)
	}

	env.store.stop(t)

	env.awaitAnswer(t, lookupFlag, env.key(), rowGoneUncached, isStoreErr)
	env.awaitAnswer(t, lookupFlag, "alice", rowGoneCached+": a member for the TTL", isMember)
	env.awaitAnswer(t, lookupFlag, "zed", rowGoneCached+": a non-member for the negative TTL", notMember)
	env.awaitAnswer(t, snapFlag, "alice", rowSnapGone, isStale)
	env.awaitAnswer(t, snapFlag, "mallory", rowSnapGone+": a non-member", func(a answer) bool {
		return !a.Value && a.status() == staleStatus
	})

	env.awaitBreaker(t, "open", func(n float64) bool { return n >= 1 }, rowBreakerOpen)

	got, err := env.eval(lookupFlag, env.key())
	require.NoError(t, err)
	assert.True(t, isStoreErr(got), "%s: not a member, STORE_ERROR: %+v", rowBreakerOpen, got)

	short, _ := env.metric(t, "semaphore_segment_lookups_total", map[string]string{labelResult: "short_circuited"})
	assert.Positive(t, short, "%s: the lookups were short-circuited, the store was not called", rowBreakerOpen)

	env.awaitStoreErrors(t, func(n int) bool { return n >= 1 }, "/info counts the segments in store error")
	env.awaitLog(t, "a segment store fails", "the loop logs the failing store once")
	env.assertReady(t)

	env.store.start(t)
	env.proxy.SetTarget(env.store.address)
	env.store.add(t, "aud", "alice")
	env.store.add(t, "snap", "alice", "bob")

	env.awaitAnswer(t, lookupFlag, env.key(), rowStoreBack+": the lookup recovers without a restart", notMember)
	env.awaitAnswer(t, lookupFlag, "alice", rowStoreBack, isMember)
	env.awaitAnswer(t, snapFlag, "bob", rowSnapBack, isMember)
	env.awaitBreaker(t, "open", func(n float64) bool { return n == 0 }, rowStoreBack+": the breaker closes")
	env.awaitStoreErrors(t, func(n int) bool { return n == 0 }, rowStoreBack+": /info is healthy again")
	env.awaitLog(t, "a segment store works again", "the loop logs the store that works again")
	env.assertReady(t)
}

// TestChaos_PauseStore pauses the container: the connections stay open and nothing answers (no reset).
//
// Rows of the outage table: gone with an answer that is not cached (every evaluation ends at the lookup timeout, plus
// the margin: evalBound), snapshot call hangs (the held set answers, STALE), store back.
func TestChaos_PauseStore(t *testing.T) {
	t.Setenv(serialMarker, "1") // serial: time is the measure

	// a breaker that never opens: every evaluation has to wait for its timeout
	env := startChaos(t, 1_000_000)

	_, _ = env.storm(t, 8, 10) // the connections and the goroutines of a healthy load of this shape

	runtime.GC()

	healthy := runtime.NumGoroutine()

	env.store.pause(t)

	answers, slowest := env.storm(t, 8, 10)
	for _, got := range answers {
		assert.True(t, isStoreErr(got), "%s: %+v", rowGoneUncached, got)
	}

	assert.Len(t, answers, 80)
	t.Logf("paused store: slowest evaluation %s (bound %s)", slowest, evalBound)
	assert.LessOrEqual(
		t,
		slowest,
		evalBound,
		"%s: every evaluation ends within the lookup timeout %s plus the margin %s",
		rowGoneUncached,
		lookupTimeout,
		margin,
	)

	// 8 requests in flight, the connection that hangs and its reconnect: the goroutines of the load do not pile up
	// with the number of requests (80 here)
	const goroutineBound = 40

	t.Logf("goroutines: %d healthy, %d paused", healthy, runtime.NumGoroutine())
	assert.LessOrEqual(t, runtime.NumGoroutine()-healthy, goroutineBound)

	env.awaitAnswer(t, snapFlag, "alice", rowSnapHang, isStale)
	env.awaitStoreErrors(t, func(n int) bool { return n >= 1 }, "the hung store is down in /info")
	env.awaitLog(t, "a segment store fails", "the refresh loop reports the hung store")
	env.assertReady(t)

	env.store.unpause(t)

	env.awaitAnswer(t, lookupFlag, env.key(), rowStoreBack, notMember)
	env.awaitAnswer(t, snapFlag, "alice", rowSnapBack, isMember)
	env.awaitStoreErrors(t, func(n int) bool { return n == 0 }, rowStoreBack+": /info is healthy again")
	assert.LessOrEqual(t, runtime.NumGoroutine()-healthy, goroutineBound, "goroutines after the recovery")
}

// TestChaos_AddedLatencyAndCut puts a delay between the relay and the store, below and above the lookup timeout, and
// cuts the connections.
//
// Rows of the outage table: healthy (a slow store below the timeout answers right), gone with an answer that is not
// cached (above the timeout), store back.
func TestChaos_AddedLatencyAndCut(t *testing.T) {
	t.Setenv(serialMarker, "1") // serial: time is the measure

	env := startChaos(t, 1_000_000)

	labels := map[string]string{"connection": connName}

	mean := func(run func()) float64 {
		beforeSum, beforeN := env.metric(t, "semaphore_segment_lookup_duration_seconds", labels)

		run()

		sum, calls := env.metric(t, "semaphore_segment_lookup_duration_seconds", labels)
		require.Greater(t, calls, beforeN, "the run made resolver calls")

		return (sum - beforeSum) / float64(calls-beforeN)
	}

	lookups := func(want func(answer) bool, msg string, count int) {
		for range count {
			got, took, err := env.timed(lookupFlag, env.key())
			require.NoError(t, err)
			assert.True(t, want(got), "%s: %+v", msg, got)
			assert.LessOrEqual(t, took, evalBound, "%s", msg)
		}

		got, err := env.eval(lookupFlag, "alice") // cached: the delay is not on its path
		require.NoError(t, err)
		assert.True(t, isMember(got), msg)
	}

	baseline := mean(func() { lookups(notMember, rowHealthy, 10) })

	// 50 ms in each direction: a call is one write and one read, so it takes 100 ms more, below the 300 ms timeout
	const delay = 50 * time.Millisecond

	env.proxy.SetDelay(delay)

	slow := mean(func() { lookups(notMember, rowHealthy+": slow below the timeout", 10) })
	t.Logf("resolver call: mean %.1f ms healthy, %.1f ms with %s added each way", baseline*1e3, slow*1e3, delay)
	// the delay only adds time, so the lower bound cannot flake: 2 x 50 ms, less 20 percent
	assert.GreaterOrEqual(t, slow-baseline, 0.08, "the duration histogram shifts by the added latency")

	// above the timeout: 400 ms in each direction
	env.proxy.SetDelay(4 * delay * 2)

	before, _ := env.metric(t, "semaphore_segment_unknown_total", map[string]string{"reason": "store"})

	for range 5 {
		got, took, err := env.timed(lookupFlag, env.key())
		require.NoError(t, err)
		assert.True(t, isStoreErr(got), "%s: %+v", rowGoneUncached, got)
		assert.GreaterOrEqual(t, took, lookupTimeout*9/10, "the call waited for its deadline")
		assert.LessOrEqual(t, took, evalBound, "%s", rowGoneUncached)
	}

	after, _ := env.metric(t, "semaphore_segment_unknown_total", map[string]string{"reason": "store"})
	assert.Greater(t, after, before, "the lookups that timed out are counted as unknown, reason store")

	snap, err := env.eval(snapFlag, "alice")
	require.NoError(t, err)
	assert.True(t, snap.Value, "the snapshot answers from memory while the store is slow")

	// the connections are cut, the delay is gone: the client dials again
	env.proxy.SetDelay(0)
	env.proxy.Cut()

	env.awaitAnswer(t, lookupFlag, env.key(), rowStoreBack+": after a cut", notMember)
	env.awaitAnswer(t, snapFlag, "bob", rowSnapBack, isMember)
	env.assertReady(t)
}

// TestChaos_StoreDownAtStart starts the relay while nothing reaches the store.
//
// Rows of the outage table: never connected with an answer that is not cached, snapshot never loaded, then the store
// is back.
func TestChaos_StoreDownAtStart(t *testing.T) {
	t.Setenv(serialMarker, "1")

	backend := startStore(t)
	backend.add(t, "aud", "alice")
	backend.add(t, "snap", "alice")

	proxy, err := testproxy.Start(backend.address)
	require.NoError(t, err)
	t.Cleanup(proxy.Close)

	proxy.SetRefuse(true)

	r := startRig(t, &rigOptions{
		address: proxy.Addr(), resolver: chaosResolver(3), flags: chaosFlags, segments: chaosSegments,
	})

	r.awaitAnswer(t, lookupFlag, "alice", rowGoneUncached+": never connected", isStoreErr)
	r.awaitAnswer(t, snapFlag, "alice", rowSnapNever, isStoreErr)
	r.assertReady(t)

	proxy.SetRefuse(false)

	r.awaitAnswer(t, lookupFlag, "alice", rowStoreBack, isMember)
	r.awaitAnswer(t, snapFlag, "alice", rowSnapBack, isMember)
	r.assertReady(t)
}

// TestChaos_SnapshotSetDeletedOrBroken changes one set under a running relay.
//
// Rows of the outage table: snapshot set deleted (nobody is a member, no status), snapshot set that the store cannot
// give (a key of the wrong type: the last good set, STALE).
func TestChaos_SnapshotSetDeletedOrBroken(t *testing.T) {
	t.Setenv(serialMarker, "1") // serial: time is the measure

	env := startChaos(t, 3)

	del := env.store.client.B().Del().Key("snap").Build()
	require.NoError(t, env.store.client.Do(t.Context(), del).Error())
	env.store.version(t, "snap")

	env.awaitAnswer(t, snapFlag, "alice", rowSnapDeleted, notMember)
	env.awaitAnswer(t, lookupFlag, "alice", rowHealthy+": the other segment is unaffected", isMember)

	env.store.add(t, "snap", "alice")
	env.awaitAnswer(t, snapFlag, "alice", rowSnapBack+": the set is back", isMember)

	// the key is now a string: SSCAN answers WRONGTYPE, the connection stays up
	require.NoError(
		t,
		env.store.client.Do(t.Context(), env.store.client.B().Set().Key("snap").Value("x").Build()).Error(),
	)
	env.store.version(t, "snap")

	env.awaitAnswer(t, snapFlag, "alice", rowSnapBroken, isStale)
	env.awaitAnswer(t, lookupFlag, "alice", rowSnapBroken+": the other segment is unaffected", isMember)
	env.assertReady(t)
}
