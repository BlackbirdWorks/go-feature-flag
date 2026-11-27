package resolver

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/clock"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// baseTime is a fixed instant of the tests of the breaker and of the cache.
func baseTime() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }

func TestBreakerStateMachine(t *testing.T) {
	t.Parallel()

	type op struct {
		kind        string
		at          time.Duration
		wantFails   int
		result      outcome
		stale       bool
		wantAllowed bool
		wantFrom    BreakerState
		wantTo      BreakerState
		wantChanged bool
		wantState   BreakerState
		wantProbing bool
	}

	fail := outcomeFailure
	ok := outcomeSuccess
	neutral := outcomeNeutral

	opened := op{
		kind:        opRecord,
		result:      fail,
		wantFrom:    BreakerClosed,
		wantTo:      BreakerOpen,
		wantChanged: true,
		wantState:   BreakerOpen,
		wantFails:   2,
	}
	first := op{kind: opRecord, result: fail, wantState: BreakerClosed, wantFails: 1}
	probe := op{
		kind:        opAllow,
		at:          10 * time.Second,
		wantAllowed: true,
		wantFrom:    BreakerOpen,
		wantTo:      BreakerHalfOpen,
		wantChanged: true,
		wantState:   BreakerHalfOpen,
		wantFails:   2,
		wantProbing: true,
	}

	tests := []struct {
		name string
		ops  []op
	}{
		{
			name: "closed counts consecutive failures and opens at the threshold",
			ops: []op{
				first,
				{kind: opRecord, result: ok, at: 1, wantState: BreakerClosed, wantFails: 0},
				first,
				opened,
			},
		},
		{
			name: "a neutral result neither counts nor resets",
			ops: []op{
				first,
				{kind: opRecord, result: neutral, wantState: BreakerClosed, wantFails: 1},
				opened,
			},
		},
		{
			name: "open refuses until the cool-down, then lets exactly one probe through",
			ops: []op{
				first, opened,
				{kind: opAllow, at: 5 * time.Second, wantState: BreakerOpen, wantFails: 2},
				{kind: opAllow, at: 10*time.Second - 1, wantState: BreakerOpen, wantFails: 2},
				probe,
				{kind: opAllow, at: 10 * time.Second, wantState: BreakerHalfOpen, wantFails: 2, wantProbing: true},
			},
		},
		{
			name: "a successful probe closes the breaker and resets the count",
			ops: []op{
				first,
				opened,
				probe,
				{
					kind:        opRecord,
					result:      ok,
					at:          11 * time.Second,
					wantFrom:    BreakerHalfOpen,
					wantTo:      BreakerClosed,
					wantChanged: true,
					wantState:   BreakerClosed,
					wantFails:   0,
				},
				{kind: opAllow, at: 11 * time.Second, wantAllowed: true, wantState: BreakerClosed, wantFails: 0},
			},
		},
		{
			name: "a failed probe reopens the breaker for a whole cool-down",
			ops: []op{
				first,
				opened,
				probe,
				{
					kind:        opRecord,
					result:      fail,
					at:          12 * time.Second,
					wantFrom:    BreakerHalfOpen,
					wantTo:      BreakerOpen,
					wantChanged: true,
					wantState:   BreakerOpen,
					wantFails:   2,
				},
				{kind: opAllow, at: 21 * time.Second, wantState: BreakerOpen, wantFails: 2},
				{
					kind:        opAllow,
					at:          22 * time.Second,
					wantAllowed: true,
					wantFrom:    BreakerOpen,
					wantTo:      BreakerHalfOpen,
					wantChanged: true,
					wantState:   BreakerHalfOpen,
					wantFails:   2,
					wantProbing: true,
				},
			},
		},
		{
			name: "a probe that learns nothing lets the next call probe again",
			ops: []op{
				first,
				opened,
				probe,
				{kind: opRecord, result: neutral, at: 11 * time.Second, wantState: BreakerHalfOpen, wantFails: 2},
				{
					kind:        opAllow,
					at:          11 * time.Second,
					wantAllowed: true,
					wantState:   BreakerHalfOpen,
					wantFails:   2,
					wantProbing: true,
				},
			},
		},
		{
			name: "a result of an older generation is not the result of the probe",
			ops: []op{
				first,
				opened,
				probe,
				{
					kind:        opRecord,
					result:      ok,
					at:          11 * time.Second,
					stale:       true,
					wantState:   BreakerHalfOpen,
					wantFails:   2,
					wantProbing: true,
				},
				{
					kind:        opRecord,
					result:      fail,
					at:          11 * time.Second,
					stale:       true,
					wantState:   BreakerHalfOpen,
					wantFails:   2,
					wantProbing: true,
				},
				{kind: opAllow, at: 11 * time.Second, wantState: BreakerHalfOpen, wantFails: 2, wantProbing: true},
			},
		},
		{
			name: "a result that arrives while the breaker is open changes nothing",
			ops: []op{
				first, opened,
				{kind: opRecord, result: ok, at: 1, stale: true, wantState: BreakerOpen, wantFails: 2},
				{kind: opRecord, result: fail, at: 1, stale: true, wantState: BreakerOpen, wantFails: 2},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			brk := newBreaker(SegmentOptions{BreakerFailures: 2, BreakerCooldown: 10 * time.Second})

			for i, step := range tt.ops {
				var (
					trans   transition
					allowed bool
				)

				if step.kind == opAllow {
					allowed, _, trans = brk.allow(baseTime().Add(step.at))
				} else {
					gen := brk.gen
					if step.stale {
						gen--
					}

					trans = brk.record(gen, step.result, baseTime().Add(step.at))
					allowed = step.wantAllowed
				}

				state, fails := brk.snapshot()

				label := "op " + strconv.Itoa(i)
				assert.Equal(t, step.wantAllowed, allowed, label)
				assert.Equal(
					t,
					transition{from: step.wantFrom, to: step.wantTo, changed: step.wantChanged},
					trans,
					label,
				)
				assert.Equal(t, step.wantState, state, label)
				assert.Equal(t, step.wantFails, fails, label)
				assert.Equal(t, step.wantProbing, brk.probing, label)
			}
		})
	}
}

