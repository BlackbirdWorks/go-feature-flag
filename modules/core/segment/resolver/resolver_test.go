package resolver_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

const (
	callAlice      = "beta:alice"
	callZed        = "beta:zed"
	callGammaAlice = "gamma:alice"
	eventuallyWait = 10 * time.Second
	eventuallyTick = time.Millisecond
	leaveBound     = 5 * time.Second
	segBeta        = "beta"
	idAlice        = "alice"
	idZed          = "zed"
)

func lk(seg, id string) segment.Lookup { return segment.Lookup{Segment: seg, ID: id} }

func states(lookups []segment.Lookup) []segment.State {
	out := make([]segment.State, len(lookups))
	for i := range lookups {
		out[i] = lookups[i].State
	}

	return out
}

func newResolver(t *testing.T, store segment.Provider, opts *resolver.Options) *resolver.Resolver {
	t.Helper()

	res, err := resolver.New(store, opts)
	require.NoError(t, err)

	// every resolver of a test is shut down when the test ends, so that no run loop is mid-exit when the leak scan of
	// TestMain looks: a store that waits on a gate is released first, the wait is bounded
	t.Cleanup(func() {
		if releaser, ok := store.(interface{ Release() }); ok {
			releaser.Release()
		}

		ctx, cancel := context.WithTimeout(context.Background(), eventuallyWait)
		defer cancel()

		assert.NoError(
			t,
			res.Shutdown(ctx),
		) //nolint:testifylint // cleanup: the test goroutine continues to the next cleanup
	})

	return res
}

func betaStore() *fakeStore {
	return &fakeStore{data: members(map[string][]string{segBeta: {idAlice}})}
}

