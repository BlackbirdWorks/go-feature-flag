package runtime //nolint:revive,nolintlint // the name is fixed: the package is cmd/relayproxy/runtime

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// incrementalPass handles a generation that changed flags objects only: the documents and the set of object
// names are the same, so the entries, keys and sources are the same. Only the flag sets that list a changed
// object are rebuilt.
func (st *stage) incrementalPass(ctx context.Context) (*state, error) {
	next := *st.prev
	next.objects, next.docs = st.objects, st.docs
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
	hash     uint64
	rejected int
	loaded   int
	degraded bool
}

// summarize resolves the sources of a definition against the objects of the stage.
func (st *stage) summarize(spec *setSpec, notes []Problem) (*inputSummary, []*objState) {
	inputs := make([]*objState, len(spec.sources))
	sum := &inputSummary{problems: slices.Clone(notes), hash: fnvOffset, degraded: len(notes) > 0}

	for idx, source := range spec.sources {
		held := st.objects[source]
		inputs[idx] = held

		if held == nil {
			sum.problems = append(sum.problems, Problem{
				Code: controlplane.CodeUnresolvedSource, Subject: source,
				Reason: "the source is not an object of kind flags in the content",
			})
			sum.degraded = true
			sum.hash = mixUint(sum.hash, 0)

			continue
		}

		sum.hash = mixUint(sum.hash, held.hash)
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

// mergeFlags builds the flag table of a flag set. A flag set with one source shares the map of the source.
func mergeFlags(sum *inputSummary, inputs []*objState) map[string]*flagdef.InternalFlag {
	switch sum.loaded {
	case 0:
		return nil
	case 1:
		return sum.last.flags
	}

	total := 0

	for _, held := range inputs {
		if held != nil {
			total += len(held.flags)
		}
	}

	flags := make(map[string]*flagdef.InternalFlag, total)

	for _, held := range inputs {
		if held != nil {
			maps.Copy(flags, held.flags) // a later source wins on a duplicate flag key
		}
	}

	return flags
}

// buildSet builds a flag set from its definition and the objects of the stage.
func (st *stage) buildSet(spec *setSpec, notes []Problem) *setState {
	sum, inputs := st.summarize(spec, notes)
	flags := mergeFlags(sum, inputs)

	phase := StateServing

	switch {
	case len(spec.sources) > 0 && sum.loaded == 0:
		phase = StatePending
	case sum.degraded || sum.rejected > 0:
		phase = StateDegraded
	}

	fset := &FlagSet{
		Flags: flags, Name: spec.name, State: phase, Hash: sum.hash, APIKeys: len(spec.keys), Rejected: sum.rejected,
	}

	return &setState{
		spec: spec, fs: fset, inputs: inputs, notes: notes,
		status: &FlagSetStatus{
			Name: spec.name, State: phase, Sources: spec.sources, Problems: sum.problems, Flags: len(flags),
			APIKeys: len(spec.keys), Rejected: sum.rejected,
		},
	}
}