func TestBreakerStateString(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "closed", BreakerClosed.String())
	assert.Equal(t, "open", BreakerOpen.String())
	assert.Equal(t, "half-open", BreakerHalfOpen.String())
	assert.Equal(t, "invalid", BreakerState(9).String())
}

func TestCacheTTLAndNegativeTTL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		at        time.Duration
		state     segment.State
		wantState segment.State
		wantHit   bool
	}{
		{
			name:      "member just before its TTL",
			state:     segment.Member,
			at:        30*time.Second - 1,
			wantState: segment.Member,
			wantHit:   true,
		},
		{name: "member at its TTL", state: segment.Member, at: 30 * time.Second, wantState: segment.Unknown},
		{
			name:      "not member just before its negative TTL",
			state:     segment.NotMember,
			at:        5*time.Second - 1,
			wantState: segment.NotMember,
			wantHit:   true,
		},
		{
			name:      "not member at its negative TTL",
			state:     segment.NotMember,
			at:        5 * time.Second,
			wantState: segment.Unknown,
		},
		{name: "unknown is never cached", state: segment.Unknown, at: 0, wantState: segment.Unknown},
		{name: "a state that is not valid is never cached", state: segment.State(9), at: 0, wantState: segment.Unknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cch := newCache(SegmentOptions{}.withDefaults())
			cch.put("k", tt.state, baseTime())

			got, hit := cch.get("k", baseTime().Add(tt.at))

			assert.Equal(t, tt.wantState, got)
			assert.Equal(t, tt.wantHit, hit)
		})
	}
}

func TestCacheEvictsTheLeastRecentlyUsed(t *testing.T) {
	t.Parallel()

	cch := newCache(SegmentOptions{MaxEntries: 2, Shards: 1, TTL: time.Hour, NegativeTTL: time.Hour})

	cch.put("a", segment.Member, baseTime())
	cch.put("b", segment.Member, baseTime())

	_, hit := cch.get("a", baseTime()) // a is now the most recent
	require.True(t, hit)

	cch.put("c", segment.NotMember, baseTime()) // evicts b

	_, hitA := cch.get("a", baseTime())
	_, hitB := cch.get("b", baseTime())
	state, hitC := cch.get("c", baseTime())

	assert.True(t, hitA)
	assert.False(t, hitB)
	assert.True(t, hitC)
	assert.Equal(t, segment.NotMember, state)
	assert.Equal(t, 2, cch.len())

	cch.put("a", segment.NotMember, baseTime()) // an update keeps one entry

	updated, _ := cch.get("a", baseTime())
	assert.Equal(t, segment.NotMember, updated)
	assert.Equal(t, 2, cch.len())

	cch.clear()
	assert.Equal(t, 0, cch.len())
}

