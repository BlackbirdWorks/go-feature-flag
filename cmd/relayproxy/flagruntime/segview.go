package flagruntime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

// DefaultVersionInterval is how often the version key of a segment is polled when its definition gives no interval.
const DefaultVersionInterval = 5 * time.Second

// ErrSnapshotNotServed is the reason of a definition in the snapshot mode when the relay serves dynamic segments but
// not that mode yet.
var ErrSnapshotNotServed = errors.New("the snapshot mode is not served yet by this relay: use mode: lookup")

// BoundDefinition is what a store needs to serve one dynamic segment.
type BoundDefinition struct {
	// Name is the name of the segment.
	Name string
	// Key is the key of the set, or the prefix of the member keys; empty means the default of the store.
	Key string
	// VersionKey is the key that holds the version of the segment, empty for none.
	VersionKey string
	// Layout is how the members are stored.
	Layout segment.Layout
}

// Binding is what a connection serves for a set of definitions.
type Binding struct {
	// Provider answers the lookups of the definitions. It must honour the context.
	Provider segment.Provider
	// Versioner reads the version keys of the definitions, nil when the store cannot.
	Versioner segment.Versioner
}

// SegmentBinder gives a generation the providers of the process: one connection serves the definitions of any number
// of generations, and nothing here depends on a kind of store. Bind must not block and must not fail for a store that
// is down: the lookups of such a binding are Unknown until the store is back.
type SegmentBinder interface {
	// Bind returns the binding of the definitions on the named connection. A missing key of the store is read as
	// missing says.
	Bind(connection string, missing segment.MissingKey, defs []BoundDefinition) (*Binding, error)
}

// SegmentView is the dynamic segments of one generation of a flag set: the definitions bound to the providers of the
// process, each connection behind a resolver (cache, coalescing, breaker) that belongs to this view. It is built off
// to the side with the flag set, swapped in with it and carried over by pointer while the flag set is. It holds no
// connection: dropping it releases the resolvers and nothing else.
type SegmentView struct {
	byName map[string]*viewGroup
	groups []*viewGroup
}

// viewGroup is the segments of one connection that share a resolver.
type viewGroup struct {
	res        *resolver.Resolver
	versioner  segment.Versioner
	connection string
	segments   []string
	next       atomic.Int64 // unix nanoseconds of the next poll of the version keys
	interval   time.Duration
	versioned  bool
}

// SegmentEnv is what a build needs to bind dynamic segments: the process-level binder, the resolver settings of the
// bootstrap file and the observer of the resolvers. The zero value serves no dynamic segment.
type SegmentEnv struct {
	// Binder gives the providers. Nil means no dynamic segment is served.
	Binder SegmentBinder
	// Observer (optional) receives what the resolvers do; one value for the process.
	Observer resolver.Observer
	// Resolver holds the settings of the resolvers. Nil means the defaults.
	Resolver *bootstrap.SegmentResolver
}

func (e *SegmentEnv) enabled() bool { return e != nil && e.Binder != nil }

func durationOr(p *bootstrap.Duration) time.Duration {
	if p == nil {
		return 0
	}

	return time.Duration(*p)
}

func intOr(p *int) int {
	if p == nil {
		return 0
	}

	return *p
}

func boolOr(p *bool) bool { return p != nil && *p }

func (e *SegmentEnv) options() *resolver.Options {
	opts := &resolver.Options{Observer: e.Observer}
	if e.Resolver == nil {
		return opts
	}

	r := e.Resolver
	opts.CallTimeout = durationOr(r.CallTimeout)
	opts.ShutdownGrace = durationOr(r.ShutdownGrace)
	opts.MaxOverdue = intOr(r.MaxOverdue)
	opts.MaxIDBytes = intOr(r.MaxIDBytes)
	opts.Default = e.tuning(nil)

	return opts
}

