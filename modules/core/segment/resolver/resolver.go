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

	"github.com/blackbirdworks/semaphore/modules/core/clock"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

var (
	// ErrCircuitOpen is returned when the breaker of a segment is open: the lookups are Unknown and the store was
	// not called.
	ErrCircuitOpen = errors.New("resolver: circuit open")
	// ErrWaiting is wrapped, with the error of the context, when the context of a caller ended while it waited for
	// the store. The lookups it waited for are Unknown; the shared call goes on for the other callers.
	ErrWaiting = errors.New("resolver: the context ended while waiting for the store")
	// ErrClosed is returned after Shutdown.
	ErrClosed = errors.New("resolver: closed")
	// ErrProviderPanic is the error of a call to the store that panicked. The value of the panic is not kept.
	ErrProviderPanic = errors.New("resolver: the store panicked")
	// ErrUnsupported is returned by Exists when the store has no such operation.
	ErrUnsupported = errors.New("resolver: the store does not support this operation")
)

// Initializer is implemented by a store that has something to check or prepare before the first lookup.
type Initializer interface {
	Init(ctx context.Context) error
}

// Shutdowner is implemented by a store that holds a resource that must be released.
type Shutdowner interface {
	Shutdown(ctx context.Context) error
}

// Existencer is implemented by a store that can tell whether a segment is loaded. The answers are in the order of
// names: Member means loaded, NotMember means not, Unknown means the store could not tell.
type Existencer interface {
	Exists(ctx context.Context, names []string) ([]segment.State, error)
}

// flight is one lookup that a caller has sent to the store; the other callers that ask the same wait on it.
type flight struct {
	err   error
	done  chan struct{}
	epoch uint64
	state segment.State
}

// segState is what the resolver keeps for one segment.
type segState struct {
	cache   *cache
	brk     *breaker
	flights map[string]*flight
	name    string
	opts    SegmentOptions
	mu      sync.Mutex
	epoch   atomic.Uint64
	joined  atomic.Uint64 // the lookups that joined the flight of another caller
}

func (s *segState) normalize(id string) string {
	if s.opts.Normalize == NormalizeTrimLower {
		return strings.ToLower(strings.TrimSpace(id))
	}

	return id
}

// Resolver is a [segment.Provider] with a cache, coalescing and a breaker in front of another provider. It is safe
// for concurrent use.
type Resolver struct {
	inner    segment.Provider
	states   atomic.Pointer[map[string]*segState]
	observer Observer
	opts     Options
	wg       sync.WaitGroup
	mu       sync.Mutex
	timeout  time.Duration
	closed   atomic.Bool
}

var _ segment.Provider = (*Resolver)(nil)

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

	res := &Resolver{inner: inner, opts: *opts, observer: opts.Observer, timeout: opts.CallTimeout}
	if res.timeout == 0 {
		res.timeout = DefaultCallTimeout
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
	idx    int
	leader bool
}

// leader is a lookup that this call sends to the store.
type leader struct {
	sst *segState
	flt *flight
	key string
}

// Contains implements [segment.Provider].
func (r *Resolver) Contains(ctx context.Context, lookups []segment.Lookup) error {
	if len(lookups) == 0 {
		return nil
	}

	if r.closed.Load() {
		return ErrClosed
	}

	start := clock.Now()
	obs := CallObservation{Lookups: len(lookups)}
	err := r.answerAll(ctx, lookups, start, &obs)

	if r.observer != nil {
		for i := range lookups {
			if lookups[i].State == segment.Unknown {
				obs.Unknown++
			}
		}

		obs.Duration = clock.Now().Sub(start)
		obs.Failed = err != nil
		r.observer.ObserveCall(obs)
	}

	return err
}

// decision is what the breaker of a segment said to this call.
type decision struct {
	sst *segState
	ok  bool
}

// wait reads the answers of the flights, and leaves when the context of the caller ends.
func waitFor(ctx context.Context, lookups []segment.Lookup, pend []pending) error {
	var firstErr error

	done := ctx.Done()

	for i := range pend {
		pnd := &pend[i]
		if pnd.flt == nil {
			continue
		}

		select {
		case <-pnd.flt.done:
			lookups[pnd.idx].State = pnd.flt.state

			if pnd.flt.err != nil && firstErr == nil {
				firstErr = pnd.flt.err
			}
		case <-done:
			return fmt.Errorf("%w: %w", ErrWaiting, ctx.Err())
		}
	}

	return firstErr
}

// Invalidate drops the cached answers of a segment, and the answers of the calls that are in flight for it. It is
// the seam for a change notification: whatever learns that the members of a segment changed (a new version of the
// set, a sync of the tooling) calls it.
func (r *Resolver) Invalidate(name string) {
	sst := (*r.states.Load())[name]
	if sst == nil {
		return
	}

	sst.mu.Lock()
	defer sst.mu.Unlock()

	sst.epoch.Add(1)

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
}

// Status is a snapshot of the resolver.
type Status struct {
	// Segments is the state of each segment that was configured or looked up.
	Segments map[string]SegmentStatus
	// Closed is true after Shutdown.
	Closed bool
}