func TestCacheBoundIsExact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		maxEntries, shards int
	}{
		{1, 16}, {7, 16}, {10, 3}, {16, 16}, {17, 16}, {100, 16}, {10000, 16}, {5, 1},
	}

	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.maxEntries)+" entries over "+strconv.Itoa(tt.shards)+" shards", func(t *testing.T) {
			t.Parallel()

			cch := newCache(
				SegmentOptions{MaxEntries: tt.maxEntries, Shards: tt.shards, TTL: time.Hour, NegativeTTL: time.Hour},
			)

			total := 0
			for _, shd := range cch.shards {
				total += shd.max
			}

			assert.Equal(t, tt.maxEntries, total, "the shards add up to the bound")
			assert.LessOrEqual(t, len(cch.shards), tt.maxEntries)

			for i := range 3 * tt.maxEntries {
				cch.put("key-"+strconv.Itoa(i), segment.Member, baseTime())
				require.LessOrEqual(t, cch.len(), tt.maxEntries)
			}

			for _, shd := range cch.shards {
				assert.LessOrEqual(t, len(shd.m), shd.max)
			}
		})
	}
}

func TestDefaults(t *testing.T) {
	t.Parallel()

	got := SegmentOptions{}.withDefaults()

	assert.Equal(t, SegmentOptions{
		Normalize: NormalizeNone, TTL: 30 * time.Second, NegativeTTL: 5 * time.Second, MaxEntries: 10000, Shards: 16,
		BreakerFailures: 5, BreakerCooldown: 10 * time.Second,
	}, got)
	assert.Equal(t, 2*time.Second, DefaultCallTimeout)

	custom := SegmentOptions{TTL: time.Minute, MaxEntries: 7}.withDefaults()
	assert.Equal(t, time.Minute, custom.TTL)
	assert.Equal(t, 7, custom.MaxEntries)
	assert.Equal(t, 5*time.Second, custom.NegativeTTL)
}

type internalStore struct {
	fail  error
	mu    sync.Mutex
	calls int
}

func (s *internalStore) Contains(_ context.Context, lookups []segment.Lookup) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()

	if s.fail != nil {
		return s.fail
	}

	for i := range lookups {
		lookups[i].State = segment.Member
	}

	return nil
}

func newInternal(t *testing.T, store segment.Provider, opts *Options) *Resolver {
	t.Helper()

	res, err := New(store, opts)
	require.NoError(t, err)

	return res
}

func TestResolverHonoursTheTTLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		at        time.Duration
		wantCalls int
	}{
		{name: "just before the TTL the cache answers", at: time.Minute - 1, wantCalls: 1},
		{name: "at the TTL the store is asked again", at: time.Minute, wantCalls: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &internalStore{}
			res := newInternal(t, store, &Options{Default: SegmentOptions{TTL: time.Minute}})
			now := clock.Now()
			lookups := func() []segment.Lookup { return []segment.Lookup{{Segment: testSeg, ID: "a"}} }

			require.NoError(t, res.containsAt(context.Background(), lookups(), now))

			next := lookups()
			require.NoError(t, res.containsAt(context.Background(), next, now.Add(tt.at)))
			assert.Equal(t, tt.wantCalls, store.calls)
			assert.Equal(t, segment.Member, next[0].State)
		})
	}
}

