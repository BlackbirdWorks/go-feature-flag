package resolver_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

// segRecorder keeps the observations of the calls in order.
type segRecorder struct {
	segments []resolver.SegmentObservation
	calls    []resolver.CallObservation
	counters resolver.Counters
	mu       sync.Mutex
}

// Snapshot returns the totals of the observed calls.
func (r *segRecorder) Snapshot() resolver.Stats { return r.counters.Snapshot() }

func (r *segRecorder) ObserveBreaker(ev resolver.BreakerEvent) { r.counters.ObserveBreaker(ev) }

func (r *segRecorder) ObserveCall(obs resolver.CallObservation) { //nolint:gocritic // hugeParam: by value
	r.counters.ObserveCall(obs)

	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls = append(r.calls, obs)
}

func (r *segRecorder) ObserveSegment(obs resolver.SegmentObservation) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.segments = append(r.segments, obs)
}

func (r *segRecorder) observed() ([]resolver.CallObservation, []resolver.SegmentObservation) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]resolver.CallObservation{}, r.calls...), append([]resolver.SegmentObservation{}, r.segments...)
}

func TestNothingReachesTheStoreAfterShutdown(t *testing.T) {
	t.Parallel()

	const (
		iterations = 200
		callers    = 4
		perCaller  = 3
	)

	for iteration := range iterations {
		store := betaStore()
		res := newResolver(t, store, &resolver.Options{Default: resolver.SegmentOptions{NoCache: true}})
		start := make(chan struct{})
		errs := make([][]error, callers)

		var wg sync.WaitGroup

		for caller := range callers {
			wg.Go(func() {
				<-start

				for round := range perCaller {
					lookups := []segment.Lookup{lk(segBeta, fmt.Sprintf("id-%d-%d", caller, round))}
					errs[caller] = append(errs[caller], res.Contains(context.Background(), lookups))
				}
			})
		}

		close(start)

		require.NoError(t, res.Shutdown(context.Background()), "iteration %d", iteration)

		reached := len(store.callList())

		wg.Wait()

		assert.Len(
			t,
			store.callList(),
			reached,
			"a call reached the store after Shutdown returned, iteration %d",
			iteration,
		)

		for caller := range callers {
			for _, err := range errs[caller] {
				if err != nil {
					require.ErrorIs(t, err, resolver.ErrClosed)
				}
			}
		}
	}
}

func TestACallerAfterInvalidateDoesNotJoinAnOlderFlight(t *testing.T) {
	t.Parallel()

	store := &fakeStore{data: members(map[string][]string{segBeta: {idAlice}})}
	store.gate = gateFor(t)
	store.entered = make(chan struct{}, 4)
	res := newResolver(t, store, &resolver.Options{})

	var wg sync.WaitGroup

	before := []segment.Lookup{lk(segBeta, idAlice)}
	after := []segment.Lookup{lk(segBeta, idAlice)}

	var beforeErr, afterErr error

	wg.Go(func() { beforeErr = res.Contains(context.Background(), before) })

	<-store.entered // the first call is in the store, with the members as they are now

	store.setData(members(map[string][]string{segBeta: {}})) // the set changes
	res.Invalidate(segBeta)

	wg.Go(func() { afterErr = res.Contains(context.Background(), after) })

	awaitEntered(t, store.entered, store.gate) // the second caller made a call of its own

	close(store.gate)
	wg.Wait()

	require.NoError(t, beforeErr)
	require.NoError(t, afterErr)
	assert.Equal(t, []segment.State{segment.Member}, states(before), "the call that started before keeps its answer")
	assert.Equal(t, []segment.State{segment.NotMember}, states(after), "the caller after Invalidate sees the new set")
	assert.Equal(t, [][]string{{callAlice}, {callAlice}}, store.callList())
	assert.Equal(t, uint64(0), res.Status().Segments[segBeta].Joined)

	cached := []segment.Lookup{lk(segBeta, idAlice)}
	require.NoError(t, res.Contains(context.Background(), cached))
	assert.Equal(t, []segment.State{segment.NotMember}, states(cached), "the stale answer was not cached")
	assert.Len(t, store.callList(), 2)
}