// Status returns a snapshot of the breakers and the caches.
func (r *Resolver) Status() Status {
	states := *r.states.Load()
	status := Status{Segments: make(map[string]SegmentStatus, len(states)), Closed: r.closed.Load()}

	for name, sst := range states {
		seg := SegmentStatus{Joined: sst.joined.Load()}

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

// Init calls Init of the store when it has one.
func (r *Resolver) Init(ctx context.Context) error {
	init, ok := r.inner.(Initializer)
	if !ok {
		return nil
	}

	return init.Init(ctx) //nolint:wrapcheck // the store's own error
}

// Exists asks the store whether the segments are loaded. It returns ErrUnsupported when the store cannot tell.
func (r *Resolver) Exists(ctx context.Context, names []string) ([]segment.State, error) {
	existencer, ok := r.inner.(Existencer)
	if !ok {
		return nil, ErrUnsupported
	}

	return existencer.Exists(ctx, names) //nolint:wrapcheck // the store's own error
}

// Shutdown stops accepting calls, waits for the calls to the store that are in flight (until ctx ends), and calls
// Shutdown of the store when it has one.
func (r *Resolver) Shutdown(ctx context.Context) error {
	r.closed.Store(true)

	drained := make(chan struct{})

	go func() {
		r.wg.Wait()
		close(drained)
	}()

	select {
	case <-drained:
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", ErrWaiting, ctx.Err())
	}

	stopper, ok := r.inner.(Shutdowner)
	if !ok {
		return nil
	}

	return stopper.Shutdown(ctx) //nolint:wrapcheck // the store's own error
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

func (r *Resolver) answerAll(ctx context.Context, lookups []segment.Lookup, now time.Time, obs *CallObservation) error {
	var pend []pending

	for i := range lookups {
		sst := r.state(lookups[i].Segment)
		key := sst.normalize(lookups[i].ID)

		if sst.cache != nil {
			state, ok := sst.cache.get(key, now)
			if ok {
				lookups[i].State = state
				obs.Hits++

				continue
			}
		}

		pend = append(pend, pending{sst: sst, key: key, idx: i})
	}

	if len(pend) == 0 {
		return nil
	}

	shorted := r.register(pend, now, obs)

	var leaders []leader

	for i := range pend {
		if pend[i].leader {
			leaders = append(leaders, leader{sst: pend[i].sst, flt: pend[i].flt, key: pend[i].key})
		}
	}

	if len(leaders) > 0 {
		r.wg.Add(1)

		go r.run(ctx, leaders)
	}

	err := waitFor(ctx, lookups, pend)
	if err == nil && shorted {
		return ErrCircuitOpen
	}

	return err
}

// register joins the flight of each lookup, or opens one when the breaker lets it. It returns true when the
// breaker of a segment dropped a lookup.
func (r *Resolver) register(pend []pending, now time.Time, obs *CallObservation) bool {
	var memo [4]decision

	decided := memo[:0]
	shorted := false

	for i := range pend {
		pnd := &pend[i]

		pnd.sst.mu.Lock()

		flt := pnd.sst.flights[pnd.key]
		if flt != nil {
			pnd.sst.joined.Add(1)
			pnd.sst.mu.Unlock()

			pnd.flt = flt
			obs.Coalesced++

			continue
		}

		if !r.allowed(pnd.sst, &decided, now) {
			pnd.sst.mu.Unlock()

			obs.ShortCircuited++
			shorted = true

			continue
		}

		flt = &flight{done: make(chan struct{}), epoch: pnd.sst.epoch.Load()}
		pnd.sst.flights[pnd.key] = flt
		pnd.sst.mu.Unlock()

		pnd.flt, pnd.leader = flt, true
		obs.Misses++
	}

	return shorted
}

// allowed asks the breaker of the segment once for each call: a probe is one permission for the whole call.
func (r *Resolver) allowed(sst *segState, decided *[]decision, now time.Time) bool {
	if sst.brk == nil {
		return true
	}

	for _, d := range *decided {
		if d.sst == sst {
			return d.ok
		}
	}

	ok, tr := sst.brk.allow(now)
	r.emit(sst, tr)

	*decided = append(*decided, decision{sst: sst, ok: ok})

	return ok
}

func (r *Resolver) emit(sst *segState, tr transition) {
	if tr.changed && r.observer != nil {
		r.observer.ObserveBreaker(BreakerEvent{Segment: sst.name, From: tr.from, To: tr.to})
	}
}

// run is the shared call to the store. It does not depend on the context of the caller that started it, so a
// caller that leaves does not cancel the others; it keeps the values of that context.
func (r *Resolver) run(ctx context.Context, leaders []leader) {
	defer r.wg.Done()

	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.timeout)
	defer cancel()

	sub := make([]segment.Lookup, len(leaders))
	for i := range leaders {
		sub[i] = segment.Lookup{Segment: leaders[i].sst.name, ID: leaders[i].key}
	}

	callErr := r.call(callCtx, sub)
	now := clock.Now()

	r.settle(leaders, sub, callErr, now)

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
		delete(lead.sst.flights, lead.key)

		if lead.sst.cache != nil && lead.sst.epoch.Load() == lead.flt.epoch {
			lead.sst.cache.put(lead.key, state, now)
		}

		lead.sst.mu.Unlock()

		close(lead.flt.done)
	}
}

func (r *Resolver) call(ctx context.Context, sub []segment.Lookup) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrProviderPanic
		}
	}()

	return r.inner.Contains(ctx, sub) //nolint:wrapcheck // the store's own error is the call's error
}

// settle tells the breaker of each segment of the call how it went: a segment failed when the call failed and left
// one of its lookups unanswered.
func (r *Resolver) settle(leaders []leader, sub []segment.Lookup, callErr error, now time.Time) {
	var seen []*segState

	for i := range leaders {
		sst := leaders[i].sst
		if sst.brk == nil || slices.Contains(seen, sst) {
			continue
		}

		seen = append(seen, sst)
		success := callErr == nil

		if !success {
			success = true

			for j := range leaders {
				if leaders[j].sst == sst && (!sub[j].State.Valid() || sub[j].State == segment.Unknown) {
					success = false

					break
				}
			}
		}

		r.emit(sst, sst.brk.record(success, now))
	}
}