func TestResolverRecoversThroughTheProbe(t *testing.T) {
	t.Parallel()

	store := &internalStore{fail: errBoomInternal}
	counters := &Counters{}
	res := newInternal(t, store, &Options{
		Observer: counters,
		Default:  SegmentOptions{BreakerFailures: 1, BreakerCooldown: time.Minute, NoCache: true},
	})
	now := clock.Now() // the instant the shared call stamps the opening of the breaker

	lookups := func() []segment.Lookup { return []segment.Lookup{{Segment: testSeg, ID: "a"}} }

	require.ErrorIs(t, res.containsAt(context.Background(), lookups(), now), errBoomInternal)
	require.ErrorIs(t, res.containsAt(context.Background(), lookups(), now), ErrCircuitOpen)
	assert.Equal(t, 1, store.calls)

	store.fail = nil

	probe := lookups()
	require.NoError(t, res.containsAt(context.Background(), probe, now.Add(time.Minute)))
	assert.Equal(t, []segment.State{segment.Member}, []segment.State{probe[0].State})
	assert.Equal(t, 2, store.calls)
	assert.Equal(t, BreakerClosed, res.Status().Segments[testSeg].Breaker)

	stats := counters.Snapshot()
	assert.Equal(t, uint64(1), stats.BreakerOpened)
	assert.Equal(t, uint64(1), stats.BreakerHalfOpened)
	assert.Equal(t, uint64(1), stats.BreakerClosed)
}

func TestInvalidateDetachesTheFlightsInTheMap(t *testing.T) {
	t.Parallel()

	res := newInternal(t, &internalStore{}, &Options{})
	seg := res.state(testSeg)

	seg.cache.put("old", segment.Member, baseTime())

	stale := &flight{done: make(chan struct{}), epoch: seg.epoch.Load()}
	seg.flights["k"] = stale

	res.Invalidate(testSeg)
	assert.Equal(t, 0, seg.cache.len())
	assert.Empty(t, seg.flights, "the flight is detached: a caller after Invalidate does not join it")

	fresh := &flight{done: make(chan struct{}), epoch: seg.epoch.Load()}
	seg.flights["k"] = fresh

	res.wg.Add(1)
	res.inflight.Add(1)
	res.run(newCall(context.Background(), time.Minute), []leader{{sst: seg, flt: stale, key: "k"}})

	<-stale.done

	assert.Equal(t, segment.Member, stale.state, "the caller of the old flight still gets its answer")
	assert.Equal(t, 0, seg.cache.len(), "but a stale answer is not cached")
	assert.Same(t, fresh, seg.flights["k"], "and it does not remove the flight that replaced it")
}

func TestUnlistedSegmentGetsTheDefaults(t *testing.T) {
	t.Parallel()

	res := newInternal(t, &internalStore{}, &Options{Default: SegmentOptions{MaxEntries: 3}})
	first := res.state("new")

	assert.Equal(t, 3, first.opts.MaxEntries)
	assert.Same(t, first, res.state("new"))
}

const (
	opAllow  = "allow"
	opRecord = "record"
)

const (
	testSeg   = "beta"
	waitBound = 10 * time.Second // a bound for a wait that must not hang, only used when something is wrong
)

var errBoomInternal = errors.New("boom")

func TestCallTimeoutDefault(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 2*time.Second, newInternal(t, &internalStore{}, nil).timeout)
	assert.Equal(t, time.Second, newInternal(t, &internalStore{}, &Options{CallTimeout: time.Second}).timeout)
}

// signalCtx is a context whose Done is observed: the test learns when someone starts to wait on it.
type signalCtx struct {
	context.Context //nolint:containedctx // a double of context.Context that wraps one

	waiting chan struct{}
	once    sync.Once
}

func (c *signalCtx) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })

	return c.Context.Done()
}

func TestOneFlightTimingOutDoesNotCutTheOthersShort(t *testing.T) {
	t.Parallel()

	expired, cancelExpired := context.WithCancel(context.Background())
	cancelExpired() // the call of the first flight is past its bound, and its store has not answered

	live := &signalCtx{Context: context.Background(), waiting: make(chan struct{})}
	slow := &flight{done: make(chan struct{}), call: &call{ctx: expired}}
	late := &flight{done: make(chan struct{}), call: &call{ctx: live}, state: segment.Member}
	seg := &segState{name: testSeg}

	pend := []pending{{sst: seg, flt: slow, idx: 0}, {sst: seg, flt: late, idx: 1}}
	lookups := []segment.Lookup{{Segment: testSeg, ID: "a"}, {Segment: testSeg, ID: "b"}}

	var accs segAccs

	// the worker of the test ends with the test, whatever happens
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	result := make(chan error, 1)

	go func() { result <- waitFor(ctx, lookups, pend, &accs) }()

	select {
	case <-live.waiting: // waitFor passed the first flight and now waits for the second
	case err := <-result:
		require.Fail(t, "waitFor returned before the second flight had its own wait", "%v", err)
	case <-time.After(waitBound):
		require.Fail(t, "waitFor never reached the second flight")
	}

	close(late.done) // the second answers late, inside its bound

	var err error

	select {
	case err = <-result:
	case <-time.After(waitBound):
		require.Fail(t, "waitFor did not return the answer of the second flight")
	}

	require.ErrorIs(t, err, ErrWaiting)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(
		t,
		[]segment.State{segment.Unknown, segment.Member},
		[]segment.State{lookups[0].State, lookups[1].State},
	)
}