func TestIDsThatCannotBeLookedUp(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", resolver.DefaultMaxIDBytes+1)
	exact := strings.Repeat("b", resolver.DefaultMaxIDBytes)
	trim := map[string]resolver.SegmentOptions{segBeta: {Normalize: resolver.NormalizeTrimLower}}

	tests := []struct {
		name          string
		opts          *resolver.Options
		ids           []string
		wantStates    []segment.State
		wantCalls     [][]string
		wantOversized uint64
		wantInvalid   uint64
	}{
		{
			name:          "one byte over the default is not looked up",
			opts:          &resolver.Options{},
			ids:           []string{long, idAlice},
			wantStates:    []segment.State{segment.Unknown, segment.Member},
			wantCalls:     [][]string{{callAlice}},
			wantOversized: 1,
		},
		{
			name:          "a raw id far over the limit is not looked up even without normalisation",
			opts:          &resolver.Options{},
			ids:           []string{strings.Repeat("a", 3*resolver.DefaultMaxIDBytes)},
			wantStates:    []segment.State{segment.Unknown},
			wantCalls:     [][]string{},
			wantOversized: 1,
		},
		{
			name: "an id that grows when it is lowered is checked at the length that reaches the store",
			opts: &resolver.Options{Segments: trim},
			ids: []string{
				strings.Repeat("\u023a", resolver.DefaultMaxIDBytes/2),
			}, // 2 bytes each, 3 bytes lowered
			wantStates:    []segment.State{segment.Unknown},
			wantCalls:     [][]string{},
			wantOversized: 1,
		},
		{
			name:       "an id that shrinks when it is trimmed is looked up",
			opts:       &resolver.Options{Segments: trim},
			ids:        []string{strings.Repeat(" ", 20) + strings.Repeat("a", resolver.DefaultMaxIDBytes)},
			wantStates: []segment.State{segment.NotMember},
			wantCalls:  [][]string{{"beta:" + strings.Repeat("a", resolver.DefaultMaxIDBytes)}},
		},
		{
			name:       "exactly the default is looked up",
			opts:       &resolver.Options{},
			ids:        []string{exact},
			wantStates: []segment.State{segment.NotMember},
			wantCalls:  [][]string{{"beta:" + exact}},
		},
		{
			name:          "a custom limit",
			opts:          &resolver.Options{MaxIDBytes: 4},
			ids:           []string{idAlice, "bob"},
			wantStates:    []segment.State{segment.Unknown, segment.NotMember},
			wantCalls:     [][]string{{"beta:bob"}},
			wantOversized: 1,
		},
		{
			name:       "trimLower lowers and trims a valid id",
			opts:       &resolver.Options{Segments: trim},
			ids:        []string{"  ALICE ", "Ünï"},
			wantStates: []segment.State{segment.Member, segment.NotMember},
			wantCalls:  [][]string{{callAlice, "beta:ünï"}},
		},
		{
			name:        "trimLower leaves an id that is not UTF-8 unknown and does not send it",
			opts:        &resolver.Options{Segments: trim},
			ids:         []string{"\xff", "AL\xffICE", idAlice},
			wantStates:  []segment.State{segment.Unknown, segment.Unknown, segment.Member},
			wantCalls:   [][]string{{callAlice}},
			wantInvalid: 2,
		},
		{
			name:       "without normalisation an id that is not UTF-8 is an id like another",
			opts:       &resolver.Options{},
			ids:        []string{"\xff"},
			wantStates: []segment.State{segment.NotMember},
			wantCalls:  [][]string{{"beta:\xff"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := betaStore()
			rec := &segRecorder{}
			tt.opts.Observer = rec
			res := newResolver(t, store, tt.opts)

			lookups := make([]segment.Lookup, len(tt.ids))
			for i, id := range tt.ids {
				lookups[i] = lk(segBeta, id)
			}

			require.NoError(t, res.Contains(context.Background(), lookups))
			assert.Equal(t, tt.wantStates, states(lookups))
			assert.Equal(t, tt.wantCalls, store.callList())

			again := make([]segment.Lookup, len(tt.ids))
			for i, id := range tt.ids {
				again[i] = lk(segBeta, id)
			}

			require.NoError(t, res.Contains(context.Background(), again))
			assert.Equal(t, tt.wantStates, states(again), "the same answers the second time")
			assert.Equal(
				t,
				tt.wantCalls,
				store.callList(),
				"what was not looked up is never sent later, what was is cached",
			)

			stats := rec.Snapshot()
			assert.Equal(t, tt.wantOversized*2, stats.Oversized)
			assert.Equal(t, tt.wantInvalid*2, stats.Invalid)
		})
	}
}

func TestAStoreThatIgnoresItsContext(t *testing.T) {
	t.Parallel()

	store := betaStore()
	store.gate = gateFor(t)
	store.entered = make(chan struct{}, 4)
	store.deaf = true
	res := newResolver(t, store, &resolver.Options{CallTimeout: time.Nanosecond})

	lookups := []segment.Lookup{lk(segBeta, idAlice)}
	err := res.Contains(context.Background(), lookups)

	require.ErrorIs(t, err, resolver.ErrWaiting)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, []segment.State{segment.Unknown}, states(lookups), "the caller left at the timeout of the call")

	<-store.entered

	require.Eventually(t, func() bool { return res.Status().Overdue == 1 }, eventuallyWait, eventuallyTick)
	assert.Equal(t, 1, res.Status().Running)

	ended, cancel := context.WithCancel(context.Background())
	cancel()

	shutdownErr := res.Shutdown(ended)

	require.ErrorIs(t, shutdownErr, resolver.ErrShutdown)
	assert.Equal(
		t,
		"resolver: shutdown ended before the calls in flight: 1 calls still running: context canceled",
		shutdownErr.Error(),
	)

	close(store.gate) // the store answers at last

	require.NoError(t, res.Shutdown(context.Background()))
	assert.Equal(t, 0, res.Status().Overdue)
	assert.Equal(t, 0, res.Status().Running)
}

