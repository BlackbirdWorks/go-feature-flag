package resolver

import (
	"context"
	"errors"
	"strconv"
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
		kind    string // opAllow or opRecord
		at      time.Duration
		success bool
		// wants
		wantAllowed bool
		wantFrom    BreakerState
		wantTo      BreakerState
		wantChanged bool
		wantState   BreakerState
		wantFails   int
	}

	tests := []struct {
		name string
		ops  []op
	}{
		{
			name: "closed counts consecutive failures and opens at the threshold",
			ops: []op{
				{kind: opRecord, at: 0, success: false, wantState: BreakerClosed, wantFails: 1},
				{kind: opRecord, at: 1, success: true, wantState: BreakerClosed, wantFails: 0},
				{kind: opRecord, at: 2, success: false, wantState: BreakerClosed, wantFails: 1},
				{
					kind:        opRecord,
					at:          3,
					success:     false,
					wantFrom:    BreakerClosed,
					wantTo:      BreakerOpen,
					wantChanged: true,
					wantState:   BreakerOpen,
					wantFails:   2,
				},
			},
		},
		{
			name: "open refuses until the cool-down, then lets exactly one probe through",
			ops: []op{
				{kind: opRecord, at: 0, success: false, wantState: BreakerClosed, wantFails: 1},
				{
					kind:        opRecord,
					at:          0,
					success:     false,
					wantFrom:    BreakerClosed,
					wantTo:      BreakerOpen,
					wantChanged: true,
					wantState:   BreakerOpen,
					wantFails:   2,
				},
				{kind: opAllow, at: 5 * time.Second, wantAllowed: false, wantState: BreakerOpen, wantFails: 2},
				{kind: opAllow, at: 10*time.Second - 1, wantAllowed: false, wantState: BreakerOpen, wantFails: 2},
				{
					kind:        opAllow,
					at:          10 * time.Second,
					wantAllowed: true,
					wantFrom:    BreakerOpen,
					wantTo:      BreakerHalfOpen,
					wantChanged: true,
					wantState:   BreakerHalfOpen,
					wantFails:   2,
				},
				{kind: opAllow, at: 10 * time.Second, wantAllowed: false, wantState: BreakerHalfOpen, wantFails: 2},
			},
		},
		{
			name: "a successful probe closes the breaker and resets the count",
			ops: []op{
				{kind: opRecord, at: 0, success: false, wantState: BreakerClosed, wantFails: 1},
				{
					kind:        opRecord,
					at:          0,
					success:     false,
					wantFrom:    BreakerClosed,
					wantTo:      BreakerOpen,
					wantChanged: true,
					wantState:   BreakerOpen,
					wantFails:   2,
				},
				{
					kind:        opAllow,
					at:          10 * time.Second,
					wantAllowed: true,
					wantFrom:    BreakerOpen,
					wantTo:      BreakerHalfOpen,
					wantChanged: true,
					wantState:   BreakerHalfOpen,
					wantFails:   2,
				},
				{
					kind:        opRecord,
					at:          11 * time.Second,
					success:     true,
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
				{kind: opRecord, at: 0, success: false, wantState: BreakerClosed, wantFails: 1},
				{
					kind:        opRecord,
					at:          0,
					success:     false,
					wantFrom:    BreakerClosed,
					wantTo:      BreakerOpen,
					wantChanged: true,
					wantState:   BreakerOpen,
					wantFails:   2,
				},
				{
					kind:        opAllow,
					at:          10 * time.Second,
					wantAllowed: true,
					wantFrom:    BreakerOpen,
					wantTo:      BreakerHalfOpen,
					wantChanged: true,
					wantState:   BreakerHalfOpen,
					wantFails:   2,
				},
				{
					kind:        opRecord,
					at:          12 * time.Second,
					success:     false,
					wantFrom:    BreakerHalfOpen,
					wantTo:      BreakerOpen,
					wantChanged: true,
					wantState:   BreakerOpen,
					wantFails:   2,
				},
				{kind: opAllow, at: 21 * time.Second, wantAllowed: false, wantState: BreakerOpen, wantFails: 2},
				{
					kind:        opAllow,
					at:          22 * time.Second,
					wantAllowed: true,
					wantFrom:    BreakerOpen,
					wantTo:      BreakerHalfOpen,
					wantChanged: true,
					wantState:   BreakerHalfOpen,
					wantFails:   2,
				},
			},
		},
		{
			name: "a result that arrives while the breaker is open changes nothing",
			ops: []op{
				{kind: opRecord, at: 0, success: false, wantState: BreakerClosed, wantFails: 1},
				{
					kind:        opRecord,
					at:          0,
					success:     false,
					wantFrom:    BreakerClosed,
					wantTo:      BreakerOpen,
					wantChanged: true,
					wantState:   BreakerOpen,
					wantFails:   2,
				},
				{kind: opRecord, at: 1, success: true, wantState: BreakerOpen, wantFails: 2},
				{kind: opRecord, at: 1, success: false, wantState: BreakerOpen, wantFails: 2},
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
					allowed, trans = brk.allow(baseTime().Add(step.at))
				} else {
					trans = brk.record(step.success, baseTime().Add(step.at))
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

func TestCacheNeverExceedsItsBound(t *testing.T) {
	t.Parallel()

	cch := newCache(SegmentOptions{MaxEntries: 40, Shards: 4, TTL: time.Hour, NegativeTTL: time.Hour})

	for i := range 1000 {
		cch.put(strconv.Itoa(i), segment.Member, baseTime())
	}

	assert.LessOrEqual(t, cch.len(), 40)
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
	calls int
}

func (s *internalStore) Contains(_ context.Context, lookups []segment.Lookup) error {
	s.calls++

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

	store := &internalStore{}
	res := newInternal(t, store, &Options{Default: SegmentOptions{TTL: time.Minute}})
	now := clock.Now()

	lookups := func() []segment.Lookup { return []segment.Lookup{{Segment: "beta", ID: "a"}} }

	require.NoError(t, res.answerAll(context.Background(), lookups(), now, &CallObservation{}))
	require.NoError(t, res.answerAll(context.Background(), lookups(), now.Add(time.Minute-1), &CallObservation{}))
	assert.Equal(t, 1, store.calls, "served from the cache before the TTL")

	require.NoError(t, res.answerAll(context.Background(), lookups(), now.Add(time.Minute), &CallObservation{}))
	assert.Equal(t, 2, store.calls, "asked again at the TTL")
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

	lookups := func() []segment.Lookup { return []segment.Lookup{{Segment: "beta", ID: "a"}} }

	require.ErrorIs(t, res.answerAll(context.Background(), lookups(), now, &CallObservation{}), errBoomInternal)
	require.ErrorIs(t, res.answerAll(context.Background(), lookups(), now, &CallObservation{}), ErrCircuitOpen)
	assert.Equal(t, 1, store.calls)

	store.fail = nil

	probe := lookups()
	require.NoError(t, res.answerAll(context.Background(), probe, now.Add(time.Minute), &CallObservation{}))
	assert.Equal(t, []segment.State{segment.Member}, []segment.State{probe[0].State})
	assert.Equal(t, 2, store.calls)
	assert.Equal(t, BreakerClosed, res.Status().Segments["beta"].Breaker)

	stats := counters.Snapshot()
	assert.Equal(t, uint64(1), stats.BreakerOpened)
	assert.Equal(t, uint64(1), stats.BreakerHalfOpened)
	assert.Equal(t, uint64(1), stats.BreakerClosed)
}

func TestInvalidateDuringAFlightDropsItsAnswer(t *testing.T) {
	t.Parallel()

	store := &internalStore{}
	res := newInternal(t, store, &Options{})
	seg := res.state("beta")

	seg.cache.put("old", segment.Member, baseTime())

	flt := &flight{done: make(chan struct{}), epoch: seg.epoch.Load()}
	seg.flights["k"] = flt

	res.Invalidate("beta")
	assert.Equal(t, 0, seg.cache.len())

	res.wg.Add(1)
	res.run(context.Background(), []leader{{sst: seg, flt: flt, key: "k"}})

	<-flt.done

	assert.Equal(t, segment.Member, flt.state, "the caller still gets its answer")
	assert.Equal(t, 0, seg.cache.len(), "but a stale answer is not cached")
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

var errBoomInternal = errors.New("boom")

func TestCallTimeoutDefault(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 2*time.Second, newInternal(t, &internalStore{}, nil).timeout)
	assert.Equal(t, time.Second, newInternal(t, &internalStore{}, &Options{CallTimeout: time.Second}).timeout)
}
