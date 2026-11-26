package runtime //nolint:revive,nolintlint // the name is fixed: the package is cmd/relayproxy/runtime

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// ParseEvent tells that an object was parsed. It is delivered to [BuilderOptions.OnParse].
type ParseEvent struct {
	// Name is the object name.
	Name string
	// Kind is the object kind.
	Kind string
	// Checksum is the checksum that was parsed.
	Checksum string
}

// Stats counts the work of one build.
type Stats struct {
	// Parsed is the number of objects parsed (flags objects and flag-set documents).
	Parsed int
	// Rebuilt is the number of flag sets that got a new pointer (new, changed or withdrawn).
	Rebuilt int
	// Reused is the number of flag sets carried over by pointer.
	Reused int
}

// Built is the candidate of one [Builder.Build]. Nothing of the builder changes until [Builder.Commit].
type Built struct {
	// Runtime holds the maps, the generation and the status tree of the candidate. Snapshot, AdminKeys and
	// the reload fields of the status are the business of the caller.
	Runtime *Runtime
	next    *state
	flagSet map[string]*parsedFlags
	docSet  map[string]*parsedDoc
	// Rebuilt lists the flag sets with a new pointer, by name.
	Rebuilt []Rebuilt
	incF    []string
	decF    []string
	incD    []string
	decD    []string
	// Stats counts the work of the build.
	Stats Stats
}

// BuilderOptions configure a [Builder].
type BuilderOptions struct {
	// OnParse is called once for every object that is parsed (not for an object served from the cache).
	OnParse func(ParseEvent)
}

// Builder turns a [Content] into the candidate of a [Runtime], with structural sharing against the last
// committed build. It is not safe for concurrent use: the engine goroutine owns it.
type Builder struct {
	svc   *flagdef.Service
	cur   *state
	flags refCache[*parsedFlags]
	docs  refCache[*parsedDoc]
	opts  BuilderOptions
}

// NewBuilder returns a Builder that validates flags with svc, which the caller owns.
func NewBuilder(svc *flagdef.Service, opts BuilderOptions) *Builder {
	return &Builder{
		svc:   svc,
		cur:   emptyState(),
		flags: newRefCache[*parsedFlags](),
		docs:  newRefCache[*parsedDoc](),
		opts:  opts,
	}
}

// CachedParses returns the number of parse results held (flags objects plus documents).
func (b *Builder) CachedParses() int { return b.flags.len() + b.docs.len() }

// Commit makes a candidate the base of the next build: the parse caches drop what the content no longer
// references. A candidate that was not stored must not be committed.
func (b *Builder) Commit(built *Built) {
	b.flags.apply(built.flagSet, built.incF, built.decF)
	b.docs.apply(built.docSet, built.incD, built.decD)
	b.cur = built.next
}

// stage is the working state of one Build.
type stage struct {
	b       *Builder
	prev    *state
	objects map[string]*objState
	docs    map[string]*docState
	built   *Built
	manif   *controlplane.Manifest
	changed []string // names of objects (any kind) whose state changed or that were removed
	chFlags []string // names of flags objects that changed or were removed
	cloneO  bool
	cloneD  bool
	docsChg bool
}

func (st *stage) putObj(name string, held *objState) {
	if !st.cloneO {
		st.objects = maps.Clone(st.objects)
		st.cloneO = true
	}

	st.objects[name] = held
}

func (st *stage) delObj(name string) {
	if !st.cloneO {
		st.objects = maps.Clone(st.objects)
		st.cloneO = true
	}

	delete(st.objects, name)
}

func (st *stage) putDoc(name string, held *docState) {
	if !st.cloneD {
		st.docs = maps.Clone(st.docs)
		st.cloneD = true
	}

	st.docs[name] = held
}

func (st *stage) delDoc(name string) {
	if !st.cloneD {
		st.docs = maps.Clone(st.docs)
		st.cloneD = true
	}

	delete(st.docs, name)
}

