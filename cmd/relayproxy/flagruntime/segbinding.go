package flagruntime

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
	"github.com/blackbirdworks/semaphore/modules/core/segment/snapshot"
)

// ErrNoSnapshotSource is the reason of a snapshot definition on a connection that cannot read the members of a set.
var ErrNoSnapshotSource = errors.New("the connection cannot read the members of a segment: use mode: lookup")

// bindKey is what one binding serves: the definitions of one segments object on one connection with one reading of a
// missing key.
type bindKey struct {
	connection string
	missing    segment.MissingKey
}

// segBinding is the dynamic segments of one segments object (one checksum) on one connection: one resolver (cache,
// coalescing, breaker) for the lookup mode, one holder for each snapshot segment, one poll of the version keys. It is
// shared by every flag set and every generation that uses the object, built once and carried over by pointer while the
// checksum of the object is. It holds no connection and no goroutine: dropping the last reference releases it.
type segBinding struct {
	res       *resolver.Resolver
	versioner segment.Versioner
	holders   map[string]*snapshot.Holder
	// ids is the identity of each definition that the binding serves, see [defIdentity]. The next generation of the
	// object takes over what an unchanged identity built.
	ids map[string]string
	// refused holds, for a definition that this binding cannot serve, why; the others of the binding are served.
	refused map[string]error
	// lastClass is the class of the last failure of each holder that was reported, so a state is reported once. Only the
	// loop that polls touches it.
	lastClass map[string]snapshot.Class
	// lastOK is, for each holder, the time (of the loop) of its last refresh that worked.
	lastOK     map[string]time.Time
	connection string
	// lookupID is the identity of the lookup definitions together.
	lookupID  string
	lookups   []string
	snapshots []string
	next      atomic.Int64 // unix nanoseconds of the next poll
	// lookupNext and snapNext are the next version read of the lookups and the next refresh of the snapshot sets.
	lookupNext atomic.Int64
	snapNext   atomic.Int64
	// lookupInterval is the interval of the version reads of the lookups, snapInterval the one of the snapshot sets: each
	// is its own budget, and the binding wakes at the shortest.
	lookupInterval time.Duration
	snapInterval   time.Duration
	// pullTimeout is the deadline of the refresh of one holder.
	pullTimeout time.Duration
	armed       atomic.Bool
	versioned   bool
	mu          sync.Mutex // guards lastOK
}

// bindCache builds the bindings of one segments object when a flag set first uses them, and keeps them for the life
// of the object.
type bindCache struct {
	env   *SegmentEnv
	defs  map[bindKey][]*controlplane.SegmentDefinition
	keyOf map[string]bindKey
	built map[bindKey]bound
	carry *carry
	mu    sync.Mutex
}

// carry is what the previous generation of a segments object built and a definition of the new one with the same
// identity takes over: the holder of a snapshot segment with its state, and the resolver (and its cache) of the lookup
// definitions of a connection when none of them changed. A changed or new definition starts empty.
type carry struct {
	holders map[string]carriedHolder
	lookups map[bindKey]carriedLookup
}

type carriedHolder struct {
	holder *snapshot.Holder
	id     string
}

type carriedLookup struct {
	res       *resolver.Resolver
	versioner segment.Versioner
	id        string
	versioned bool
}

// defIdentity is what a binding needs to serve a definition the same way: everything of the definition except its name
// and its failure policy (the catalog applies that one). For a snapshot definition the cache block and the interval of
// the version do not count: a snapshot has no cache, and the interval is the one of the binding.
func defIdentity(def *controlplane.SegmentDefinition) string {
	same := *def
	same.Name, same.OnError = "", ""
	same.Mode = string(segment.Mode(def.Mode).Effective())
	same.MissingKey = string(segment.MissingKey(def.MissingKey).Effective())

	if same.Layout == "" {
		same.Layout = string(segment.LayoutSet) // an omitted layout is the set layout
	}

	if isSnapshot(def) {
		same.Cache = nil

		if same.Version != nil {
			version := *same.Version
			version.Interval = ""
			same.Version = &version
		}
	}

	data, err := json.Marshal(&same)
	if err != nil {
		return def.Name // cannot happen for this struct: the name makes it a different identity
	}

	return string(data)
}