func TestContainsCaching(t *testing.T) {
	t.Parallel()

	type step struct {
		wantErr    error
		lookups    []segment.Lookup
		wantStates []segment.State
	}

	tests := []struct {
		store     *fakeStore
		opts      *resolver.Options
		name      string
		steps     []step
		wantCalls [][]string
	}{
		{
			name: "a member and a non member are served from the cache the second time",
			steps: []step{
				{
					lookups:    []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, idZed)},
					wantStates: []segment.State{segment.Member, segment.NotMember},
				},
				{
					lookups:    []segment.Lookup{lk(segBeta, idZed), lk(segBeta, idAlice)},
					wantStates: []segment.State{segment.NotMember, segment.Member},
				},
			},
			wantCalls: [][]string{{callAlice, callZed}},
		},
		{
			name:  "an unknown answer is never cached: the segment that is not loaded is not asked again for a while",
			store: &fakeStore{data: members(map[string][]string{segOther: {idAlice}})},
			steps: []step{
				{lookups: []segment.Lookup{lk(segBeta, idAlice)}, wantStates: []segment.State{segment.Unknown}},
				{lookups: []segment.Lookup{lk(segBeta, idAlice)}, wantStates: []segment.State{segment.Unknown}},
			},
			wantCalls: [][]string{{callAlice}},
		},
		{
			name:  "an error is never cached",
			store: &fakeStore{data: members(map[string][]string{segBeta: {idAlice}}), fail: errStore},
			steps: []step{
				{
					lookups:    []segment.Lookup{lk(segBeta, idAlice)},
					wantStates: []segment.State{segment.Unknown},
					wantErr:    errStore,
				},
				{
					lookups:    []segment.Lookup{lk(segBeta, idAlice)},
					wantStates: []segment.State{segment.Unknown},
					wantErr:    errStore,
				},
			},
			wantCalls: [][]string{{callAlice}, {callAlice}},
		},
		{
			name: "only the misses go to the store and positions are kept",
			steps: []step{
				{lookups: []segment.Lookup{lk(segBeta, idAlice)}, wantStates: []segment.State{segment.Member}},
				{
					lookups:    []segment.Lookup{lk(segBeta, idZed), lk(segBeta, idAlice), lk(segBeta, "bob")},
					wantStates: []segment.State{segment.NotMember, segment.Member, segment.NotMember},
				},
			},
			wantCalls: [][]string{{callAlice}, {callZed, "beta:bob"}},
		},
		{
			name: "the same lookup twice in a call is asked once",
			steps: []step{
				{
					lookups:    []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, idAlice)},
					wantStates: []segment.State{segment.Member, segment.Member},
				},
			},
			wantCalls: [][]string{{callAlice}},
		},
		{
			name: "NoCache asks the store every time",
			opts: &resolver.Options{Default: resolver.SegmentOptions{NoCache: true}},
			steps: []step{
				{lookups: []segment.Lookup{lk(segBeta, idAlice)}, wantStates: []segment.State{segment.Member}},
				{lookups: []segment.Lookup{lk(segBeta, idAlice)}, wantStates: []segment.State{segment.Member}},
			},
			wantCalls: [][]string{{callAlice}, {callAlice}},
		},
		{
			name: "trimLower normalises before the cache and the store",
			opts: &resolver.Options{
				Segments: map[string]resolver.SegmentOptions{segBeta: {Normalize: resolver.NormalizeTrimLower}},
			},
			steps: []step{
				{lookups: []segment.Lookup{lk(segBeta, "  Alice ")}, wantStates: []segment.State{segment.Member}},
				{lookups: []segment.Lookup{lk(segBeta, "ALICE")}, wantStates: []segment.State{segment.Member}},
			},
			wantCalls: [][]string{{callAlice}},
		},
		{
			name:  "the answers of a segment are kept apart from another segment",
			store: &fakeStore{data: members(map[string][]string{segBeta: {idAlice}, segGamma: {}})},
			steps: []step{
				{
					lookups:    []segment.Lookup{lk(segBeta, idAlice), lk(segGamma, idAlice)},
					wantStates: []segment.State{segment.Member, segment.NotMember},
				},
				{
					lookups:    []segment.Lookup{lk(segGamma, idAlice), lk(segBeta, idAlice)},
					wantStates: []segment.State{segment.NotMember, segment.Member},
				},
			},
			wantCalls: [][]string{{callAlice, callGammaAlice}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := tt.store
			if store == nil {
				store = betaStore()
			}

			res := newResolver(t, store, tt.opts)

			for _, step := range tt.steps {
				err := res.Contains(context.Background(), step.lookups)

				if step.wantErr != nil {
					require.ErrorIs(t, err, step.wantErr)
				} else {
					require.NoError(t, err)
				}

				assert.Equal(t, step.wantStates, states(step.lookups))
			}

			assert.Equal(t, tt.wantCalls, store.callList())
		})
	}
}

func TestContainsNoLookups(t *testing.T) {
	t.Parallel()

	store := betaStore()
	res := newResolver(t, store, &resolver.Options{})

	require.NoError(t, res.Contains(context.Background(), nil))
	assert.Empty(t, store.callList())
}

