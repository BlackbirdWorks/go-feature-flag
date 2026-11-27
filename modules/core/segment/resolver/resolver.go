package resolver

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/blackbirdworks/semaphore/modules/core/clock"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

var (
	// ErrCircuitOpen is returned when the breaker of a segment is open: the lookups are Unknown and the store was
	// not called.
	ErrCircuitOpen = errors.New("resolver: circuit open")
	// ErrWaiting is wrapped, with the error of the context, when the context of a caller (or the shared call)
	// ended while it waited for the store. The lookups it waited for are Unknown; the shared call goes on for the
	// other callers until its own timeout.
	ErrWaiting = errors.New("resolver: the context ended while waiting for the store")
	// ErrClosed is returned after Shutdown: no call reaches the store.
	ErrClosed = errors.New("resolver: closed")
	// ErrOverdue is returned when too many calls to the store are running after their timeout: the lookups that needed a
	// new call are Unknown and the store was not called.
	ErrOverdue = errors.New("resolver: too many calls to the store are overdue")
	// ErrShutdown is wrapped by the error of a Shutdown that ended before the calls in flight did.
	ErrShutdown = errors.New("resolver: shutdown ended before the calls in flight")
	// ErrProviderPanic is the error of a call to the store that panicked. The value of the panic is not kept.
	ErrProviderPanic = errors.New("resolver: the store panicked")
	// ErrUnsupported is returned by Exists when the store has no such operation.
	ErrUnsupported = errors.New("resolver: the store does not support this operation")
)

// StoreError is the error of a call to the store as the callers of the resolver see it. Its message is fixed: the
// message of the store may hold an address, a key or an id, and a caller logs what it gets. The error of the store
// is still there for [errors.Is] and [errors.As].
type StoreError struct {
	// Err is the error of the store.
	Err error
}

// Error implements error.
func (*StoreError) Error() string { return "resolver: the call to the store failed" }

// Unwrap returns the error of the store.
func (e *StoreError) Unwrap() error { return e.Err }

// maxRawFactor: a raw id longer than this many times MaxIDBytes is not even normalised.
const maxRawFactor = 4

// call is the context of one shared call to the store. It does not depend on the context of the caller that
// started it, so a caller that leaves does not cancel the others; it keeps the values of that context.
type call struct {
	ctx    context.Context //nolint:containedctx // the context of one shared call, read by the callers that wait for it
	cancel context.CancelFunc
}

// callState decides how a shared call ended, exactly once, under one lock: either the store returned in time or its
// deadline fired first. Whichever takes the lock first wins; the other then counts for nothing.
type callState struct {
	mu       sync.Mutex
	returned bool // the store returned before its deadline fired
	overdue  bool // the deadline fired before the store returned
}

// flight is one lookup that a caller has sent to the store; the other callers that ask the same wait on it.
type flight struct {
	err   error
	call  *call
	done  chan struct{}
	epoch uint64
	state segment.State
}

// segState is what the resolver keeps for one segment.
type segState struct {
	notLoadedUntil time.Time
	cache          *cache
	brk            *breaker
	flights        map[string]*flight
	name           string
	opts           SegmentOptions
	mu             sync.Mutex
	epoch          atomic.Uint64
	joined         atomic.Uint64 // the lookups that joined the flight of another caller
	notLoadedSkips atomic.Uint64 // the lookups not asked because the segment was found not loaded
}

// normalize returns the key of an id. An id that is not valid UTF-8 has no key when the segment normalises: the
// lowering would replace its bytes by U+FFFD and two different ids could meet a member that holds that character.
func (s *segState) normalize(id string) (string, Reason) {
	if s.opts.Normalize != NormalizeTrimLower {
		return id, ReasonNone
	}

	if !utf8.ValidString(id) {
		return "", ReasonInvalid
	}

	return strings.ToLower(strings.TrimSpace(id)), ReasonNone
}

// Resolver is a [segment.Provider] with a cache, coalescing and a breaker in front of another provider. It is safe
// for concurrent use.
type Resolver struct {
	inner      segment.Provider
	observer   Observer
	stopErr    error
	states     atomic.Pointer[map[string]*segState]
	beforeStop func(*call)
	opts       Options
	wg         sync.WaitGroup
	overdue    atomic.Int64
	inflight   atomic.Int64
	maxID      int
	maxOver    int
	timeout    time.Duration
	admit      sync.RWMutex
	stopOnce   sync.Once
	mu         sync.Mutex
	closed     bool
}