// carryOf is what the new generation can take over from the cache of the previous one: only from bindings that were
// built (a flag set of the previous generation used them, so the loop kept their sets fresh), and only for a name that
// is still a snapshot definition with the same identity, or a connection that still has definitions. What nobody
// references is not kept: a set that was not refreshed for a while must not come back as it was. It keeps no reference
// to the previous cache.
func carryOf(prev *bindCache, snapshots map[string]string, keys map[bindKey]struct{}) *carry {
	out := &carry{holders: map[string]carriedHolder{}, lookups: map[bindKey]carriedLookup{}}
	if prev == nil {
		return out
	}

	prev.mu.Lock()
	defer prev.mu.Unlock()

	for key, held := range prev.built {
		if held.b == nil {
			continue
		}

		for name, holder := range held.b.holders {
			if id, ok := snapshots[name]; ok && id == held.b.ids[name] {
				out.holders[name] = carriedHolder{holder: holder, id: id}
			}
		}

		if _, still := keys[key]; still && held.b.res != nil {
			out.lookups[key] = carriedLookup{
				res: held.b.res, versioner: held.b.versioner, id: held.b.lookupID, versioned: held.b.versioned,
			}
		}
	}

	return out
}

type bound struct {
	b   *segBinding
	err error
}

// newBindCache groups the definitions of a document that the relay can serve. It binds nothing.
func newBindCache(
	env *SegmentEnv, doc *controlplane.SegmentsDocument, rejected map[string]struct{}, prev *bindCache,
) *bindCache {
	if !env.enabled() {
		return nil
	}

	cache := &bindCache{
		env: env, defs: map[bindKey][]*controlplane.SegmentDefinition{}, keyOf: map[string]bindKey{},
		built: map[bindKey]bound{},
	}

	for i := range doc.Segments {
		def := &doc.Segments[i]
		if _, bad := rejected[def.Name]; bad || def.Connection == "" {
			continue
		}

		key := bindKey{def.Connection, segment.MissingKey(def.MissingKey).Effective()}
		cache.defs[key] = append(cache.defs[key], def)
		cache.keyOf[def.Name] = key
	}

	if len(cache.defs) == 0 {
		return nil
	}

	snapshots := map[string]string{}

	for _, defs := range cache.defs {
		for _, def := range defs {
			if isSnapshot(def) {
				snapshots[def.Name] = defIdentity(def)
			}
		}
	}

	keys := make(map[bindKey]struct{}, len(cache.defs))

	for key, defs := range cache.defs {
		if slices.ContainsFunc(defs, func(def *controlplane.SegmentDefinition) bool { return !isSnapshot(def) }) {
			keys[key] = struct{}{}
		}
	}

	cache.carry = carryOf(prev, snapshots, keys)

	return cache
}

// binding returns the binding that serves the segment, built on first use. The segment is a served dynamic definition
// of the object that the cache was made for.
func (c *bindCache) binding(name string) (*segBinding, error) {
	key := c.keyOf[name] // the caller asks for a served dynamic definition of the object: it is there

	c.mu.Lock()
	defer c.mu.Unlock()

	held, done := c.built[key]
	if done {
		return held.b, held.err
	}

	b, err := c.env.bindObject(key, c.defs[key], c.carry)
	c.built[key] = bound{b, err}

	// what was not taken is not kept: the cache of a changed definition goes with the previous generation
	delete(c.carry.lookups, key)

	for _, def := range c.defs[key] {
		delete(c.carry.holders, def.Name)
	}

	return b, err
}

// isSnapshot reports whether the definition is served from a held set.
func isSnapshot(def *controlplane.SegmentDefinition) bool {
	return segment.Mode(def.Mode).Effective() == segment.ModeSnapshot
}

func (e *SegmentEnv) bindObject(
	key bindKey, defs []*controlplane.SegmentDefinition, carried *carry,
) (*segBinding, error) {
	bounds := make([]BoundDefinition, 0, len(defs))
	segs := map[string]resolver.SegmentOptions{}
	out := &segBinding{
		connection:  key.connection,
		holders:     map[string]*snapshot.Holder{},
		ids:         map[string]string{},
		refused:     map[string]error{},
		lastOK:      map[string]time.Time{},
		pullTimeout: cmp.Or(e.PullTimeout, DefaultPullTimeout),
	}
	values := map[string]string{}
	reuse := map[string]*snapshot.Holder{}
	lookupIDs := make([]string, 0, len(defs))

	for _, def := range defs {
		bounds = append(bounds, BoundDefinition{
			Name: def.Name, Key: def.Key, Layout: segment.Layout(def.Layout), VersionKey: versionKey(def),
		})

		interval := pollInterval(def)
		if def.Version == nil || def.Version.Key == "" {
			interval = DefaultVersionInterval
		}

		id := defIdentity(def)
		out.ids[def.Name] = id

		if isSnapshot(def) {
			out.snapshots = append(out.snapshots, def.Name)
			out.snapInterval = minInterval(out.snapInterval, interval)

			if old, ok := carried.holders[def.Name]; ok && old.id == id {
				reuse[def.Name] = old.holder
			}

			continue
		}

		lookupIDs = append(lookupIDs, def.Name+"="+id)

		opts := e.tuning(def.Cache)
		opts.Versioned = versionKey(def) != ""
		segs[def.Name] = opts
		out.lookups = append(out.lookups, def.Name)

		if opts.Versioned {
			out.versioned = true
			out.lookupInterval = minInterval(out.lookupInterval, interval)
		}

		if def.Version != nil && def.Version.Value != "" {
			values[def.Name] = def.Version.Value
		}
	}

	binding, err := e.Binder.Bind(key.connection, key.missing, bounds)
	if err != nil {
		return nil, fmt.Errorf("bind the segments of connection %q: %w", key.connection, err)
	}

	slices.Sort(lookupIDs)
	out.lookupID = strings.Join(lookupIDs, "\n")

	slices.Sort(out.lookups)
	slices.Sort(out.snapshots)

	out.build(e, binding, key, segs, values, &buildCarry{holders: reuse, lookup: carried.lookups[key]})

	return out, nil
}

