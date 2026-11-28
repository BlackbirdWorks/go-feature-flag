package flagruntime

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	// lastClass is the class of the last failure of each holder that was reported, so a state is reported once. Only the
	// loop that polls touches it.
	lastClass  map[string]snapshot.Class
	connection string
	lookups    []string
	snapshots  []string
	next       atomic.Int64 // unix nanoseconds of the next poll
	interval   time.Duration
	armed      atomic.Bool
	versioned  bool
}

// bindCache builds the bindings of one segments object when a flag set first uses them, and keeps them for the life
// of the object.
type bindCache struct {
	env   *SegmentEnv
	defs  map[bindKey][]*controlplane.SegmentDefinition
	keyOf map[string]bindKey
	built map[bindKey]bound
	mu    sync.Mutex
}

type bound struct {
	b   *segBinding
	err error
}

// newBindCache groups the definitions of a document that the relay can serve. It binds nothing.
func newBindCache(env *SegmentEnv, doc *controlplane.SegmentsDocument, rejected map[string]struct{}) *bindCache {
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

	b, err := c.env.bindObject(key, c.defs[key])
	c.built[key] = bound{b, err}

	return b, err
}

// isSnapshot reports whether the definition is served from a held set.
func isSnapshot(def *controlplane.SegmentDefinition) bool {
	return segment.Mode(def.Mode).Effective() == segment.ModeSnapshot
}

func (e *SegmentEnv) bindObject(key bindKey, defs []*controlplane.SegmentDefinition) (*segBinding, error) {
	bounds := make([]BoundDefinition, 0, len(defs))
	segs := map[string]resolver.SegmentOptions{}
	out := &segBinding{connection: key.connection, holders: map[string]*snapshot.Holder{}}
	values := map[string]string{}

	for _, def := range defs {
		bounds = append(bounds, BoundDefinition{
			Name: def.Name, Key: def.Key, Layout: segment.Layout(def.Layout), VersionKey: versionKey(def),
		})

		interval := pollInterval(def)
		if def.Version == nil || def.Version.Key == "" {
			interval = DefaultVersionInterval
		}

		if isSnapshot(def) {
			out.snapshots = append(out.snapshots, def.Name)
			out.interval = minInterval(out.interval, interval)

			continue
		}

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

	err = out.build(e, binding, key, segs, values)
	if err != nil {
		return nil, err
	}

	slices.Sort(out.lookups)
	slices.Sort(out.snapshots)

	return out, nil
}

func (b *segBinding) build(
	env *SegmentEnv, binding *Binding, key bindKey, segs map[string]resolver.SegmentOptions, values map[string]string,
) error {
	if len(segs) > 0 {
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

// loaded reports whether every holder has a set (or knows that the segment is missing).
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

// poll reads the version keys and refreshes the holders, then schedules the next poll. The error joins the failures
// that are new: a holder that fails the same way as at the last poll is not reported again.
func (b *segBinding) poll(ctx context.Context, now time.Time) error {
	var errs []error

	if b.versioned && b.res != nil {
		err := b.res.RefreshVersions(ctx, b.versioner)
		if err != nil {
			errs = append(errs, fmt.Errorf("connection %q: %w", b.connection, err))
		}
	}

	for _, name := range b.snapshots {
		holder := b.holders[name]

		err := holder.Refresh(ctx)
		class := holder.Status().LastError

		if err != nil && class != snapshot.ClassMissing && class != b.lastClass[name] {
			errs = append(errs, fmt.Errorf("connection %q: %w", b.connection, err))
		}

		b.lastClass[name] = class
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

	for _, h := range b.holders {
		if _, status := h.Answer(""); status != segment.StatusHealthy {
			return true
		}
	}

	return false
}