var (
	_ segment.Provider       = (*Resolver)(nil)
	_ segment.Initializer    = (*Resolver)(nil)
	_ segment.Shutdowner     = (*Resolver)(nil)
	_ segment.Existencer     = (*Resolver)(nil)
	_ segment.HealthReporter = (*Resolver)(nil)
)

// New returns a Resolver in front of inner.
func New(inner segment.Provider, opts *Options) (*Resolver, error) {
	if inner == nil {
		return nil, ErrNilProvider
	}

	if opts == nil {
		opts = &Options{}
	}

	err := opts.validate()
	if err != nil {
		return nil, err
	}

	res := &Resolver{
		inner: inner, opts: *opts, observer: opts.Observer, timeout: opts.CallTimeout, maxID: opts.MaxIDBytes,
		maxOver: opts.MaxOverdue,
	}
	if res.timeout == 0 {
		res.timeout = DefaultCallTimeout
	}

	if res.maxID == 0 {
		res.maxID = DefaultMaxIDBytes
	}

	if res.maxOver == 0 {
		res.maxOver = DefaultMaxOverdue
	}

	states := make(map[string]*segState, len(opts.Segments))
	for name, seg := range opts.Segments {
		states[name] = newSegState(name, seg)
	}

	res.states.Store(&states)

	return res, nil
}

func newSegState(name string, opts SegmentOptions) *segState {
	opts = opts.withDefaults()
	sst := &segState{name: name, opts: opts, flights: make(map[string]*flight)}

	if !opts.NoCache {
		sst.cache = newCache(opts)
	}

	if !opts.NoBreaker {
		sst.brk = newBreaker(opts)
	}

	return sst
}

// pending is a lookup that the cache did not answer.
type pending struct {
	sst    *segState
	flt    *flight
	key    string
	gen    uint64
	idx    int
	state  segment.State // the answer of the cache, when hit is true
	leader bool
	hit    bool
}

// leader is a lookup that this call sends to the store.
type leader struct {
	sst *segState
	flt *flight
	key string
	gen uint64
}

// segAcc counts what happened to the lookups of one segment in one call, for the observer.
type segAcc struct {
	name                                      string
	lookups, hits, misses, coalesced, unknown int
	reason                                    Reason
}

func (a *segAcc) mark(reason Reason) {
	if a.reason == ReasonNone {
		a.reason = reason
	}
}

// segAccs holds the counts of the first segments of a call in an array, so counting does not allocate.
type segAccs struct {
	items [MaxObservedSegments]segAcc
	spill segAcc
	n     int
}

func (a *segAccs) of(name string) *segAcc {
	for i := range a.n {
		if a.items[i].name == name {
			return &a.items[i]
		}
	}

	if a.n < len(a.items) {
		a.items[a.n].name = name
		a.n++

		return &a.items[a.n-1]
	}

	return &a.spill
}

// Contains implements [segment.Provider].
func (r *Resolver) Contains(ctx context.Context, lookups []segment.Lookup) error {
	return r.containsAt(ctx, lookups, clock.Now())
}

// decision is what the breaker of a segment said to this call.
type decision struct {
	sst *segState
	gen uint64
	ok  bool
}

// waitFor reads the answers of the flights. Each lookup waits for its own flight, until the context of the caller
// ends or the call of that flight passes its own timeout (whether or not the store honours its context): a flight
// that times out leaves its lookups Unknown and the others go on waiting for theirs. An answer that is already there
// wins over a context that ended too.
func waitFor(ctx context.Context, lookups []segment.Lookup, pend []pending, accs *segAccs) error {
	var storeErr, waitErr error

	for i := range pend {
		pnd := &pend[i]
		if pnd.flt == nil {
			continue
		}

		verdict, cause := awaitFlight(ctx, pnd.flt)

		switch verdict {
		case flightAnswered:
			lookups[pnd.idx].State = pnd.flt.state

			if pnd.flt.err != nil {
				accs.of(pnd.sst.name).mark(ReasonStore)

				storeErr = firstOf(storeErr, pnd.flt.err)
			}
		case flightTimedOut:
			accs.of(pnd.sst.name).mark(ReasonWaiting)

			waitErr = firstOf(waitErr, fmt.Errorf("%w: %w", ErrWaiting, cause))
		case callerGone:
			accs.of(pnd.sst.name).mark(ReasonWaiting)
			keepAnswered(lookups, pend[i+1:])

			return errors.Join(storeErr, firstOf(waitErr, fmt.Errorf("%w: %w", ErrWaiting, cause)))
		}
	}

	// both kinds are reported, in this order, whatever the order of the lookups: a caller can detect either
	return errors.Join(storeErr, waitErr)
}