func TestShutdownIsIdempotent(t *testing.T) {
	t.Parallel()

	full := &lifecycleStore{fakeStore: betaStore()}
	res := newResolver(t, full, &resolver.Options{})

	require.NoError(t, res.Shutdown(context.Background()))
	require.NoError(t, res.Shutdown(context.Background()))
	assert.Equal(t, []string{"shutdown"}, full.events, "the store is shut down once")

	plain := newResolver(t, betaStore(), &resolver.Options{})

	require.NoError(t, plain.Shutdown(context.Background()))
	require.NoError(t, plain.Shutdown(context.Background()))
	assert.True(t, plain.Health().Closed)
}

func TestACallerThatIsGoneAnswersFromTheCacheOnly(t *testing.T) {
	t.Parallel()

	store := betaStore()
	res := newResolver(t, store, &resolver.Options{})

	require.NoError(t, res.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)}))
	require.Equal(t, [][]string{{callAlice}}, store.callList())

	gone, cancel := context.WithCancel(context.Background())
	cancel()

	for range 20 {
		lookups := []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, idZed)}

		err := res.Contains(gone, lookups)

		require.ErrorIs(t, err, resolver.ErrWaiting)
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(
			t,
			[]segment.State{segment.Member, segment.Unknown},
			states(lookups),
			"the cache answers, nothing else",
		)
	}

	assert.Equal(t, [][]string{{callAlice}}, store.callList(), "no call to the store for a caller that is gone")
	allCached := []segment.Lookup{lk(segBeta, idAlice)}
	require.NoError(t, res.Contains(gone, allCached), "a call that the cache answers entirely has nothing to wait for")
	assert.Equal(t, []segment.State{segment.Member}, states(allCached))
}