func TestBreakerThroughTheResolver(t *testing.T) {
	t.Parallel()

	type step struct {
		wantErr    error
		lookups    []segment.Lookup
		wantStates []segment.State
	}

	alice := func() []segment.Lookup { return []segment.Lookup{lk(segBeta, idAlice)} }
	unknown := []segment.State{segment.Unknown}

	tests := []struct {
		store        *fakeStore
		opts         *resolver.Options
		wantBreakers map[string]resolver.BreakerState
		name         string
		steps        []step
		wantCalls    [][]string
		wantStats    resolver.Stats
	}{
		{
			name:  "it opens after the configured failures and then calls nothing",
			store: &fakeStore{data: members(map[string][]string{segBeta: {idAlice}}), fail: errStore},
			opts: &resolver.Options{
				Segments: map[string]resolver.SegmentOptions{segBeta: {BreakerFailures: 2, NoCache: true}},
			},
			steps: []step{
				{lookups: alice(), wantStates: unknown, wantErr: errStore},
				{lookups: alice(), wantStates: unknown, wantErr: errStore},
				{lookups: alice(), wantStates: unknown, wantErr: resolver.ErrCircuitOpen},
			},
			wantCalls:    [][]string{{callAlice}, {callAlice}},
			wantBreakers: map[string]resolver.BreakerState{segBeta: resolver.BreakerOpen},
			wantStats: resolver.Stats{
				Calls:          3,
				Lookups:        3,
				Misses:         2,
				ShortCircuited: 1,
				Unknown:        3,
				Failed:         3,
				BreakerOpened:  1,
			},
		},
		{
			name: "it is per segment: the other segment keeps its answers",
			store: &fakeStore{
				data:         members(map[string][]string{segBeta: {idAlice}, segGamma: {idAlice}}),
				failSegments: map[string]bool{segBeta: true},
			},
			opts: &resolver.Options{Default: resolver.SegmentOptions{BreakerFailures: 1, NoCache: true}},
			steps: []step{
				{
					lookups:    []segment.Lookup{lk(segBeta, idAlice), lk(segGamma, idAlice)},
					wantStates: []segment.State{segment.Unknown, segment.Member},
					wantErr:    errStore,
				},
				{
					lookups:    []segment.Lookup{lk(segBeta, idAlice), lk(segGamma, idAlice)},
					wantStates: []segment.State{segment.Unknown, segment.Member},
					wantErr:    resolver.ErrCircuitOpen,
				},
			},
			wantCalls: [][]string{{callAlice, callGammaAlice}, {callGammaAlice}},
			wantBreakers: map[string]resolver.BreakerState{
				segBeta:  resolver.BreakerOpen,
				segGamma: resolver.BreakerClosed,
			},
			wantStats: resolver.Stats{
				Calls:          2,
				Lookups:        4,
				Misses:         3,
				ShortCircuited: 1,
				Unknown:        2,
				Failed:         2,
				BreakerOpened:  1,
			},
		},
		{
			name:  "NoBreaker never opens",
			store: &fakeStore{fail: errStore},
			opts: &resolver.Options{
				Default: resolver.SegmentOptions{NoBreaker: true, NoCache: true, BreakerFailures: 1},
			},
			steps: []step{
				{lookups: alice(), wantStates: unknown, wantErr: errStore},
				{lookups: alice(), wantStates: unknown, wantErr: errStore},
				{lookups: alice(), wantStates: unknown, wantErr: errStore},
			},
			wantCalls:    [][]string{{callAlice}, {callAlice}, {callAlice}},
			wantBreakers: map[string]resolver.BreakerState{segBeta: resolver.BreakerClosed},
			wantStats:    resolver.Stats{Calls: 3, Lookups: 3, Misses: 3, Unknown: 3, Failed: 3},
		},
		{
			name:  "a segment that is not loaded is neither a failure nor a success",
			store: &fakeStore{data: members(map[string][]string{segOther: {idAlice}})},
			opts:  &resolver.Options{Default: resolver.SegmentOptions{BreakerFailures: 1, NoCache: true}},
			steps: []step{
				{lookups: alice(), wantStates: unknown},
				{lookups: alice(), wantStates: unknown},
			},
			wantCalls:    [][]string{{callAlice}},
			wantBreakers: map[string]resolver.BreakerState{segBeta: resolver.BreakerClosed},
			wantStats:    resolver.Stats{Calls: 2, Lookups: 2, Misses: 1, NotLoaded: 1, Unknown: 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			counters := &resolver.Counters{}
			tt.opts.Observer = counters
			res := newResolver(t, tt.store, tt.opts)

			for _, stp := range tt.steps {
				err := res.Contains(context.Background(), stp.lookups)

				if stp.wantErr != nil {
					require.ErrorIs(t, err, stp.wantErr)
				} else {
					require.NoError(t, err)
				}

				assert.Equal(t, stp.wantStates, states(stp.lookups))
			}

			assert.Equal(t, tt.wantCalls, tt.store.callList())

			got := map[string]resolver.BreakerState{}
			for name, seg := range res.Status().Segments {
				got[name] = seg.Breaker
			}

			assert.Equal(t, tt.wantBreakers, got)

			stats := counters.Snapshot()
			stats.Duration = 0
			assert.Equal(t, tt.wantStats, stats)
		})
	}
}