func firstOf(first, next error) error {
	if first != nil {
		return first
	}

	return next
}

// keepAnswered sets the lookups whose flights are already done: the caller leaves, and what it has keeps its answer.
func keepAnswered(lookups []segment.Lookup, rest []pending) {
	for i := range rest {
		if rest[i].flt != nil && isDone(rest[i].flt) {
			lookups[rest[i].idx].State = rest[i].flt.state
		}
	}
}

// flightVerdict is how the wait for one flight ended.
type flightVerdict uint8

const (
	flightAnswered flightVerdict = iota
	flightTimedOut
	callerGone
)

// awaitFlight waits for a flight, and tells how the wait ended and, when it did not end with an answer, why.
func awaitFlight(ctx context.Context, flt *flight) (flightVerdict, error) {
	if isDone(flt) {
		return flightAnswered, nil
	}

	select {
	case <-flt.done:
		return flightAnswered, nil
	case <-ctx.Done():
		if isDone(flt) {
			return flightAnswered, nil // the answer came together with the end of the context
		}

		return callerGone, fmt.Errorf("%w", ctx.Err())
	case <-flt.call.ctx.Done():
		if isDone(flt) {
			return flightAnswered, nil
		}

		return flightTimedOut, fmt.Errorf("%w", flt.call.ctx.Err())
	}
}

func isDone(flt *flight) bool {
	select {
	case <-flt.done:
		return true
	default:
		return false
	}
}

// Invalidate drops the cached answers of a segment, the memory of a segment that was not loaded, and detaches the
// calls that are in flight for it: their answers are not cached, and a caller that comes after Invalidate asks the
// store again instead of joining them. It is the seam for a change notification: whatever learns that the members
// of a segment changed (a new version of the set, a sync of the tooling) calls it.
func (r *Resolver) Invalidate(name string) {
	sst := (*r.states.Load())[name]
	if sst == nil {
		return
	}

	sst.mu.Lock()
	defer sst.mu.Unlock()

	sst.epoch.Add(1)
	sst.flights = make(map[string]*flight)
	sst.notLoadedUntil = time.Time{}

	if sst.cache != nil {
		sst.cache.clear()
	}
}

// SegmentStatus is the state of one segment of the resolver.
type SegmentStatus struct {
	// Breaker is the state of the breaker, BreakerClosed when there is none.
	Breaker BreakerState
	// Failures is the number of consecutive failed calls counted by a closed breaker.
	Failures int
	// Entries is the number of cached answers.
	Entries int
	// Joined is the number of lookups that joined the call of another caller since the start.
	Joined uint64
	// NotLoaded is the number of lookups not asked because the segment was recently found not loaded.
	NotLoaded uint64
}

// Status is a snapshot of the resolver.
type Status struct {
	// Segments is the state of each segment that was configured or looked up.
	Segments map[string]SegmentStatus
	// Running is the number of shared calls to the store that have not returned.
	Running int
	// Overdue is the number of those that are running after their context ended: a store that ignores its context.
	Overdue int
	// Closed is true after Shutdown.
	Closed bool
}

// Status returns a snapshot of the breakers and the caches.
func (r *Resolver) Status() Status {
	states := *r.states.Load()
	status := Status{
		Segments: make(map[string]SegmentStatus, len(states)),
		Running:  int(r.inflight.Load()),
		Overdue:  int(r.overdue.Load()),
	}

	r.admit.RLock()
	status.Closed = r.closed
	r.admit.RUnlock()

	for name, sst := range states {
		seg := SegmentStatus{Joined: sst.joined.Load(), NotLoaded: sst.notLoadedSkips.Load()}

		if sst.brk != nil {
			seg.Breaker, seg.Failures = sst.brk.snapshot()
		}

		if sst.cache != nil {
			seg.Entries = sst.cache.len()
		}

		status.Segments[name] = seg
	}

	return status
}

