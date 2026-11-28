package flagruntime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

// DefaultPullTimeout is the deadline of the refresh of one snapshot segment, much longer than the poll interval: a big
// set may take longer than the interval to pull, and a slow set never takes the time of another.
const DefaultPullTimeout = time.Minute

// DefaultVersionInterval is how often the version key of a segment is polled when its definition gives no interval.
const DefaultVersionInterval = 5 * time.Second

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
	// Source reads the version and the members of a set, for the definitions in the snapshot mode; nil when the store
	// cannot, and then a snapshot definition is not served.
	Source segment.SnapshotSource
}

// SegmentBinder gives a generation the providers of the process: one connection serves the definitions of any number
// of generations, and nothing here depends on a kind of store. Bind must not block and must not fail for a store that
// is down: the lookups of such a binding are Unknown until the store is back.
type SegmentBinder interface {
	// Bind returns the binding of the definitions on the named connection. A missing key of the store is read as
	// missing says.
	Bind(connection string, missing segment.MissingKey, defs []BoundDefinition) (*Binding, error)
}

// SegmentView is the dynamic segments of one generation of a flag set: each served definition is bound to the binding
// of its segments object (see [segBinding]), which every flag set and every generation that uses the object shares. The
// view is built off to the side with the flag set, swapped in with it and carried over by pointer while the flag set
// is. It holds no connection: dropping it releases what nothing else references.
type SegmentView struct {
	byName map[string]*viewGroup
	groups []*viewGroup
	// traced is true when the relay exports traces: only then is the lookup step a span.
	traced bool
}

// viewGroup is the segments of one binding that the flag set serves.
type viewGroup struct {
	bind      *segBinding
	lookups   []string
	snapshots []string
}

// SegmentEnv is what a build needs to bind dynamic segments: the process-level binder, the resolver settings of the
// bootstrap file and the observer of the resolvers. The zero value serves no dynamic segment.
type SegmentEnv struct {
	// Binder gives the providers. Nil means no dynamic segment is served.
	Binder SegmentBinder
	// Observer (optional) receives what the resolvers do; one value for the process.
	Observer resolver.Observer
	// ObserverFor (optional) gives the observer of the resolvers of one connection; it wins over Observer.
	ObserverFor func(connection string) resolver.Observer
	// Resolver holds the settings of the resolvers. Nil means the defaults.
	Resolver *bootstrap.SegmentResolver
	// Traced is true when the relay exports traces; the lookups of a request are then a span. Off, they cost nothing.
	Traced bool
	// PullTimeout is the deadline of the refresh of one snapshot segment (the version read and the pull of the
	// members). Zero means [DefaultPullTimeout].
	PullTimeout time.Duration
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

func (e *SegmentEnv) options(connection string) *resolver.Options {
	opts := &resolver.Options{Observer: e.Observer}
	if e.ObserverFor != nil {
		opts.Observer = e.ObserverFor(connection)
	}

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

// newSegmentView groups the served names of a flag set by binding. The names that have no binding are returned as
// errors by name; they stay unserved. The live answers of the snapshot segments are returned for the catalog.
func newSegmentView(bindings map[string]*segBinding) (*SegmentView, map[string]segment.Live) {
	byBinding := map[*segBinding]*viewGroup{}
	view := &SegmentView{byName: map[string]*viewGroup{}}
	live := map[string]segment.Live{}

	for name, b := range bindings {
		group := byBinding[b]
		if group == nil {
			group = &viewGroup{bind: b}
			byBinding[b] = group
			view.groups = append(view.groups, group)
		}

		if holder, ok := b.holders[name]; ok {
			group.snapshots = append(group.snapshots, name)
			live[name] = holder

			continue
		}

		group.lookups = append(group.lookups, name)
		view.byName[name] = group
	}

	slices.SortFunc(view.groups, func(a, b *viewGroup) int {
		if a.bind.connection != b.bind.connection {
			return compareStrings(a.bind.connection, b.bind.connection)
		}

		return compareStrings(a.first(), b.first())
	})

	for _, group := range view.groups {
		slices.Sort(group.lookups)
		slices.Sort(group.snapshots)
	}

	if len(view.groups) == 0 {
		return nil, nil
	}

	return view, live
}

func (g *viewGroup) first() string {
	names := slices.Concat(g.lookups, g.snapshots)
	if len(names) == 0 {
		return ""
	}

	return slices.Min(names)
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

// pollInterval is how often the version key of a definition is read: its own interval, and the default only for a
// definition that gives none.
func pollInterval(def *controlplane.SegmentDefinition) time.Duration {
	parsed, err := time.ParseDuration(versionOf(def).Interval)
	if err == nil && parsed > 0 {
		return parsed
	}

	return DefaultVersionInterval
}

// debounceOf is how long a refresh of a snapshot definition waits for a set that changes while it is pulled: the
// definition's own value, zero (off) when it gives none or one that does not parse (the document was validated).
func debounceOf(def *controlplane.SegmentDefinition) time.Duration {
	parsed, err := time.ParseDuration(versionOf(def).Debounce)
	if err != nil || parsed < 0 {
		return 0
	}

	return parsed
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

	if len(v.groups) == 1 && v.serveAll(lookups) {
		err := v.groups[0].bind.res.Contains(ctx, lookups)
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

		err := group.bind.res.Contains(ctx, sub)
		if err != nil {
			errs = append(errs, err)
		}

		for n, i := range idx {
			lookups[i].State = sub[n].State
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
		if res := group.bind.res; res != nil {
			status := res.Status()
			failing := res.Health().Failure != ""

			for _, name := range group.lookups {
				if failing || status.Segments[name].Breaker == resolver.BreakerOpen {
					storeError++
				} else {
					healthy++
				}
			}
		}

		for _, name := range group.snapshots {
			if _, answer := group.bind.holders[name].Answer(""); answer == segment.StatusHealthy {
				healthy++
			} else {
				storeError++
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
		if !slices.Contains(names, group.bind.connection) {
			names = append(names, group.bind.connection)
		}
	}

	slices.Sort(names)

	return names
}

// serveAll reports whether the view serves the segment of every lookup. It does not allocate.
func (v *SegmentView) serveAll(lookups []segment.Lookup) bool {
	for i := range lookups {
		if _, ok := v.byName[lookups[i].Segment]; !ok {
			return false
		}
	}

	return true
}

// Wait is the clock seam of the watcher of the versions: it returns after d, or with the error of ctx when it ends
// first. Production waits on a timer; a test drives the ticks.
type Wait func(ctx context.Context, d time.Duration) error