func TestASegmentThatIsNotLoadedIsNotAskedAgainForAWhile(t *testing.T) {
	t.Parallel()

	store := existsStore{&fakeStore{data: members(map[string][]string{segOther: {idAlice}})}} // beta: not loaded
	rec := &segRecorder{}
	res := newResolver(t, store, &resolver.Options{
		Observer: rec,
		Segments: map[string]resolver.SegmentOptions{segBeta: {BreakerFailures: 1}},
	})

	for range 3 {
		lookups := []segment.Lookup{lk(segBeta, idAlice)}

		require.NoError(t, res.Contains(context.Background(), lookups))
		assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))
	}

	assert.Equal(
		t,
		[][]string{{callAlice}},
		store.callList(),
		"one call, then the memory of the segment that is not loaded",
	)
	assert.Equal(t, resolver.SegmentStatus{NotLoaded: 2}, res.Status().Segments[segBeta], "the breaker is untouched")
	assert.Equal(t, uint64(2), rec.Snapshot().NotLoaded)

	res.Invalidate(segBeta)
	store.setData(members(map[string][]string{segBeta: {idAlice}}))

	loaded := []segment.Lookup{lk(segBeta, idAlice)}
	require.NoError(t, res.Contains(context.Background(), loaded))
	assert.Equal(t, []segment.State{segment.Member}, states(loaded), "Invalidate clears the memory")
	assert.Len(t, store.callList(), 2)
}

func TestObservationsPerSegment(t *testing.T) {
	t.Parallel()

	store := &fakeStore{
		data:         members(map[string][]string{segBeta: {idAlice}, segGamma: {}}),
		failSegments: map[string]bool{"delta": true},
	}
	rec := &segRecorder{}
	res := newResolver(t, store, &resolver.Options{Observer: rec, MaxIDBytes: 6})

	require.NoError(t, res.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)}))

	lookups := []segment.Lookup{
		lk(
			segBeta,
			idAlice,
		),
		lk(segBeta, idZed),
		lk(segGamma, idAlice),
		lk(segBeta, "much-too-long"),
		lk(segBeta, idZed),
	}

	require.NoError(t, res.Contains(context.Background(), lookups))

	calls, segs := rec.observed()

	require.Len(t, calls, 2)
	assert.Equal(t, resolver.CallObservation{
		Lookups: 5, Hits: 1, Misses: 2, Coalesced: 1, Oversized: 1, Unknown: 1, Duration: calls[1].Duration,
	}, calls[1])
	assert.Equal(t, []resolver.SegmentObservation{
		{Segment: segBeta, Lookups: 1, Hits: 0, Misses: 1},
		{Segment: segBeta, Lookups: 4, Hits: 1, Misses: 1, Coalesced: 1, Unknown: 1, Failure: resolver.ReasonOversized},
		{Segment: segGamma, Lookups: 1, Misses: 1},
	}, segs)
}

func TestObserverSeesTheReasons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		store      storeUnderTest
		opts       *resolver.Options
		prepare    func(*resolver.Resolver)
		name       string
		wantReason resolver.Reason
	}{
		{name: "a failing store", store: &fakeStore{fail: errStore}, wantReason: resolver.ReasonStore},
		{
			name: "an open breaker", store: &fakeStore{fail: errStore},
			opts: &resolver.Options{Default: resolver.SegmentOptions{BreakerFailures: 1}},
			prepare: func(res *resolver.Resolver) {
				_ = res.Contains(context.Background(), []segment.Lookup{lk(segBeta, "warm")})
			},
			wantReason: resolver.ReasonCircuitOpen,
		},
		{
			name: "a segment that is not loaded", store: existsStore{&fakeStore{data: members(map[string][]string{})}},
			prepare: func(res *resolver.Resolver) {
				_ = res.Contains(context.Background(), []segment.Lookup{lk(segBeta, "warm")})
			},
			wantReason: resolver.ReasonNotLoaded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := &segRecorder{}
			opts := &resolver.Options{}

			if tt.opts != nil {
				*opts = *tt.opts
			}

			opts.Observer = rec

			res := newResolver(t, tt.store, opts)

			if tt.prepare != nil {
				tt.prepare(res)
			}

			_ = res.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)})

			_, segs := rec.observed()
			require.NotEmpty(t, segs)
			assert.Equal(t, tt.wantReason, segs[len(segs)-1].Failure)
		})
	}

	assert.Equal(t, "none", resolver.ReasonNone.String())
	assert.Equal(t, "store", resolver.ReasonStore.String())
	assert.Equal(t, "circuit_open", resolver.ReasonCircuitOpen.String())
	assert.Equal(t, "waiting", resolver.ReasonWaiting.String())
	assert.Equal(t, "not_loaded", resolver.ReasonNotLoaded.String())
	assert.Equal(t, "oversized", resolver.ReasonOversized.String())
	assert.Equal(t, "invalid", resolver.ReasonInvalid.String())
	assert.Equal(t, "invalid_reason", resolver.Reason(99).String())
}