// Health implements [segment.HealthReporter]: the health of the store when it reports one, and whether the resolver is
// closed.
func (r *Resolver) Health() segment.Health {
	health := segment.Health{Ready: true}

	if reporter, ok := r.inner.(segment.HealthReporter); ok {
		health = reporter.Health()
	}

	r.admit.RLock()
	defer r.admit.RUnlock()

	health.Closed = health.Closed || r.closed

	return health
}

// Init calls Init of the store when it has one.
func (r *Resolver) Init(ctx context.Context) error {
	init, ok := r.inner.(segment.Initializer)
	if !ok {
		return nil
	}

	leave, err := r.enter()
	if err != nil {
		return err
	}

	defer leave()

	return init.Init(ctx) //nolint:wrapcheck // the store's own error
}

// Exists asks the store whether the segments are loaded. It returns ErrUnsupported when the store cannot tell.
func (r *Resolver) Exists(ctx context.Context, names []string) ([]segment.State, error) {
	existencer, ok := r.inner.(segment.Existencer)
	if !ok {
		return nil, ErrUnsupported
	}

	leave, err := r.enter()
	if err != nil {
		return nil, err
	}

	defer leave()

	return existencer.Exists(ctx, names) //nolint:wrapcheck // the store's own error
}

// Shutdown stops accepting calls, waits for the calls to the store that are in flight (until ctx ends), and calls
// Shutdown of the store once. After it started, no call reaches the store: Contains returns ErrClosed. A second
// Shutdown waits again for the calls in flight, and returns nil when they are done.
func (r *Resolver) Shutdown(ctx context.Context) error {
	r.admit.Lock()
	r.closed = true
	r.admit.Unlock()

	drained := make(chan struct{})

	go func() {
		r.wg.Wait()
		close(drained)
	}()

	select {
	case <-drained:
	case <-ctx.Done():
		return fmt.Errorf("%w: %d calls still running: %w", ErrShutdown, r.inflight.Load(), ctx.Err())
	}

	if stopper, ok := r.inner.(segment.Shutdowner); ok {
		r.stopOnce.Do(func() { r.stopErr = stopper.Shutdown(ctx) })

		return r.stopErr
	}

	return nil
}

// state returns the state of a segment, and creates it with the default settings the first time a segment that
// the options do not name is looked up.
func (r *Resolver) state(name string) *segState {
	if sst := (*r.states.Load())[name]; sst != nil {
		return sst
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	current := *r.states.Load()
	if sst := current[name]; sst != nil {
		return sst
	}

	next := make(map[string]*segState, len(current)+1)
	maps.Copy(next, current)

	sst := newSegState(name, r.opts.Default)
	next[name] = sst
	r.states.Store(&next)

	return sst
}

// register joins the flight of each lookup, or opens one when the breaker lets it. It returns what it refused (the
// breaker of a segment dropped a lookup, or too many calls are overdue) and the shared call that the new flights
// belong to (nil when there is none). The cache is looked at again under the lock of the registration: an answer
// that was filled since the first look costs no second call.
func (r *Resolver) register(
	ctx context.Context, pend []pending, now time.Time, obs *CallObservation, accs *segAccs,
) (*call, error) {
	var memo [4]decision

	decided := memo[:0]

	var (
		refusal error
		shared  *call
	)

	for i := range pend {
		pnd := &pend[i]
		sst := pnd.sst
		acc := accs.of(sst.name)

		sst.mu.Lock()

		flt := sst.flights[pnd.key]
		if flt != nil {
			sst.joined.Add(1)
			sst.mu.Unlock()

			pnd.flt = flt
			obs.Coalesced++
			acc.coalesced++

			continue
		}

		handled, refused := r.skip(pnd, now, obs, acc)
		if handled {
			if refused != nil && refusal == nil {
				refusal = refused
			}

			continue
		}

		ok, gen := r.allowed(sst, &decided, now)
		if !ok {
			sst.mu.Unlock()

			obs.ShortCircuited++

			acc.mark(ReasonCircuitOpen)

			refusal = ErrCircuitOpen

			continue
		}

		if shared == nil {
			shared = newCall(ctx, r.timeout)
		}

		flt = &flight{done: make(chan struct{}), call: shared, epoch: sst.epoch.Load()}
		sst.flights[pnd.key] = flt
		sst.mu.Unlock()

		pnd.flt, pnd.leader, pnd.gen = flt, true, gen
		obs.Misses++
		acc.misses++
	}

	return shared, refusal
}

// skip handles the lookups that need no call: an answer that the cache has since the first look, a segment found not
// loaded recently, and too many calls overdue. It is called with the lock of the segment held and releases it when it
// handled the lookup. It returns what it refused, if anything.
func (r *Resolver) skip(pnd *pending, now time.Time, obs *CallObservation, acc *segAcc) (bool, error) {
	sst := pnd.sst

	if sst.cache != nil {
		state, ok := sst.cache.get(pnd.key, now)
		if ok {
			sst.mu.Unlock()

			pnd.hit, pnd.state = true, state
			obs.Hits++
			acc.hits++

			return true, nil
		}
	}

	if now.Before(sst.notLoadedUntil) {
		sst.mu.Unlock()
		sst.notLoadedSkips.Add(1)

		obs.NotLoaded++

		acc.mark(ReasonNotLoaded)

		return true, nil
	}

	if int(r.overdue.Load()) >= r.maxOver {
		sst.mu.Unlock()

		obs.ShortCircuited++

		acc.mark(ReasonOverdue)

		return true, ErrOverdue
	}

	return false, nil
}

func newCall(ctx context.Context, timeout time.Duration) *call {
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)

	return &call{ctx: callCtx, cancel: cancel}
}