// buildCarry is what a binding takes over from the previous generation of its object.
type buildCarry struct {
	holders map[string]*snapshot.Holder
	lookup  carriedLookup
}

func (b *segBinding) build(
	env *SegmentEnv, binding *Binding, key bindKey, segs map[string]resolver.SegmentOptions, values map[string]string,
	take *buildCarry,
) {
	if len(segs) > 0 && take.lookup.res != nil && take.lookup.id == b.lookupID {
		b.res, b.versioner, b.versioned = take.lookup.res, take.lookup.versioner, take.lookup.versioned
	} else if len(segs) > 0 {
		opts := env.options()
		opts.Segments = segs

		res, err := resolver.New(binding.Provider, opts)
		if err != nil {
			// the lookups of the connection are refused; its snapshot definitions are served on their own
			for _, name := range b.lookups {
				b.refused[name] = fmt.Errorf("the resolver of connection %q: %w", key.connection, err)
			}

			b.versioned = false
		} else {
			for name, value := range values {
				res.SetVersion(name, value)
			}

			b.res, b.versioner = res, binding.Versioner
			b.versioned = b.versioned && binding.Versioner != nil
		}
	}

	b.lastClass = map[string]snapshot.Class{}
	served := b.snapshots[:0:0]

	for _, name := range b.snapshots {
		if held, ok := take.holders[name]; ok {
			b.holders[name] = held
			b.lastClass[name] = held.Status().LastError
			served = append(served, name)

			continue
		}

		holder, err := snapshot.New(name, binding.Source, snapshot.Options{Missing: key.missing})
		if err != nil {
			// a store that cannot list members refuses its snapshot definitions alone: its lookups keep working
			b.refused[name] = fmt.Errorf(
				"the segments of connection %q: %w: %w",
				key.connection,
				ErrNoSnapshotSource,
				err,
			)

			continue
		}

		b.holders[name] = holder
		served = append(served, name)
	}

	b.snapshots = served
}

func minInterval(cur, other time.Duration) time.Duration {
	if cur == 0 || other < cur {
		return other
	}

	return cur
}

// interval is the shortest interval of the binding: it wakes then, and each part reads when its own is due.
func (b *segBinding) interval() time.Duration { return minInterval(b.lookupInterval, b.snapInterval) }

// polls reports whether the binding has anything to read off the request path.
func (b *segBinding) polls() bool { return b.versioned || len(b.holders) > 0 }

// loaded reports whether every holder has an answer: a set, or the word of the store that the segment is missing.
func (b *segBinding) loaded() bool {
	for _, h := range b.holders {
		if _, status := h.Answer(""); status == segment.StatusStoreError {
			return false
		}
	}

	return true
}

// firstPollSpread bounds the jitter of the first poll of a binding, and the pause before a holder that never loaded is
// read again.
const firstPollSpread = time.Second

// due reports whether the binding is polled at now, and schedules its next poll. The first poll of a binding is spread
// by jitter so that the bindings of a process do not read the stores in the same tick.
func (b *segBinding) due(now time.Time, jitter func(time.Duration) time.Duration) bool {
	if !b.polls() {
		return false
	}

	if !b.armed.Load() {
		var spread time.Duration
		if jitter != nil {
			spread = jitter(min(b.interval(), firstPollSpread))
		}

		b.next.Store(now.Add(spread).UnixNano())
		b.armed.Store(true)
	}

	return now.UnixNano() >= b.next.Load()
}

// holderError is the failure of the refresh of one holder: the class says whether the store failed or answered with
// an error about this set.
type holderError struct {
	err   error
	name  string
	class snapshot.Class
}

func (f *holderError) Error() string { return f.err.Error() }
func (f *holderError) Unwrap() error { return f.err }

// overdueAfter is how many intervals a held set may go without a refresh that worked before it answers STALE.
const overdueAfter = 3

