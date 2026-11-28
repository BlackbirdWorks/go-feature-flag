package flagruntime

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// incrementalPass handles a generation that changed flags objects only: the documents and the set of object
// names are the same, so the entries, keys and sources are the same. Only the flag sets that list a changed
// object are rebuilt.
func (st *stage) incrementalPass(ctx context.Context) (*state, error) {
	next := *st.prev
	next.objects, next.docs, next.segs = st.objects, st.docs, st.segs
	next.objStatus = st.objectStatus()

	affected := map[string]struct{}{}

	for _, name := range st.chFlags {
		for _, set := range st.prev.rev[name] {
			affected[set] = struct{}{}
		}
	}

	if len(affected) == 0 {
		return &next, nil
	}

	next.sets = maps.Clone(st.prev.sets)
	next.flagSets = maps.Clone(st.prev.flagSets)
	next.setStatus = maps.Clone(st.prev.setStatus)
	next.byKey = maps.Clone(st.prev.byKey)

	names := slices.Sorted(maps.Keys(affected))
	for idx, name := range names {
		if idx%checkEvery == 0 && ctx.Err() != nil {
			return nil, fmt.Errorf("build: %w", ctx.Err())
		}

		old := st.prev.sets[name]
		set := st.buildSet(old.spec, old.notes)
		next.sets[name] = set
		next.flagSets[name] = set.fs
		next.setStatus[name] = set.status

		for _, key := range set.spec.keys {
			next.byKey[key] = set.fs
		}

		st.built.Rebuilt = append(st.built.Rebuilt, Rebuilt{Name: name, Old: old.fs, New: set.fs})
	}

	return &next, nil
}

// inputSummary is what a flag set learns from its sources.
type inputSummary struct {
	last     *objState
	problems []Problem
	rejected int
	loaded   int
	degraded bool
}

// summarize resolves the sources of a definition against the objects of the stage.
func (st *stage) summarize(spec *setSpec, notes []Problem) (*inputSummary, []*objState) {
	inputs := make([]*objState, len(spec.sources))
	sum := &inputSummary{problems: slices.Clone(notes), degraded: len(notes) > 0}

	for idx, source := range spec.sources {
		held := st.objects[source]
		inputs[idx] = held

		if held == nil {
			sum.problems = append(sum.problems, Problem{
				Code: controlplane.CodeUnresolvedSource, Subject: source,
				Reason: "the source is not an object of kind flags in the content",
			})
			sum.degraded = true

			continue
		}

		sum.rejected += len(held.rejected)

		if held.state != ObjectOK {
			sum.degraded = true
			sum.problems = append(sum.problems, Problem{Code: CodeSourceDegraded, Subject: source, Reason: held.reason})
		}

		if held.loaded {
			sum.loaded++
			sum.last = held
		}
	}

	return sum, inputs
}

// mergeFlags builds the flag table of a flag set and its content hash. A flag set with one source shares the
// map of the source. The hash combines the effective flags (key and content of each), so it does not depend on
// the order of the sources, on checksums, on formats or on history.
func mergeFlags(sum *inputSummary, inputs []*objState) (map[string]*flagdef.InternalFlag, uint64) {
	switch sum.loaded {
	case 0:
		return nil, 0
	case 1:
		return sum.last.flags, sum.last.content
	}

	total := 0

	for _, held := range inputs {
		if held != nil {
			total += len(held.flags)
		}
	}

	flags := make(map[string]*flagdef.InternalFlag, total)
	hashes := make(map[string]uint64, total)

	for _, held := range inputs {
		if held != nil {
			maps.Copy(flags, held.flags) // a later source wins on a duplicate flag key
			maps.Copy(hashes, held.hashes)
		}
	}

	return flags, tableHash(hashes)
}

// buildSet builds a flag set from its definition and the objects of the stage.
func (st *stage) buildSet(spec *setSpec, notes []Problem) *setState {
	sum, inputs := st.summarize(spec, notes)
	flags, hash := mergeFlags(sum, inputs)

	phase := StateServing

	switch {
	case len(spec.sources) > 0 && sum.loaded == 0:
		phase = StatePending
	case sum.degraded || sum.rejected > 0:
		phase = StateDegraded
	}

	fset := &FlagSet{
		Flags: flags, Name: spec.name, State: phase, Hash: hash, APIKeys: len(spec.keys), Rejected: sum.rejected,
		ModifiedAt: st.now,
	}

	return &setState{
		spec: spec, fs: fset, inputs: inputs, notes: notes,
		status: &FlagSetStatus{
			Name: spec.name, State: phase, Sources: spec.sources, Problems: sum.problems, Flags: len(flags),
			APIKeys: len(spec.keys), Rejected: sum.rejected,
		},
	}
}

// frozenSet keeps the flags a flag set had when its entry became invalid: the sources are not read again, so a
// partial list of sources is never served.
func frozenSet(old *setState, spec *setSpec, notes []Problem, now time.Time) *setState {
	// the fields are copied one by one: a FlagSet holds a memo that must not be copied
	fset := &FlagSet{
		Flags: old.fs.Flags, Name: old.fs.Name, State: old.fs.State, Hash: old.fs.Hash, APIKeys: len(spec.keys),
		Rejected: old.fs.Rejected, ModifiedAt: now,
	}

	if fset.State == StateServing {
		fset.State = StateDegraded
	}

	status := *old.status
	status.State, status.APIKeys, status.Problems = fset.State, len(spec.keys), slices.Clone(notes)

	return &setState{spec: spec, fs: fset, status: &status, inputs: old.inputs, notes: notes, frozen: true}
}

// placeholderSet routes the keys of an invalid entry that never had a valid definition to a flag set that is
// pending and has no flags, so a valid key reaches the "not available" answer.
func placeholderSet(spec *setSpec, notes []Problem, now time.Time) *setState {
	return &setState{
		spec: spec, notes: notes, placeholder: true,
		fs: &FlagSet{Name: spec.name, State: StatePending, APIKeys: len(spec.keys), ModifiedAt: now},
		status: &FlagSetStatus{
			Name: spec.name, State: StatePending, Sources: spec.sources, Problems: slices.Clone(notes),
			APIKeys: len(spec.keys),
		},
	}
}