// allowed asks the breaker of the segment once for each call: a probe is one permission for the whole call.
func (r *Resolver) allowed(sst *segState, decided *[]decision, now time.Time) (bool, uint64) {
	if sst.brk == nil {
		return true, 0
	}

	for _, d := range *decided {
		if d.sst == sst {
			return d.ok, d.gen
		}
	}

	ok, gen, tr := sst.brk.allow(now)
	r.emit(sst, tr)

	*decided = append(*decided, decision{sst: sst, ok: ok, gen: gen})

	return ok, gen
}

func (r *Resolver) emit(sst *segState, tr transition) {
	if tr.changed && r.observer != nil {
		r.observer.ObserveBreaker(BreakerEvent{Segment: sst.name, From: tr.from, To: tr.to})
	}
}

// run is the shared call to the store.
func (r *Resolver) run(shared *call, leaders []leader) {
	defer r.wg.Done()
	defer r.inflight.Add(-1)
	defer shared.cancel()

	// A store that ignores its context keeps running after the deadline: it is a failure of the store from the moment
	// it is overdue, not when (if ever) it returns, so that the breaker stops the new calls while the stuck ones are
	// still out. The breaker is told before the counter moves, so a reader that sees the counter sees the failure
	// too. The store's return and the deadline are decided under one lock (see callState).
	state := &callState{}
	fired := make(chan struct{})
	stop := context.AfterFunc(shared.ctx, func() {
		defer close(fired)

		state.mu.Lock()
		defer state.mu.Unlock()

		if state.returned {
			return // the store was in time: nothing is overdue
		}

		state.overdue = true

		r.settleOverdue(leaders, clock.Now())
		r.overdue.Add(1)
	})

	sub := make([]segment.Lookup, len(leaders))
	for i := range leaders {
		sub[i] = segment.Lookup{Segment: leaders[i].sst.name, ID: leaders[i].key}
	}

	callErr := r.call(shared.ctx, sub)

	late := r.disarm(state, shared, stop, fired)

	if errors.Is(callErr, ErrProviderPanic) {
		// a store that panicked is not trusted: nothing it set counts
		for i := range sub {
			sub[i].State = segment.Unknown
		}
	} else if callErr != nil {
		callErr = &StoreError{Err: callErr}
	}

	now := clock.Now()

	r.settle(leaders, sub, callErr, now, late)

	for i := range leaders {
		lead := &leaders[i]
		state := sub[i].State

		if !state.Valid() {
			state = segment.Unknown
		}

		lead.flt.state = state

		if state == segment.Unknown {
			lead.flt.err = callErr
		}

		lead.sst.mu.Lock()

		// delete the flight only if the map still holds this one: Invalidate may have detached it
		if lead.sst.flights[lead.key] == lead.flt {
			delete(lead.sst.flights, lead.key)
		}

		if lead.sst.cache != nil && lead.sst.epoch.Load() == lead.flt.epoch {
			lead.sst.cache.put(lead.key, state, now)
		}

		lead.sst.mu.Unlock()

		close(lead.flt.done)
	}
}