// poll reads the version keys and refreshes the holders, then schedules the next poll. Each holder has a deadline of
// its own ([DefaultPullTimeout]): a slow set cannot starve the next one, and a pull may take longer than the interval.
// The failures are the ones that are new: a holder that fails the same way as at the last poll is not reported again.
// Only the end of ctx stops the poll before the last holder.
func (b *segBinding) poll(ctx context.Context, now time.Time) []error {
	var errs []error

	if b.versioned && b.res != nil && now.UnixNano() >= b.lookupNext.Load() {
		call, cancel := context.WithTimeout(ctx, b.lookupInterval)
		err := b.res.RefreshVersions(call, b.versioner)

		cancel()

		b.lookupNext.Store(now.Add(b.lookupInterval).UnixNano())

		if err != nil {
			errs = append(errs, fmt.Errorf("connection %q: %w", b.connection, err))
		}
	}

	if now.UnixNano() >= b.snapNext.Load() || !b.loaded() {
		errs = append(errs, b.refreshHolders(ctx, now)...)
		b.snapNext.Store(now.Add(b.snapInterval).UnixNano())
	}

	wait := b.interval()
	if !b.loaded() {
		wait = min(wait, firstPollSpread)
	}

	b.next.Store(now.Add(wait).UnixNano())

	return errs
}

// refreshHolders refreshes every holder, each under a deadline of its own.
func (b *segBinding) refreshHolders(ctx context.Context, now time.Time) []error {
	var errs []error

	for _, name := range b.snapshots {
		holder := b.holders[name]
		call, cancel := context.WithTimeout(ctx, b.pullTimeout)
		err := holder.Refresh(call)

		cancel()

		class := holder.Status().LastError
		worked := err == nil || class == snapshot.ClassMissing

		b.refreshed(name, holder, worked, now)

		if err != nil && class != snapshot.ClassMissing && class != b.lastClass[name] {
			errs = append(errs, &holderError{name: name, class: class, err: err})
		}

		b.lastClass[name] = class

		if ctx.Err() != nil {
			break
		}
	}

	return errs
}

// refreshed records the outcome of a refresh: a refresh that worked clears the overdue mark of the holder.
func (b *segBinding) refreshed(name string, holder *snapshot.Holder, worked bool, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if worked {
		b.lastOK[name] = now

		holder.SetOverdue(false)

		return
	}

	if b.lastOK[name].IsZero() {
		b.lastOK[name] = now
	}
}

// checkOverdue marks the holders whose set was not refreshed for longer than the bound: they answer STALE, whatever
// keeps them from being refreshed (a store that fails, a pass that hangs, a pass that never gets to them).
func (b *segBinding) checkOverdue(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for name, holder := range b.holders {
		last := b.lastOK[name]
		if last.IsZero() {
			b.lastOK[name] = now

			continue
		}

		if now.Sub(last) > overdueAfter*b.snapInterval {
			holder.SetOverdue(true)
		}
	}
}

// idle is called when no flag set of the served runtime uses the binding any more: nothing refreshes its sets, so they
// answer STALE if the binding is used again before a refresh worked.
func (b *segBinding) idle() {
	for _, holder := range b.holders {
		holder.SetOverdue(true)
	}

	b.next.Store(0) // due at once when it is used again
}

// lookupFailing reports whether the lookups of the binding fail: the store failed the last call or a breaker is open.
func (b *segBinding) lookupFailing() bool {
	if b.res == nil {
		return false
	}

	if b.res.Health().Failure != "" {
		return true
	}

	for _, seg := range b.res.Status().Segments {
		if seg.Breaker == resolver.BreakerOpen {
			return true
		}
	}

	return false
}

// holderHealth counts the holders that were tried and are not a problem of their set alone, and among them the ones
// that fail because the store fails (not reached, a timeout, a version that cannot be read). A set that the store
// answered about (a key of the wrong type, a reply that is not the one of the command, a set over the limit) is not
// counted: one broken set is not a store that fails.
func (b *segBinding) holderHealth() (int, int) {
	var failing, total int

	for _, held := range b.holders {
		status := held.Status()

		switch status.LastError {
		case snapshot.ClassSet, snapshot.ClassTooLarge:
			continue
		case snapshot.ClassNone:
			if !status.Loaded {
				continue // not tried yet
			}
		case snapshot.ClassMissing:
		case snapshot.ClassVersion, snapshot.ClassRead, snapshot.ClassCanceled:
			failing++
		}

		total++
	}

	return failing, total
}

// connectionDown reports whether the store of a connection fails: its lookups fail, or every set that was tried fails
// to refresh because the store does (and at least one was tried).
func connectionDown(bindings []*segBinding) bool {
	var failing, total int

	for _, b := range bindings {
		if b.lookupFailing() {
			return true
		}

		f, t := b.holderHealth()
		failing, total = failing+f, total+t
	}

	return total > 0 && failing == total
}