// tuning is the settings of a segment: the process default and what the cache block of the definition says.
func (e *SegmentEnv) tuning(cache *controlplane.SegmentCache) resolver.SegmentOptions {
	var out resolver.SegmentOptions

	if e.Resolver != nil {
		def := &e.Resolver.Default
		out = resolver.SegmentOptions{
			Normalize: resolver.Normalization(def.Normalize), TTL: durationOr(def.TTL),
			NegativeTTL: durationOr(def.NegativeTTL), MaxEntries: intOr(def.MaxEntries),
			Shards: intOr(def.Shards), BreakerFailures: intOr(def.BreakerFailures),
			BreakerCooldown: durationOr(def.BreakerCooldown), NoCache: boolOr(def.NoCache),
			NoBreaker: boolOr(def.NoBreaker),
		}
	}

	if cache != nil {
		if cache.MaxEntries > 0 {
			out.MaxEntries = cache.MaxEntries
		}

		ttl, err := time.ParseDuration(cache.TTL)
		if err == nil && ttl > 0 {
			out.TTL = ttl
		}
	}

	return out
}

type groupKey struct {
	connection string
	missing    segment.MissingKey
}

// newSegmentView binds the served definitions of a flag set: lookup mode, a connection the binder knows. The
// definitions that cannot be bound are returned as errors by name; they stay unserved.
func newSegmentView(env *SegmentEnv, defs []*controlplane.SegmentDefinition) (*SegmentView, map[string]error) {
	byGroup := map[groupKey][]*controlplane.SegmentDefinition{}

	for _, def := range defs {
		key := groupKey{def.Connection, segment.MissingKey(def.MissingKey).Effective()}
		byGroup[key] = append(byGroup[key], def)
	}

	keys := make([]groupKey, 0, len(byGroup))
	for key := range byGroup {
		keys = append(keys, key)
	}

	slices.SortFunc(keys, func(a, b groupKey) int {
		if a.connection != b.connection {
			return compareStrings(a.connection, b.connection)
		}

		return compareStrings(string(a.missing), string(b.missing))
	})

	view := &SegmentView{byName: map[string]*viewGroup{}}
	failed := map[string]error{}

	for _, key := range keys {
		group, err := env.bindGroup(key, byGroup[key])
		if err != nil {
			for _, def := range byGroup[key] {
				failed[def.Name] = err
			}

			continue
		}

		view.groups = append(view.groups, group)

		for _, name := range group.segments {
			view.byName[name] = group
		}
	}

	if len(view.groups) == 0 {
		return nil, failed
	}

	return view, failed
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}

	return 0
}

func (e *SegmentEnv) bindGroup(key groupKey, defs []*controlplane.SegmentDefinition) (*viewGroup, error) {
	bound := make([]BoundDefinition, 0, len(defs))
	segs := make(map[string]resolver.SegmentOptions, len(defs))
	group := &viewGroup{connection: key.connection, interval: DefaultVersionInterval}

	var values map[string]string

	for _, def := range defs {
		bound = append(bound, BoundDefinition{
			Name: def.Name, Key: def.Key, Layout: segment.Layout(def.Layout), VersionKey: versionKey(def),
		})

		opts := e.tuning(def.Cache)
		opts.Versioned = versionKey(def) != ""
		segs[def.Name] = opts
		group.segments = append(group.segments, def.Name)

		if opts.Versioned {
			group.versioned = true

			interval, err := time.ParseDuration(versionOf(def).Interval)
			if err == nil && interval > 0 {
				group.interval = min(group.interval, interval)
			}
		}

		if def.Version != nil && def.Version.Value != "" {
			if values == nil {
				values = map[string]string{}
			}

			values[def.Name] = def.Version.Value
		}
	}

	binding, err := e.Binder.Bind(key.connection, key.missing, bound)
	if err != nil {
		return nil, fmt.Errorf("bind the segments of connection %q: %w", key.connection, err)
	}

	opts := e.options()
	opts.Segments = segs

	res, err := resolver.New(binding.Provider, opts)
	if err != nil {
		return nil, fmt.Errorf("the resolver of connection %q: %w", key.connection, err)
	}

	for name, value := range values {
		res.SetVersion(name, value)
	}

	group.res, group.versioner = res, binding.Versioner
	group.versioned = group.versioned && binding.Versioner != nil

	slices.Sort(group.segments)

	return group, nil
}