// waitLeft waits for a caller to return while the gate of the store is shut. A caller that is still there after the
// bound is held past its context: the gate is opened so that the test can end, and the test fails.
func waitLeft(t *testing.T, left <-chan struct{}, gate chan struct{}) {
	t.Helper()

	select {
	case <-left:
	case <-time.After(leaveBound):
		close(gate)
		<-left

		require.FailNow(t, "the caller was held past the end of its context")
	}
}

func waitForJoin(t *testing.T, res *resolver.Resolver, name string, want uint64) {
	t.Helper()

	assert.Eventually(
		t,
		func() bool { return res.Status().Segments[name].Joined >= want },
		eventuallyWait,
		eventuallyTick,
	)
}

func TestCoalescingAndWaitingCallers(t *testing.T) {
	t.Parallel()

	store := betaStore()
	store.gate = gateFor(t)
	store.entered = make(chan struct{}, 4)
	counters := &resolver.Counters{}
	res := newResolver(t, store, &resolver.Options{Observer: counters})

	var wg sync.WaitGroup

	leadLookups := []segment.Lookup{lk(segBeta, idAlice)}

	var leadErr error

	wg.Go(func() { leadErr = res.Contains(context.Background(), leadLookups) })

	<-store.entered // the shared call is in the store

	followCtx, cancelFollower := context.WithCancel(context.Background())
	followLookups := []segment.Lookup{lk(segBeta, idAlice)}
	followDone := make(chan struct{})

	var followErr error

	go func() {
		defer close(followDone)

		followErr = res.Contains(followCtx, followLookups)
	}()

	waitForJoin(t, res, segBeta, 1)
	cancelFollower()
	waitLeft(t, followDone, store.gate) // the follower leaves while the store has not answered: the gate is still shut

	close(store.gate)
	wg.Wait()

	require.ErrorIs(t, followErr, resolver.ErrWaiting)
	require.ErrorIs(t, followErr, context.Canceled)
	assert.Equal(t, []segment.State{segment.Unknown}, states(followLookups))

	require.NoError(t, leadErr)
	assert.Equal(t, []segment.State{segment.Member}, states(leadLookups))
	assert.Equal(t, [][]string{{callAlice}}, store.callList(), "one call for both callers")
	assert.Equal(t, []error{nil}, store.ctxErrors(), "the context of the call outlived the follower")
	assert.Equal(t, uint64(1), counters.Snapshot().Coalesced)
}

func TestLeaderLeavesButFollowerGetsTheAnswer(t *testing.T) {
	t.Parallel()

	store := betaStore()
	store.gate = gateFor(t)
	store.entered = make(chan struct{}, 4)
	res := newResolver(t, store, &resolver.Options{})

	var wg sync.WaitGroup

	leadCtx, cancelLeader := context.WithCancel(context.Background())
	leadLookups := []segment.Lookup{lk(segBeta, idAlice)}
	followLookups := []segment.Lookup{lk(segBeta, idAlice)}
	leadDone := make(chan struct{})

	var leadErr, followErr error

	go func() {
		defer close(leadDone)

		leadErr = res.Contains(leadCtx, leadLookups)
	}()

	<-store.entered

	wg.Go(func() { followErr = res.Contains(context.Background(), followLookups) })

	waitForJoin(t, res, segBeta, 1)
	cancelLeader()
	waitLeft(t, leadDone, store.gate)
	close(store.gate)
	wg.Wait()

	require.ErrorIs(t, leadErr, resolver.ErrWaiting)
	assert.Equal(t, []segment.State{segment.Unknown}, states(leadLookups))
	require.NoError(t, followErr)
	assert.Equal(t, []segment.State{segment.Member}, states(followLookups))
	assert.Equal(t, [][]string{{callAlice}}, store.callList())
	assert.Equal(t, []error{nil}, store.ctxErrors(), "the shared call outlived the caller that started it")
}

