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
			name:  "an unknown answer is never cached",
			store: &fakeStore{data: members(map[string][]string{"other": {idAlice}})},
			steps: []step{
				{lookups: []segment.Lookup{lk(segBeta, idAlice)}, wantStates: []segment.State{segment.Unknown}},
				{lookups: []segment.Lookup{lk(segBeta, idAlice)}, wantStates: []segment.State{segment.Unknown}},
			},
			wantCalls: [][]string{{callAlice}, {callAlice}},
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
			store: &fakeStore{data: members(map[string][]string{segBeta: {idAlice}, "gamma": {}})},
			steps: []step{
				{
					lookups:    []segment.Lookup{lk(segBeta, idAlice), lk("gamma", idAlice)},
					wantStates: []segment.State{segment.Member, segment.NotMember},
				},
				{
					lookups:    []segment.Lookup{lk("gamma", idAlice), lk(segBeta, idAlice)},
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

func TestBreakerOpensAndShortCircuits(t *testing.T) {
	t.Parallel()

	store := &fakeStore{data: members(map[string][]string{segBeta: {idAlice}}), fail: errStore}
	counters := &resolver.Counters{}
	res := newResolver(t, store, &resolver.Options{
		Observer: counters,
		Segments: map[string]resolver.SegmentOptions{segBeta: {BreakerFailures: 2, NoCache: true}},
	})

	for range 2 {
		err := res.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)})
		require.ErrorIs(t, err, errStore)
	}

	assert.Equal(t, resolver.Status{Segments: map[string]resolver.SegmentStatus{
		segBeta: {Breaker: resolver.BreakerOpen, Failures: 2},
	}}, res.Status())

	lookups := []segment.Lookup{lk(segBeta, idAlice)}
	err := res.Contains(context.Background(), lookups)

	require.ErrorIs(t, err, resolver.ErrCircuitOpen)
	assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))
	assert.Equal(t, [][]string{{callAlice}, {callAlice}}, store.callList(), "no third call to the store")

	stats := counters.Snapshot()
	assert.Equal(t, uint64(3), stats.Calls)
	assert.Equal(t, uint64(1), stats.ShortCircuited)
	assert.Equal(t, uint64(3), stats.Failed)
	assert.Equal(t, uint64(1), stats.BreakerOpened)
}

func TestBreakerIsPerSegment(t *testing.T) {
	t.Parallel()

	store := &fakeStore{
		data:         members(map[string][]string{segBeta: {idAlice}, "gamma": {idAlice}}),
		failSegments: map[string]bool{segBeta: true},
	}
	res := newResolver(t, store, &resolver.Options{Default: resolver.SegmentOptions{BreakerFailures: 1, NoCache: true}})

	first := []segment.Lookup{lk(segBeta, idAlice), lk("gamma", idAlice)}
	require.ErrorIs(t, res.Contains(context.Background(), first), errStore)

	second := []segment.Lookup{lk(segBeta, idAlice), lk("gamma", idAlice)}
	err := res.Contains(context.Background(), second)

	require.ErrorIs(t, err, resolver.ErrCircuitOpen)
	assert.Equal(t, []segment.State{segment.Unknown, segment.Member}, states(second))
	assert.Equal(t, [][]string{{callAlice, callGammaAlice}, {callGammaAlice}}, store.callList())
	assert.Equal(t, resolver.BreakerOpen, res.Status().Segments[segBeta].Breaker)
	assert.Equal(t, resolver.BreakerClosed, res.Status().Segments["gamma"].Breaker)
}

func TestNoBreakerNeverOpens(t *testing.T) {
	t.Parallel()

	store := &fakeStore{fail: errStore}
	res := newResolver(
		t,
		store,
		&resolver.Options{Default: resolver.SegmentOptions{NoBreaker: true, NoCache: true, BreakerFailures: 1}},
	)

	for range 3 {
		require.ErrorIs(t, res.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)}), errStore)
	}

	assert.Len(t, store.callList(), 3)
	assert.Equal(t, resolver.BreakerClosed, res.Status().Segments[segBeta].Breaker)
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
	store.gate = make(chan struct{})
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
	store.gate = make(chan struct{})
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

	res.Invalidate("other")
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
	store.gate = make(chan struct{})
	store.entered = make(chan struct{}, 1)
	res := newResolver(t, store, &resolver.Options{})

	var wg sync.WaitGroup

	wg.Go(func() { _ = res.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)}) })

	<-store.entered

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.ErrorIs(t, res.Shutdown(ctx), resolver.ErrWaiting)

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