func TestNoSecretEverAppears(t *testing.T) {
	t.Parallel()

	secrets := []string{"hunter2", "10.9.8.7", secretID, "semaphore:segment:{vault}", "AKIA-secret"}
	leaky := errLeaky

	var all []string

	collect := func(label string, value any) {
		all = append(all, label+": "+fmt.Sprintf("%v %+v %#v", value, value, value))
	}

	rec := &segRecorder{}
	segs := map[string]resolver.SegmentOptions{
		"vault": {BreakerFailures: 1, Normalize: resolver.NormalizeTrimLower},
	}

	tests := []struct {
		store storeUnderTest
		pre   func(*resolver.Resolver)
		name  string
		ids   []string
	}{
		{name: "the store fails", store: &fakeStore{fail: leaky}, ids: []string{secretID}},
		{name: "the store panics", store: &fakeStore{panicValue: leaky}, ids: []string{secretID}},
		{name: "oversized", store: &fakeStore{}, ids: []string{strings.Repeat(secretID, 3)}},
		{name: "not UTF-8", store: &fakeStore{}, ids: []string{"secret-id-42\xff"}},
		{
			name: "breaker open", store: &fakeStore{fail: leaky}, ids: []string{"secret-id-43"},
			pre: func(res *resolver.Resolver) {
				_ = res.Contains(context.Background(), []segment.Lookup{lk("vault", secretID)})
			},
		},
		{
			name: "closed", store: &fakeStore{}, ids: []string{secretID},
			pre: func(res *resolver.Resolver) { _ = res.Shutdown(context.Background()) },
		},
	}

	for _, tt := range tests {
		res := newResolver(
			t,
			tt.store,
			&resolver.Options{Observer: rec, MaxIDBytes: 20, Segments: segs},
		)

		if tt.pre != nil {
			tt.pre(res)
		}

		lookups := make([]segment.Lookup, len(tt.ids))
		for i, id := range tt.ids {
			lookups[i] = lk("vault", id)
		}

		err := res.Contains(context.Background(), lookups)

		collect(tt.name+" error", err)
		collect(tt.name+" lookups", states(lookups))
		collect(tt.name+" status", res.Status())
		collect(tt.name+" health", res.Health())
	}

	calls, segObs := rec.observed()
	collect("calls", calls)
	collect("segments", segObs)
	collect("stats", rec.Snapshot())

	for _, text := range all {
		for _, secret := range secrets {
			assert.NotContains(t, text, secret)
		}
	}

	var storeErr *resolver.StoreError

	res := newResolver(t, &fakeStore{fail: leaky}, &resolver.Options{})
	err := res.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)})

	require.ErrorAs(t, err, &storeErr)
	require.ErrorIs(t, err, leaky, "the caller can still reach the error of the store")
	require.ErrorIs(t, storeErr.Err, leaky)
}

const (
	secretID = "secret-id-42"
	segGamma = "gamma"
	segOther = "other"
)

var errLeaky = errors.New(
	"dial tcp 10.9.8.7:6379: AUTH failed password=hunter2 key=semaphore:segment:{vault} id=secret-id-42 AKIA-secret",
)

// awaitEntered waits for a call to reach the store. A caller that joined another call never gets there: the gate is
// opened so that the test can end, and the test fails.
func awaitEntered(t *testing.T, entered <-chan struct{}, gate chan struct{}) {
	t.Helper()

	select {
	case <-entered:
	case <-time.After(leaveBound):
		close(gate)

		require.FailNow(t, "the caller did not make a call of its own")
	}
}

// answersThenPanics sets an answer and then panics: the engine does not trust a provider that panicked.
type answersThenPanics struct{}

func (answersThenPanics) Contains(_ context.Context, lookups []segment.Lookup) error {
	lookups[0].State = segment.Member

	panic(errBoom)
}

func TestWhatAStoreSetBeforeItPanickedIsNotTrusted(t *testing.T) {
	t.Parallel()

	res := newResolver(t, answersThenPanics{}, &resolver.Options{})
	lookups := []segment.Lookup{lk(segBeta, idAlice)}

	err := res.Contains(context.Background(), lookups)

	require.ErrorIs(t, err, resolver.ErrProviderPanic)
	assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))
	assert.Equal(t, 0, res.Status().Segments[segBeta].Entries, "nothing of it was cached")
}