func versionOf(def *controlplane.SegmentDefinition) *controlplane.SegmentVersion {
	if def.Version == nil {
		return &controlplane.SegmentVersion{}
	}

	return def.Version
}

func versionKey(def *controlplane.SegmentDefinition) string { return versionOf(def).Key }

// Serves reports whether the view answers the segment.
func (v *SegmentView) Serves(name string) bool {
	if v == nil {
		return false
	}

	_, ok := v.byName[name]

	return ok
}

// Contains implements [segment.Provider]: the lookups go to the resolver of their segment, one call for each
// connection that they reach. A lookup of a segment the view does not serve stays Unknown.
func (v *SegmentView) Contains(ctx context.Context, lookups []segment.Lookup) error {
	if v == nil || len(lookups) == 0 {
		return nil
	}

	if len(v.groups) == 1 {
		err := v.groups[0].res.Contains(ctx, lookups)
		if err != nil {
			return fmt.Errorf("segment lookups: %w", err)
		}

		return nil
	}

	parts := make(map[*viewGroup][]int, len(v.groups))

	for i := range lookups {
		if group := v.byName[lookups[i].Segment]; group != nil {
			parts[group] = append(parts[group], i)
		}
	}

	var errs []error

	for _, group := range v.groups {
		idx := parts[group]
		if len(idx) == 0 {
			continue
		}

		sub := make([]segment.Lookup, len(idx))
		for n, i := range idx {
			sub[n] = lookups[i]
		}

		err := group.res.Contains(ctx, sub)
		if err != nil {
			errs = append(errs, err)
		}

		for n, i := range idx {
			lookups[i].State = sub[n].State
		}
	}

	return errors.Join(errs...)
}

// RefreshVersions polls the version keys that are due at now: one step of the watcher, off the request path. The
// error joins the failures of the groups; a segment whose version cannot be read keeps its version.
func (v *SegmentView) RefreshVersions(ctx context.Context, now time.Time) error {
	if v == nil {
		return nil
	}

	var errs []error

	for _, group := range v.groups {
		if !group.versioned || now.UnixNano() < group.next.Load() {
			continue
		}

		group.next.Store(now.Add(group.interval).UnixNano())

		err := group.res.RefreshVersions(ctx, group.versioner)
		if err != nil {
			errs = append(errs, fmt.Errorf("connection %q: %w", group.connection, err))
		}
	}

	return errors.Join(errs...)
}

// Health returns the number of served segments that are healthy, then the number whose store failed on the last call
// or whose breaker is open.
func (v *SegmentView) Health() (int, int) {
	if v == nil {
		return 0, 0
	}

	var healthy, storeError int

	for _, group := range v.groups {
		status := group.res.Status()
		failing := group.res.Health().Failure != ""

		for _, name := range group.segments {
			if failing || status.Segments[name].Breaker == resolver.BreakerOpen {
				storeError++
			} else {
				healthy++
			}
		}
	}

	return healthy, storeError
}

// Connections returns the names of the connections that the view uses, sorted.
func (v *SegmentView) Connections() []string {
	if v == nil {
		return nil
	}

	var names []string

	for _, group := range v.groups {
		if !slices.Contains(names, group.connection) {
			names = append(names, group.connection)
		}
	}

	slices.Sort(names)

	return names
}

// RunSegmentVersions polls the version keys of the dynamic segments of the current runtime on every tick, off the
// request path, until ctx ends. A view is polled when its interval is due by the time of the tick (the time that the
// ticker gives, not the clock package: the interval is real time), so the tick only sets the resolution.
// The loop is owned by the run context of the relay.
func RunSegmentVersions(ctx context.Context, holder *Holder, ticks <-chan time.Time, onError func(error)) {
	for {
		var now time.Time

		select {
		case <-ctx.Done():
			return
		case now = <-ticks:
		}

		for _, set := range holder.Load().FlagSets {
			if set.Segments == nil || set.Segments.View == nil {
				continue
			}

			err := set.Segments.View.RefreshVersions(ctx, now)
			if err != nil && onError != nil {
				onError(err)
			}
		}
	}
}
