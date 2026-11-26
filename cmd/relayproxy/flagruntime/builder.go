package flagruntime

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
	// WithdrawnCap is the number of withdrawn flag-set names that the status keeps; the oldest are dropped.
	// Zero means [DefaultWithdrawnCap].
	WithdrawnCap int
}

// DefaultWithdrawnCap is the default size of the list of withdrawn flag sets in the status.
const DefaultWithdrawnCap = 256

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
	if opts.WithdrawnCap <= 0 {
		opts.WithdrawnCap = DefaultWithdrawnCap
	}

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
	gen     uint64
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

// Build computes the candidate for a content.
//
// Cost: the content is scanned once, O(objects), comparing checksums. Parsing is proportional to the objects whose
// checksum changed. A generation that changed only flags objects rebuilds only the flag sets that list them and
// copies the flag-set, status and key tables once (O(total flag sets) + O(total keys) map copies, no per-flag
// work). A generation that changed a flag-set document, or added or removed any object, also walks every entry
// of the documents (O(entries + keys)), reusing the flag sets that did not change by pointer. A no-op
// generation allocates a few small values only.
func (b *Builder) Build(ctx context.Context, content *Content) (*Built, error) {
	if content == nil {
		return nil, fmt.Errorf("%w: nil content", ErrInvalidContent)
	}

	stg := &stage{
		b: b, prev: b.cur, objects: b.cur.objects, docs: b.cur.docs, gen: content.Generation,
		built: &Built{flagSet: map[string]*parsedFlags{}, docSet: map[string]*parsedDoc{}},
	}

	sums := stg.scan(content)

	// The set of names changed when a name was added or removed, whatever its kind. Names are compared exactly:
	// when no name of the content is new and the counts equal those of the running state, the sets are equal.
	namesChanged := sums.addedFlags || sums.nFlags != len(stg.prev.objects)
	if namesChanged || sums.addedDocs || sums.nDocs != len(stg.prev.docs) {
		stg.removeGone(content)
	}

	if ctx.Err() != nil {
		return nil, fmt.Errorf("build: %w", ctx.Err())
	}

	full := !stg.prev.init || stg.docsChg || namesChanged

	// the version of the set of flags object names: documents are validated against one version
	namesVersion := stg.prev.namesVer
	if namesChanged {
		namesVersion++
	}

	var (
		next *state
		err  error
	)

	if full {
		next, err = stg.fullPass(ctx, namesVersion)
	} else {
		next, err = stg.incrementalPass(ctx)
	}

	if err != nil {
		return nil, err
	}

	stg.finish(content, next, namesVersion)

	return stg.built, nil
}

// scanSums are the counts found by a scan, and whether it met a name that the running state does not have.
type scanSums struct {
	nFlags     int
	nDocs      int
	addedFlags bool
	addedDocs  bool
}

// scan reads every object of the content once: it detects the changed ones and parses what is new.
func (st *stage) scan(content *Content) scanSums {
	var sums scanSums

	for name := range content.Objects {
		obj := content.Objects[name]

		switch obj.Kind {
		case KindFlags:
			sums.nFlags++

			prev, known := st.prev.objects[name]
			sums.addedFlags = sums.addedFlags || !known

			next := st.flagObject(name, &obj, prev)
			if next != prev {
				st.putObj(name, next)
				st.changed = append(st.changed, name)
				st.chFlags = append(st.chFlags, name)
			}
		case KindFlagSets:
			sums.nDocs++

			prev, known := st.prev.docs[name]
			sums.addedDocs = sums.addedDocs || !known

			next := st.docObject(name, &obj, prev)
			if next != prev {
				st.putDoc(name, next)
				st.changed = append(st.changed, name)
				st.docsChg = true
			}
		}
	}

	return sums
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

// finish completes the candidate: the state to commit, the runtime tables, the status and the counts.
func (st *stage) finish(content *Content, next *state, namesVersion uint64) {
	next.init = true
	next.namesVer = namesVersion
	st.built.next = next
	st.built.Runtime = &Runtime{
		FlagSets:   next.flagSets,
		ByKey:      next.byKey,
		AdminKeys:  map[string]struct{}{},
		Generation: content.Generation,
		Status: &Status{
			FlagSets:   next.setStatus,
			Objects:    next.objStatus,
			Entries:    next.entries,
			Withdrawn:  next.withdrawn,
			Generation: content.Generation,
		},
	}

	slices.SortFunc(st.built.Rebuilt, func(left, right Rebuilt) int { return cmp.Compare(left.Name, right.Name) })
	st.built.Stats.Rebuilt = len(st.built.Rebuilt)

	rebuiltLive := 0

	for i := range st.built.Rebuilt {
		if st.built.Rebuilt[i].New.State != StateWithdrawn {
			rebuiltLive++
		}
	}

	st.built.Stats.Reused = max(0, len(next.sets)-rebuiltLive)
}