// disarm ends the deadline of a shared call after the store returned, and tells whether the deadline had fired first.
// The decision is taken under the lock of the state: a return that comes first makes the deadline callback record
// nothing, a deadline that comes first makes the late return count for nothing.
func (r *Resolver) disarm(state *callState, shared *call, stop func() bool, fired <-chan struct{}) bool {
	state.mu.Lock()
	state.returned = !state.overdue
	late := state.overdue
	state.mu.Unlock()

	if r.beforeStop != nil {
		r.beforeStop(shared)
	}

	if !stop() {
		<-fired // the deadline callback is running or ran: it has seen the decision above
	}

	if late {
		r.overdue.Add(-1)
	}

	return late
}

func (r *Resolver) call(ctx context.Context, sub []segment.Lookup) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrProviderPanic
		}
	}()

	return r.inner.Contains(ctx, sub) //nolint:wrapcheck // the store's own error is the call's error
}

// segmentResult is how a call went for the lookups of one segment.
type segmentResult struct {
	sst     *segState
	flt     *flight
	gen     uint64
	total   int
	unknown int
}

// settle tells the breaker of each segment of the call how it went. A segment failed when the call failed and left
// one of its lookups unanswered. When the store replied without error and every lookup of a segment is Unknown, the
// segment is not loaded: that is neither a success nor a failure of the store, and the segment is not asked again
// for a while.
func (r *Resolver) settle(leaders []leader, sub []segment.Lookup, callErr error, now time.Time, late bool) {
	var results []segmentResult

	for i := range leaders {
		res := resultOf(&results, leaders[i])

		res.total++

		if !sub[i].State.Valid() || sub[i].State == segment.Unknown {
			res.unknown++
		}
	}

	for i := range results {
		res := &results[i]
		verdict := outcomeSuccess

		switch {
		case res.unknown == 0:
		case callErr != nil:
			verdict = outcomeFailure
		case res.unknown == res.total:
			verdict = outcomeNeutral

			rememberNotLoaded(res, now)
		}

		// a call that was overdue was counted as a failure when it became overdue: its late result counts once
		if res.sst.brk != nil && !late {
			r.emit(res.sst, res.sst.brk.record(res.gen, verdict, now))
		}
	}
}

// settleOverdue records a failure for each segment of a call that passed its timeout without returning.
func (r *Resolver) settleOverdue(leaders []leader, now time.Time) {
	var seen []*segState

	for i := range leaders {
		sst := leaders[i].sst
		if sst.brk == nil || slices.Contains(seen, sst) {
			continue
		}

		seen = append(seen, sst)

		r.emit(sst, sst.brk.record(leaders[i].gen, outcomeFailure, now))
	}
}

// resultOf returns the result of the segment of a leader, and adds it when it is the first lookup of that segment.
func resultOf(results *[]segmentResult, lead leader) *segmentResult {
	for j := range *results {
		if (*results)[j].sst == lead.sst {
			return &(*results)[j]
		}
	}

	*results = append(*results, segmentResult{sst: lead.sst, flt: lead.flt, gen: lead.gen})

	return &(*results)[len(*results)-1]
}

// rememberNotLoaded makes the segment skip the store for the negative TTL, unless Invalidate ran meanwhile.
func rememberNotLoaded(res *segmentResult, now time.Time) {
	res.sst.mu.Lock()
	defer res.sst.mu.Unlock()

	if res.sst.epoch.Load() == res.flt.epoch {
		res.sst.notLoadedUntil = now.Add(res.sst.opts.NegativeTTL)
	}
}