func TestAnUnansweredLookupWithoutAnExistencerSaysNothingAboutTheSegment(t *testing.T) {
	t.Parallel()

	store := &fakeStore{
		data: members(map[string][]string{segOther: {idAlice}}),
	} // the store cannot tell, and cannot say why
	rec := &segRecorder{}
	res := newResolver(t, store, &resolver.Options{Observer: rec})

	for range 2 {
		lookups := []segment.Lookup{lk(segBeta, idAlice)}

		require.NoError(t, res.Contains(context.Background(), lookups))
		assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))
	}

	assert.Equal(
		t,
		[][]string{{callAlice}, {callAlice}},
		store.callList(),
		"asked again: nothing says it is not loaded",
	)
	assert.Equal(t, uint64(0), rec.Snapshot().NotLoaded)

	_, segs := rec.observed()
	assert.Equal(t, resolver.ReasonUnanswered, segs[len(segs)-1].Failure, "an unanswered lookup has a reason")
	assert.Equal(t, "unanswered", resolver.ReasonUnanswered.String())
}

// leakyLifecycle is a store whose Init and Exists fail with a message that holds secrets.
type leakyLifecycle struct {
	*fakeStore
}

func (leakyLifecycle) Init(context.Context) error { return errLeaky }

func (leakyLifecycle) Exists(context.Context, []string) ([]segment.State, error) {
	return nil, errLeaky
}

func TestInitAndExistsErrorsOfTheStoreDoNotShowTheirText(t *testing.T) {
	t.Parallel()

	res := newResolver(t, leakyLifecycle{betaStore()}, &resolver.Options{})

	initErr := res.Init(context.Background())
	_, existsErr := res.Exists(context.Background(), []string{segBeta})

	for name, err := range map[string]error{"Init": initErr, "Exists": existsErr} {
		require.Error(t, err, name)
		require.ErrorIs(t, err, errLeaky, name+": the error of the store is still reachable")
		assert.Equal(t, "resolver: the call to the store failed", err.Error(), name)
	}
}

func TestAHugeMaxIDBytesDoesNotOverflow(t *testing.T) {
	t.Parallel()

	store := betaStore()
	res := newResolver(t, store, &resolver.Options{MaxIDBytes: math.MaxInt})
	lookups := []segment.Lookup{lk(segBeta, idAlice)}

	require.NoError(t, res.Contains(context.Background(), lookups))
	assert.Equal(t, []segment.State{segment.Member}, states(lookups))
	assert.Equal(t, [][]string{{callAlice}}, store.callList())
}

// blockingShutdown is a store whose Shutdown waits for a gate.
type blockingShutdown struct {
	*fakeStore

	entered chan struct{}
	release chan struct{}
}

func (b blockingShutdown) Shutdown(context.Context) error {
	b.entered <- struct{}{}

	<-b.release

	return nil
}

func TestEveryShutdownCallerIsBoundedByItsOwnContext(t *testing.T) {
	t.Parallel()

	store := blockingShutdown{fakeStore: betaStore(), entered: make(chan struct{}, 1), release: make(chan struct{})}
	res := newResolver(t, store, &resolver.Options{})
	t.Cleanup(func() { close(store.release) })

	first := make(chan error, 1)

	go func() { first <- res.Shutdown(context.Background()) }()

	select {
	case <-store.entered: // the first caller is inside the Shutdown of the store
	case <-time.After(eventuallyWait):
		require.Fail(t, "the Shutdown of the store was not called")
	}

	ended, cancel := context.WithCancel(context.Background())
	cancel()

	second := make(chan error, 1)

	go func() { second <- res.Shutdown(ended) }()

	select {
	case err := <-second:
		require.ErrorIs(t, err, resolver.ErrShutdown)
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(eventuallyWait):
		require.Fail(t, "a second Shutdown was held by the first one past its own context")
	}

	select {
	case <-first:
		require.Fail(t, "the first Shutdown returned before the store did")
	default:
	}
}