func TestDifferentLookupsAreNotCoalesced(t *testing.T) {
	t.Parallel()

	store := betaStore()
	res := newResolver(t, store, &resolver.Options{Default: resolver.SegmentOptions{NoCache: true}})

	first := []segment.Lookup{lk(segBeta, idAlice)}
	second := []segment.Lookup{lk(segBeta, idZed)}

	require.NoError(t, res.Contains(context.Background(), first))
	require.NoError(t, res.Contains(context.Background(), second))
	assert.Equal(t, [][]string{{callAlice}, {callZed}}, store.callList())
}

func TestPanickingStoreIsAnError(t *testing.T) {
	t.Parallel()

	store := &fakeStore{panicValue: errBoom}
	res := newResolver(t, store, &resolver.Options{})
	lookups := []segment.Lookup{lk(segBeta, idAlice)}

	err := res.Contains(context.Background(), lookups)

	require.ErrorIs(t, err, resolver.ErrProviderPanic)
	assert.NotContains(t, err.Error(), "boom")
	assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))
}

func TestInvalidateDropsTheCachedAnswers(t *testing.T) {
	t.Parallel()

	store := betaStore()
	res := newResolver(t, store, &resolver.Options{})

	require.NoError(t, res.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)}))
	assert.Equal(t, 1, res.Status().Segments[segBeta].Entries)

	res.Invalidate(segOther)
	res.Invalidate(segBeta)
	assert.Equal(t, 0, res.Status().Segments[segBeta].Entries)

	require.NoError(t, res.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)}))
	assert.Equal(t, [][]string{{callAlice}, {callAlice}}, store.callList())
}

func TestLifecycle(t *testing.T) {
	t.Parallel()

	plain := newResolver(t, betaStore(), &resolver.Options{})
	require.NoError(t, plain.Init(context.Background()))

	_, err := plain.Exists(context.Background(), []string{segBeta})
	require.ErrorIs(t, err, resolver.ErrUnsupported)

	full := &lifecycleStore{fakeStore: betaStore()}
	res := newResolver(t, full, &resolver.Options{})

	require.NoError(t, res.Init(context.Background()))

	got, err := res.Exists(context.Background(), []string{segBeta, "x"})
	require.NoError(t, err)
	assert.Equal(t, []segment.State{segment.Member, segment.NotMember}, got)

	require.NoError(t, res.Shutdown(context.Background()))
	assert.True(t, res.Status().Closed)
	assert.Equal(t, []string{"init", "exists", "shutdown"}, full.events)

	err = res.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)})
	require.ErrorIs(t, err, resolver.ErrClosed)
}

func TestShutdownWaitsForTheCallsInFlightUntilItsContextEnds(t *testing.T) {
	t.Parallel()

	store := betaStore()
	store.gate = gateFor(t)
	store.entered = make(chan struct{}, 1)
	res := newResolver(t, store, &resolver.Options{})

	var wg sync.WaitGroup

	wg.Go(func() { _ = res.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)}) })

	<-store.entered

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := res.Shutdown(ctx)

	require.ErrorIs(t, err, resolver.ErrShutdown)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(
		t,
		"resolver: shutdown ended before the calls in flight: 1 calls still running: context canceled",
		err.Error(),
	)

	close(store.gate)
	wg.Wait()
	require.NoError(t, res.Shutdown(context.Background()))
}

type lifecycleStore struct {
	*fakeStore

	events []string
}

func (l *lifecycleStore) Init(context.Context) error { //nolint:unparam // the interface returns an error
	l.events = append(l.events, "init")

	return nil
}

func (l *lifecycleStore) Shutdown(context.Context) error { //nolint:unparam // the interface returns an error
	l.events = append(l.events, "shutdown")

	return nil
}

func (l *lifecycleStore) Exists(
	_ context.Context,
	names []string,
) ([]segment.State, error) { //nolint:unparam // the interface returns an error
	l.events = append(l.events, "exists")

	out := make([]segment.State, len(names))
	for i, name := range names {
		out[i] = segment.NotMember
		if _, ok := l.data[name]; ok {
			out[i] = segment.Member
		}
	}

	return out, nil
}