// Build computes the candidate for a content. The cost is proportional to the objects that changed (parse and
// table), the flag sets that use them (rebuild), and, only when a flag-set document or the set of object names
// changed, the flag sets and keys (entry pass). A map clone of the flag-set and key tables is paid once per
// generation that changes a flag set.
func (b *Builder) Build(ctx context.Context, content *Content) (*Built, error) {
	if content == nil {
		return nil, fmt.Errorf("%w: nil content", ErrInvalidContent)
	}

	stg := &stage{
		b: b, prev: b.cur, objects: b.cur.objects, docs: b.cur.docs,
		built: &Built{flagSet: map[string]*parsedFlags{}, docSet: map[string]*parsedDoc{}},
	}

	sig, nFlags, nDocs := stg.scan(content)

	sigChanged := sig != stg.prev.sig || nFlags != len(stg.prev.objects)
	if sigChanged || nDocs != len(stg.prev.docs) {
		stg.removeGone(content)
	}

	if ctx.Err() != nil {
		return nil, fmt.Errorf("build: %w", ctx.Err())
	}

	full := !stg.prev.init || stg.docsChg || sigChanged

	var (
		next *state
		err  error
	)

	if full {
		next, err = stg.fullPass(ctx, sig)
	} else {
		next, err = stg.incrementalPass(ctx)
	}

	if err != nil {
		return nil, err
	}

	next.init = true
	next.sig = sig
	stg.built.next = next
	stg.built.Runtime = &Runtime{
		FlagSets:   next.flagSets,
		ByKey:      next.byKey,
		AdminKeys:  map[string]struct{}{},
		Generation: content.Generation,
		Status: &Status{
			FlagSets:   next.setStatus,
			Objects:    next.objStatus,
			Entries:    next.entries,
			Generation: content.Generation,
		},
	}

	slices.SortFunc(stg.built.Rebuilt, func(left, right Rebuilt) int { return cmp.Compare(left.Name, right.Name) })
	stg.built.Stats.Rebuilt = len(stg.built.Rebuilt)

	rebuiltLive := 0

	for i := range stg.built.Rebuilt {
		if stg.built.Rebuilt[i].New.State != StateWithdrawn {
			rebuiltLive++
		}
	}

	stg.built.Stats.Reused = max(0, len(next.sets)-countTombs(next.sets)-rebuiltLive)

	return stg.built, nil
}

func countTombs(sets map[string]*setState) int {
	count := 0

	for _, set := range sets {
		if set.tomb {
			count++
		}
	}

	return count
}

// scan reads every object of the content once: it detects the changed ones and parses what is new.
func (st *stage) scan(content *Content) (uint64, int, int) {
	var (
		sig    uint64
		nFlags int
		nDocs  int
	)

	for name := range content.Objects {
		obj := content.Objects[name]

		switch obj.Kind {
		case KindFlags:
			nFlags++
			sig ^= hashString(name)

			prev := st.prev.objects[name]

			next := st.flagObject(name, &obj, prev)
			if next != prev {
				st.putObj(name, next)
				st.changed = append(st.changed, name)
				st.chFlags = append(st.chFlags, name)
			}
		case KindFlagSets:
			nDocs++

			prev := st.prev.docs[name]

			next := st.docObject(name, &obj, prev)
			if next != prev {
				st.putDoc(name, next)
				st.changed = append(st.changed, name)
				st.docsChg = true
			}
		}
	}

	return sig, nFlags, nDocs
}

// removeGone drops the objects that the content no longer lists.
func (st *stage) removeGone(content *Content) {
	for name, os := range st.prev.objects {
		if o, ok := content.Objects[name]; !ok || o.Kind != KindFlags {
			st.delObj(name)
			st.changed = append(st.changed, name)
			st.chFlags = append(st.chFlags, name)
			st.release(os.refKey, false)
		}
	}

	for name, ds := range st.prev.docs {
		if o, ok := content.Objects[name]; !ok || o.Kind != KindFlagSets {
			st.delDoc(name)
			st.changed = append(st.changed, name)
			st.docsChg = true
			st.release(ds.refKey, true)
		}
	}
}

func (st *stage) hold(key string, doc bool) {
	if key == "" {
		return
	}

	if doc {
		st.built.incD = append(st.built.incD, key)
	} else {
		st.built.incF = append(st.built.incF, key)
	}
}

func (st *stage) release(key string, doc bool) {
	if key == "" {
		return
	}

	if doc {
		st.built.decD = append(st.built.decD, key)
	} else {
		st.built.decF = append(st.built.decF, key)
	}
}

func (st *stage) lookupFlags(key string) (*parsedFlags, bool) {
	if v, ok := st.built.flagSet[key]; ok {
		return v, true
	}

	if entry := st.b.flags.lookup(key); entry != nil {
		return entry.value, true
	}

	return nil, false
}

func (st *stage) lookupDoc(key string) (*parsedDoc, bool) {
	if v, ok := st.built.docSet[key]; ok {
		return v, true
	}

	if entry := st.b.docs.lookup(key); entry != nil {
		return entry.value, true
	}

	return nil, false
}

func (st *stage) emit(name, kind, checksum string) {
	st.built.Stats.Parsed++

	if st.b.opts.OnParse != nil {
		st.b.opts.OnParse(ParseEvent{Name: name, Kind: kind, Checksum: checksum})
	}
}

// patchObjectStatus clones the object status table and applies the changes of the stage.
func (st *stage) objectStatus() map[string]*ObjectStatus {
	if len(st.changed) == 0 {
		return st.prev.objStatus
	}

	out := maps.Clone(st.prev.objStatus)

	for _, name := range st.changed {
		switch {
		case st.objects[name] != nil:
			out[name] = st.objects[name].status
		case st.docs[name] != nil:
			out[name] = st.docs[name].status
		default:
			delete(out, name)
		}
	}

	return out
}