func TestRegisterLooksAtTheCacheAgainUnderTheLock(t *testing.T) {
	t.Parallel()

	store := &internalStore{}
	res := newInternal(t, store, &Options{})
	seg := res.state(testSeg)
	now := baseTime()

	// the first look missed; the answer is filled before the lookup is registered
	seg.cache.put("a", segment.Member, now)

	pend := []pending{{sst: seg, key: "a", idx: 0}}

	var (
		obs  CallObservation
		accs segAccs
	)

	shared, refusal := res.register(context.Background(), pend, now, &obs, &accs)

	require.NoError(t, refusal)
	assert.Nil(t, shared, "no call is made for an answer that is in the cache")
	assert.Equal(t, pending{sst: seg, key: "a", hit: true, state: segment.Member}, pend[0])
	assert.Equal(t, CallObservation{Hits: 1}, obs)
	assert.Empty(t, seg.flights)
	assert.Equal(t, 0, store.calls)
}

func TestAReplyInTimeIsNotOverdueWhateverTheTimerDoes(t *testing.T) {
	t.Parallel()

	store := &internalStore{}
	res := newInternal(t, store, &Options{
		Default: SegmentOptions{BreakerFailures: 1, NoCache: true},
	})

	// the deadline fires after the store returned and before the worker stops the timer
	res.beforeStop = func(shared *call) { shared.cancel() }

	first := []segment.Lookup{{Segment: testSeg, ID: "a"}}

	// the caller may leave at the cancelled deadline: the call it started is what the test looks at
	_ = res.Contains(context.Background(), first)

	require.Eventually(t, func() bool { return res.Status().Running == 0 }, 10*time.Second, time.Millisecond)

	status := res.Status()

	assert.Equal(t, 0, status.Overdue, "a call that returned in time is not overdue")
	assert.Equal(t, SegmentStatus{Breaker: BreakerClosed}, status.Segments[testSeg], "no failure was counted")

	// the next uncached lookup is not refused by the breaker: it makes its own call (the hook cancels this one too)
	second := []segment.Lookup{{Segment: testSeg, ID: "b"}}
	err := res.Contains(context.Background(), second)

	require.NotErrorIs(t, err, ErrCircuitOpen)
	require.Eventually(t, func() bool { return res.Status().Running == 0 }, 10*time.Second, time.Millisecond)

	store.mu.Lock()
	defer store.mu.Unlock()

	assert.Equal(t, 2, store.calls, "the next uncached lookup reaches the store")
}

func TestTheDeadlineBeforeTheReplyDecidesOnceAndTheLateReplyCountsForNothing(t *testing.T) {
	t.Parallel()

	res := newInternal(t, &internalStore{}, &Options{Default: SegmentOptions{BreakerFailures: 2}})
	seg := res.state(testSeg)
	leaders := []leader{{sst: seg, key: "a"}}

	// the deadline took the lock first: it records the failure, then the counter
	res.settleOverdue(leaders, baseTime())
	res.overdue.Add(1)

	// the reply that comes after is late: it must not be taken for a success
	res.settle(leaders, []segment.Lookup{{Segment: testSeg, ID: "a", State: segment.Member}}, nil, baseTime(), true)

	status, failures := seg.brk.snapshot()

	assert.Equal(t, BreakerClosed, status)
	assert.Equal(t, 1, failures, "one failure, counted once, when it became overdue")
}

