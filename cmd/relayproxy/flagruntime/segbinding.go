package flagruntime

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
	// lastClass is the class of the last failure of each holder that was reported, so a state is reported once. Only the
	// loop that polls touches it.
	lastClass  map[string]snapshot.Class
	connection string
	// lookupID is the identity of the lookup definitions together.
	lookupID  string
	lookups   []string
	snapshots []string
	next      atomic.Int64 // unix nanoseconds of the next poll
	interval  time.Duration
	// pullTimeout is the deadline of the refresh of one holder.
	pullTimeout time.Duration
	armed       atomic.Bool
	versioned   bool
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
// and its failure policy (the catalog applies that one).
func defIdentity(def *controlplane.SegmentDefinition) string {
	same := *def
	same.Name, same.OnError = "", ""
	same.Mode = string(segment.Mode(def.Mode).Effective())
	same.MissingKey = string(segment.MissingKey(def.MissingKey).Effective())

	data, err := json.Marshal(&same)
	if err != nil {
		return def.Name // cannot happen for this struct: the name makes it a different identity
	}

	return string(data)
}

// carryOf is what the new generation can take over from the cache of the previous one: the built bindings, and what
// the previous one still carried for the bindings it never built. It keeps no reference to the previous cache.
func carryOf(prev *bindCache, keyOf map[string]bindKey) *carry {
	out := &carry{holders: map[string]carriedHolder{}, lookups: map[bindKey]carriedLookup{}}
	if prev == nil {
		return out
	}

	prev.mu.Lock()
	defer prev.mu.Unlock()

	if prev.carry != nil {
		maps.Copy(out.holders, prev.carry.holders)
		maps.Copy(out.lookups, prev.carry.lookups)
	}

	for key, held := range prev.built {
		if held.b == nil {
			continue
		}

		for name, holder := range held.b.holders {
			out.holders[name] = carriedHolder{holder: holder, id: held.b.ids[name]}
		}

		if held.b.res != nil {
			out.lookups[key] = carriedLookup{
				res: held.b.res, versioner: held.b.versioner, id: held.b.lookupID, versioned: held.b.versioned,
			}
		}
	}

	for name := range out.holders {
		if _, still := keyOf[name]; !still {
			delete(out.holders, name)
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

	cache.carry = carryOf(prev, cache.keyOf)

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
		connection: key.connection, holders: map[string]*snapshot.Holder{}, ids: map[string]string{},
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
			out.interval = minInterval(out.interval, interval)

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
			out.interval = minInterval(out.interval, interval)
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

	err = out.build(e, binding, key, segs, values, &buildCarry{holders: reuse, lookup: carried.lookups[key]})
	if err != nil {
		return nil, err
	}

	slices.Sort(out.lookups)
	slices.Sort(out.snapshots)

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
) error {
	if len(segs) > 0 && take.lookup.res != nil && take.lookup.id == b.lookupID {
		b.res, b.versioner, b.versioned = take.lookup.res, take.lookup.versioner, take.lookup.versioned
	} else if len(segs) > 0 {
		opts := env.options()
		opts.Segments = segs

		res, err := resolver.New(binding.Provider, opts)
		if err != nil {
			return fmt.Errorf("the resolver of connection %q: %w", key.connection, err)
		}

		for name, value := range values {
			res.SetVersion(name, value)
		}

		b.res, b.versioner = res, binding.Versioner
		b.versioned = b.versioned && binding.Versioner != nil
	}

	if len(b.snapshots) == 0 {
		return nil
	}

	b.lastClass = map[string]snapshot.Class{}

	for _, name := range b.snapshots {
		if held, ok := take.holders[name]; ok {
			b.holders[name] = held
			b.lastClass[name] = held.Status().LastError

			continue
		}

		holder, err := snapshot.New(name, binding.Source, snapshot.Options{Missing: key.missing})
		if err != nil {
			return fmt.Errorf("the segments of connection %q: %w: %w", key.connection, ErrNoSnapshotSource, err)
		}

		b.holders[name] = holder
	}

	return nil
}

func minInterval(cur, other time.Duration) time.Duration {
	if cur == 0 || other < cur {
		return other
	}

	return cur
}

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
			spread = jitter(min(b.interval, firstPollSpread))
		}

		b.next.Store(now.Add(spread).UnixNano())
		b.armed.Store(true)
	}

	return now.UnixNano() >= b.next.Load()
}

// poll reads the version keys and refreshes the holders, then schedules the next poll. Each holder has a deadline of
// its own ([DefaultPullTimeout]), so a slow set cannot starve the next one, and a pull may take longer than the
// interval. The error joins the failures that are new: a holder that fails the same way as at the last poll is not
// reported again. A holder that ran into its deadline ends the poll of the binding: the store does not answer.
func (b *segBinding) poll(ctx context.Context, now time.Time) error {
	var errs []error

	if b.versioned && b.res != nil {
		call, cancel := context.WithTimeout(ctx, b.interval)
		err := b.res.RefreshVersions(call, b.versioner)

		cancel()

		if err != nil {
			errs = append(errs, fmt.Errorf("connection %q: %w", b.connection, err))
		}
	}

	for _, name := range b.snapshots {
		holder := b.holders[name]
		call, cancel := context.WithTimeout(ctx, b.pullTimeout)
		err := holder.Refresh(call)
		timedOut := call.Err() != nil

		cancel()

		class := holder.Status().LastError

		if err != nil && class != snapshot.ClassMissing && class != b.lastClass[name] {
			errs = append(errs, fmt.Errorf("connection %q: %w", b.connection, err))
		}

		b.lastClass[name] = class

		if timedOut {
			break
		}
	}

	wait := b.interval
	if !b.loaded() {
		wait = min(wait, firstPollSpread)
	}

	b.next.Store(now.Add(wait).UnixNano())

	return errors.Join(errs...)
}

// unhealthy reports whether the store of the binding fails: a lookup call failed or its breaker is open, a holder never
// loaded or serves a set that could not be refreshed.
func (b *segBinding) unhealthy() bool {
	if b.res != nil {
		if b.res.Health().Failure != "" {
			return true
		}

		for _, seg := range b.res.Status().Segments {
			if seg.Breaker == resolver.BreakerOpen {
				return true
			}
		}
	}

	for _, held := range b.holders {
		// a set over the limit is one bad set, not a store that fails: it keeps its own answer (STALE); a set that was
		// not tried yet is not a failure either
		status := held.Status()
		if status.LastError == snapshot.ClassTooLarge || (!status.Loaded && status.LastError == snapshot.ClassNone) {
			continue
		}

		if _, answer := held.Answer(""); answer != segment.StatusHealthy {
			return true
		}
	}

	return false
}