// containsAt is Contains at the instant start, which the tests choose.
func (r *Resolver) containsAt(ctx context.Context, lookups []segment.Lookup, start time.Time) error {
	if len(lookups) == 0 {
		return nil
	}

	// a state that the caller left in the slice never survives a failure
	for i := range lookups {
		lookups[i].State = segment.Unknown
	}

	obs := CallObservation{Lookups: len(lookups)}

	var accs segAccs

	r.admit.RLock()

	if r.closed {
		r.admit.RUnlock()

		return ErrClosed
	}

	pend, refusal, err := r.prepare(ctx, lookups, start, &obs, &accs)

	r.admit.RUnlock()

	if err == nil && len(pend) > 0 {
		err = waitFor(ctx, lookups, pend, &accs)
		if err == nil && refusal != nil {
			err = refusal
		}
	}

	if r.observer != nil {
		r.report(lookups, &obs, &accs, err, start)
	}

	return err
}

func (r *Resolver) report(lookups []segment.Lookup, obs *CallObservation, accs *segAccs, err error, start time.Time) {
	for i := range lookups {
		if lookups[i].State == segment.Unknown {
			obs.Unknown++
			accs.of(lookups[i].Segment).unknown++
		}
	}

	for i := range accs.n {
		acc := &accs.items[i]
		r.observer.ObserveSegment(SegmentObservation{
			Segment: acc.name, Lookups: acc.lookups, Hits: acc.hits, Misses: acc.misses,
			Coalesced: acc.coalesced, Unknown: acc.unknown, Failure: acc.reason,
		})
	}

	obs.Duration = clock.Now().Sub(start)
	obs.Failed = err != nil
	r.observer.ObserveCall(*obs)
}

// prepare answers what the cache has, and sends the rest to the store: it joins the flights that exist, opens the
// ones that the breaker allows and starts the shared call. It runs under the admission lock, so a call that is
// started is waited for by Shutdown.
func (r *Resolver) prepare(
	ctx context.Context, lookups []segment.Lookup, now time.Time, obs *CallObservation, accs *segAccs,
) ([]pending, error, error) {
	var pend []pending

	for i := range lookups {
		sst := r.state(lookups[i].Segment)
		acc := accs.of(lookups[i].Segment)
		acc.lookups++

		// a guard before the work of normalising: what the store and the cache see is the normalised id, which is
		// checked below
		if len(lookups[i].ID) > maxRawFactor*r.maxID {
			obs.Oversized++

			acc.mark(ReasonOversized)

			continue
		}

		key, reason := sst.normalize(lookups[i].ID)
		if reason != ReasonNone {
			obs.Invalid++

			acc.mark(reason)

			continue
		}

		if len(key) > r.maxID {
			obs.Oversized++

			acc.mark(ReasonOversized)

			continue
		}

		if sst.cache != nil {
			state, ok := sst.cache.get(key, now)
			if ok {
				lookups[i].State = state
				obs.Hits++
				acc.hits++

				continue
			}
		}

		pend = append(pend, pending{sst: sst, key: key, idx: i})
	}

	if len(pend) == 0 {
		return nil, nil, nil
	}

	// a caller that is gone does not start a call; what the cache had was answered above
	if ctx.Err() != nil {
		for i := range pend {
			accs.of(pend[i].sst.name).mark(ReasonWaiting)
		}

		return nil, nil, fmt.Errorf("%w: %w", ErrWaiting, ctx.Err())
	}

	shared, refusal := r.register(ctx, pend, now, obs, accs)
	if shared != nil {
		r.start(shared, pend)
	}

	for i := range pend {
		if pend[i].hit {
			lookups[pend[i].idx].State = pend[i].state
		}
	}

	return pend, refusal, nil
}

// start runs the shared call of the leaders in pend.
func (r *Resolver) start(shared *call, pend []pending) {
	var leaders []leader

	for i := range pend {
		if pend[i].leader {
			leaders = append(leaders, leader{sst: pend[i].sst, flt: pend[i].flt, key: pend[i].key, gen: pend[i].gen})
		}
	}

	r.wg.Add(1)
	r.inflight.Add(1)

	go r.run(shared, leaders)
}

// enter admits a call to the store that is not a lookup (Init, Exists) under the same lock as the lookups: nothing
// reaches the store once Shutdown has started, and Shutdown waits for the calls that were admitted. The returned
// function ends the call.
func (r *Resolver) enter() (func(), error) {
	r.admit.RLock()
	defer r.admit.RUnlock()

	if r.closed {
		return nil, ErrClosed
	}

	r.wg.Add(1)
	r.inflight.Add(1)

	return func() {
		r.inflight.Add(-1)
		r.wg.Done()
	}, nil
}