func TestWaitForReportsTheStoreErrorAndTheTimeoutWhateverTheOrder(t *testing.T) {
	t.Parallel()

	storeErr := &StoreError{Err: errBoomInternal}

	tests := []struct {
		name      string
		wantState []segment.State
		failedAt  int
	}{
		{name: "the store error first", failedAt: 0, wantState: []segment.State{segment.Unknown, segment.Unknown}},
		{name: "the timeout first", failedAt: 1, wantState: []segment.State{segment.Unknown, segment.Unknown}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expired, cancel := context.WithCancel(context.Background())
			cancel()

			failed := &flight{done: make(chan struct{}), call: &call{ctx: context.Background()}, err: storeErr}
			close(failed.done)

			timedOut := &flight{done: make(chan struct{}), call: &call{ctx: expired}}
			seg := &segState{name: testSeg}

			flights := []*flight{timedOut, timedOut}
			flights[tt.failedAt] = failed

			pend := []pending{{sst: seg, flt: flights[0], idx: 0}, {sst: seg, flt: flights[1], idx: 1}}
			lookups := []segment.Lookup{{Segment: testSeg, ID: "a"}, {Segment: testSeg, ID: "b"}}

			var accs segAccs

			err := waitFor(context.Background(), lookups, pend, &accs)

			require.ErrorIs(t, err, errBoomInternal, "the store error")
			require.ErrorIs(t, err, ErrWaiting, "the timeout")
			require.ErrorIs(t, err, context.Canceled)
			assert.Equal(t, tt.wantState, []segment.State{lookups[0].State, lookups[1].State})
		})
	}
}

// stuckThenOK blocks its first call (it ignores its context) and answers the next ones.
type stuckThenOK struct {
	release chan struct{}
	entered chan struct{}
	mu      sync.Mutex
	calls   int
}

func (s *stuckThenOK) Contains(_ context.Context, lookups []segment.Lookup) error {
	s.mu.Lock()
	s.calls++
	first := s.calls == 1
	s.mu.Unlock()

	if first {
		s.entered <- struct{}{}

		<-s.release

		return nil
	}

	for i := range lookups {
		lookups[i].State = segment.Member
	}

	return nil
}

func TestALookupDoesNotJoinAFlightThatIsPastItsDeadline(t *testing.T) {
	t.Parallel()

	store := &stuckThenOK{release: make(chan struct{}), entered: make(chan struct{}, 2)}

	t.Cleanup(func() { close(store.release) })

	res := newInternal(t, store, &Options{
		MaxOverdue: 100,
		Default:    SegmentOptions{BreakerFailures: 1, BreakerCooldown: time.Minute, NoCache: true},
	})
	seg := res.state(testSeg)
	now := baseTime()

	// the first lookup makes a call that never returns; its caller leaves when the deadline passes
	first := []segment.Lookup{{Segment: testSeg, ID: "alice"}}
	done := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() { done <- res.containsAt(ctx, first, now) }()

	select {
	case <-store.entered:
	case <-time.After(waitBound):
		require.Fail(t, "the first call never reached the store")
	}

	seg.mu.Lock()
	stuck := seg.flights["alice"]
	seg.mu.Unlock()

	require.NotNil(t, stuck)

	stuck.call.cancel() // the shared deadline passes: the breaker hears of it, the call is overdue

	require.Eventually(t, func() bool { return res.Status().Overdue == 1 }, waitBound, time.Millisecond)

	select {
	case err := <-done:
		require.ErrorIs(t, err, ErrWaiting)
	case <-time.After(waitBound):
		require.Fail(t, "the caller of the stuck call did not leave")
	}

	// after the cool-down the store has recovered: the next lookup of the same id starts a call of its own
	retry := []segment.Lookup{{Segment: testSeg, ID: "alice"}}
	require.NoError(t, res.containsAt(context.Background(), retry, now.Add(time.Minute)))
	assert.Equal(t, []segment.State{segment.Member}, []segment.State{retry[0].State})

	store.mu.Lock()
	defer store.mu.Unlock()

	assert.Equal(t, 2, store.calls, "the retry reached the store instead of joining the stuck flight")
}

