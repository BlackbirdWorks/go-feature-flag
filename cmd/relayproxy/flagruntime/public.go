package flagruntime

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// PublicName is the name of the public flag set: the merged view of the flag sets that are published without an API
// key. A real flag set can not have it (the name rule forbids the character), so it never meets a published name.
const PublicName = "@public"

// ErrDuplicatePublicFlag is matched by the rejection of a generation in which two public flag sets define the same flag
// key: they form one namespace.
var ErrDuplicatePublicFlag = errors.New("runtime: a flag key is defined in two public flag sets")

// PublicStatus is the status of the public flag set of a runtime.
type PublicStatus struct {
	// Sets are the names of the flag sets published without an API key, sorted.
	Sets []string `json:"sets"`
	// Flags is the number of flags of the public namespace.
	Flags int `json:"flags"`
	// Present says that a request without a key is served.
	Present bool `json:"present"`
}

// publicView is the merged view of the flag sets without an API key of one generation. It is built once per
// generation, and kept for the next one when no source changed.
type publicView struct {
	fs      *FlagSet
	sources []*FlagSet
	names   []string
}

// buildPublic merges the flag sets that were published without an API key. A flag key that two of them define is an
// error: the generation is rejected, the relay keeps the last known good.
func (st *stage) buildPublic(next *state) error {
	// the common case first: no flag set without keys, no work and no allocation
	keyless := 0

	for _, set := range next.sets {
		if len(set.spec.declKeys) == 0 {
			keyless++
		}
	}

	if keyless == 0 {
		next.public = nil

		return nil
	}

	names := make([]string, 0, keyless)

	for name, set := range next.sets {
		if len(set.spec.declKeys) == 0 {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	sources := make([]*FlagSet, 0, len(names))
	for _, name := range names {
		sources = append(sources, next.sets[name].fs)
	}

	if prev := st.prev.public; prev != nil && slices.Equal(prev.sources, sources) {
		next.public = prev

		return nil
	}

	fset, err := mergePublic(names, sources, st.now)
	if err != nil {
		return err
	}

	next.public = &publicView{fs: fset, sources: sources, names: names}

	return nil
}

// mergePublic builds the public flag set. One source shares the table of its flags: nothing is copied.
func mergePublic(names []string, sources []*FlagSet, now time.Time) (*FlagSet, error) {
	fset := &FlagSet{Name: PublicName, State: StateServing, ModifiedAt: now}

	// the view is dated by its own build: a view that is made again because a public flag set left or changed is never
	// older than the one it replaces (the flag sets that stay may have been built long ago)
	if len(sources) == 1 {
		fset.Flags, fset.Hash, fset.State = sources[0].Flags, sources[0].Hash, sources[0].State
		fset.Segments, fset.flagSigs, fset.uses = sources[0].Segments, sources[0].flagSigs, sources[0].uses
		fset.dynTerms = sources[0].dynTerms

		return fset, nil
	}

	total := 0
	for _, source := range sources {
		total += len(source.Flags)
	}

	flags := make(map[string]*flagdef.InternalFlag, total)
	owner := make(map[string]string, total)
	catalogs := map[string]*segment.Catalog{}
	sigs := map[string]uint64{}
	uses := map[string]segUse{}
	pending := 0

	for idx, source := range sources {
		for key, flag := range source.Flags {
			if first, dup := owner[key]; dup {
				return nil, fmt.Errorf("%w: %q is defined in %s and %s", ErrDuplicatePublicFlag, key, first, names[idx])
			}

			owner[key] = names[idx]
			flags[key] = flag

			noteSegments(catalogs, sigs, uses, source, key)
		}

		fset.Hash = fset.Hash*publicHashFactor + source.Hash
		fset.Rejected += source.Rejected

		switch source.State {
		case StatePending:
			pending++
		case StateDegraded:
			fset.State = StateDegraded
		case StateServing, StateWithdrawn:
		}
	}

	fset.Flags = flags
	fset.setSegmentTables(catalogs, sigs, uses)

	if pending == len(sources) {
		fset.State = StatePending
	} else if pending > 0 {
		fset.State = StateDegraded
	}

	return fset, nil
}

// setSegmentTables keeps the per flag tables of the segments that the merge found; a table with no entry stays nil.
func (f *FlagSet) setSegmentTables(
	catalogs map[string]*segment.Catalog, sigs map[string]uint64, uses map[string]segUse,
) {
	if len(catalogs) > 0 {
		f.flagCatalogs = catalogs
	}

	if len(sigs) > 0 {
		f.flagSigs = sigs
	}

	if len(uses) > 0 {
		f.uses = uses
	}
}

// noteSegments records the catalog and the signature of the flag set that a public flag comes from.
func noteSegments(
	catalogs map[string]*segment.Catalog, sigs map[string]uint64, uses map[string]segUse, source *FlagSet, key string,
) {
	if catalog := source.SegmentCatalog(); catalog != nil {
		catalogs[key] = catalog
	}

	if sig, ok := source.flagSigs[key]; ok {
		sigs[key] = sig
	}

	if use, ok := source.uses[key]; ok {
		uses[key] = use
	}
}

// publicHashFactor mixes the hashes of the sources in order.
const publicHashFactor = 1099511628211

// publicStatus describes the public flag set for the status tree.
func publicStatus(view *publicView) *PublicStatus {
	if view == nil {
		return &PublicStatus{Sets: []string{}}
	}

	return &PublicStatus{Present: true, Sets: slices.Clone(view.names), Flags: len(view.fs.Flags)}
}

// publicSet returns the flag set of a view, nil for none.
func publicSet(view *publicView) *FlagSet {
	if view == nil {
		return nil
	}

	return view.fs
}

// notePublicChange lists the public flag set among the rebuilt ones when its content changed, so that the streams of
// the clients without a key get the change like any other subscriber. A public flag set that disappeared is
// withdrawn, its clients are closed by the relay.
func (st *stage) notePublicChange(next *state) {
	before, after := st.prev.public, next.public

	switch {
	case before == nil && after == nil, before != nil && after != nil && before.fs == after.fs:
		return
	case after == nil:
		st.built.Rebuilt = append(st.built.Rebuilt, Rebuilt{
			Name: PublicName, Old: before.fs, New: &FlagSet{Name: PublicName, State: StateWithdrawn},
		})
	case before == nil:
		st.built.Rebuilt = append(st.built.Rebuilt, Rebuilt{Name: PublicName, New: after.fs})
	default:
		st.built.Rebuilt = append(st.built.Rebuilt, Rebuilt{Name: PublicName, Old: before.fs, New: after.fs})
	}
}