func TestTheErrorOfALookupCarriesEveryCause(t *testing.T) {
	t.Parallel()

	storeErr := &StoreError{Err: errBoomInternal}

	// the three lookups: 0 failed with an error of the store, 1 timed out, 2 is still waiting when the caller leaves
	permutations := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}

	for _, order := range permutations {
		t.Run(fmt.Sprint(order), func(t *testing.T) {
			t.Parallel()

			timedOut, cancelTimedOut := context.WithTimeout(context.Background(), 0)
			t.Cleanup(cancelTimedOut)

			failed := &flight{done: make(chan struct{}), call: &call{ctx: context.Background()}, err: storeErr}
			close(failed.done)

			flights := map[int]*flight{
				0: failed,
				1: {done: make(chan struct{}), call: &call{ctx: timedOut}},
				2: {done: make(chan struct{}), call: &call{ctx: context.Background()}},
			}

			seg := &segState{name: testSeg}
			pend := make([]pending, len(order))
			lookups := make([]segment.Lookup, len(order))

			for position, which := range order {
				pend[position] = pending{sst: seg, flt: flights[which], idx: position}
				lookups[position] = segment.Lookup{Segment: testSeg, ID: strconv.Itoa(which)}
			}

			gone, cancel := context.WithCancel(context.Background())
			cancel() // the caller is gone

			var accs segAccs

			err := waitFor(gone, lookups, pend, &accs)

			require.ErrorIs(t, err, errBoomInternal, "the store error of the flight that completed")
			require.ErrorIs(t, err, ErrWaiting)
			require.ErrorIs(t, err, context.DeadlineExceeded, "the timeout of the flight that expired")
			require.ErrorIs(t, err, context.Canceled, "the cancellation of the caller")
		})
	}
}

func TestADetachedCallWritesNothingToTheCache(t *testing.T) {
	t.Parallel()

	store := &internalStore{} // every lookup is a member
	res := newInternal(t, store, &Options{})
	seg := res.state(testSeg)

	// the call below was detached when its deadline expired; a retry cached a fresher answer, finished, and left
	// the map without a flight for the id
	stale := &flight{done: make(chan struct{}), epoch: seg.epoch.Load()}

	seg.cache.put("alice", segment.NotMember, baseTime())

	res.wg.Add(1)
	res.inflight.Add(1)
	res.run(newCall(context.Background(), time.Minute), []leader{{sst: seg, flt: stale, key: "alice"}})

	<-stale.done

	assert.Equal(t, segment.Member, stale.state, "its own waiters get its answer")

	state, hit := seg.cache.get("alice", baseTime())

	assert.True(t, hit)
	assert.Equal(t, segment.NotMember, state, "but the fresher answer of the retry stays in the cache")
}

// stuckExists is a store whose Exists ignores its deadline until its gate opens.
type stuckExists struct {
	internalStore

	gate    chan struct{}
	entered chan struct{}
}

func (s *stuckExists) Exists(_ context.Context, names []string) ([]segment.State, error) {
	s.entered <- struct{}{}

	<-s.gate

	return make([]segment.State, len(names)), nil
}

func TestAStuckExistsCountsAsOverdueAndTripsTheThreshold(t *testing.T) {
	t.Parallel()

	store := &stuckExists{gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	res := newInternal(t, store, &Options{MaxOverdue: 1, Default: SegmentOptions{NoCache: true, NoBreaker: true}})
	res.timeout = time.Nanosecond // the deadline of the check has passed when it starts

	seg := res.state(testSeg)
	neutral := []*segmentResult{{sst: seg, flt: &flight{epoch: seg.epoch.Load()}}}
	finished := make(chan struct{})

	go func() {
		defer close(finished)

		res.confirmNotLoaded(context.Background(), neutral, baseTime())
	}()

	release := func() {
		select {
		case <-store.gate:
		default:
			close(store.gate)
		}
	}

	t.Cleanup(release)

	select {
	case <-store.entered:
	case <-time.After(waitBound):
		require.Fail(t, "the check never reached the store")
	}

	require.Eventually(t, func() bool { return res.Status().Overdue == 1 }, waitBound, time.Millisecond)

	// the threshold is reached: a lookup that needs a call is refused, and the store is not called
	lookups := []segment.Lookup{{Segment: testSeg, ID: "alice"}}
	err := res.containsAt(context.Background(), lookups, baseTime())

	require.ErrorIs(t, err, ErrOverdue)
	assert.Equal(t, []segment.State{segment.Unknown}, []segment.State{lookups[0].State})
	assert.Equal(t, 0, store.calls)

	release()

	select {
	case <-finished:
	case <-time.After(waitBound):
		require.Fail(t, "the check did not return once the store did")
	}

	assert.Equal(t, 0, res.Status().Overdue, "the check is not overdue any more")
	assert.True(t, seg.notLoadedUntil.IsZero(), "an answer that came after the deadline does not count")
}
