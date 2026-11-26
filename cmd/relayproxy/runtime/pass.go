package runtime //nolint:revive,nolintlint // the name is fixed: the package is cmd/relayproxy/runtime

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// checkEvery is the number of flag sets between two checks of the context.
const checkEvery = 128

// candidate is a flag-set definition found in the documents, before the key conflicts are resolved.
type candidate struct {
	spec   *setSpec
	object string
	notes  []Problem
	index  int
}

// manifest returns a manifest that lists the flags objects of the stage, to resolve the sources of the entries.
func (st *stage) manifest() *controlplane.Manifest {
	if st.manif == nil {
		objs := make([]controlplane.ManifestObject, 0, len(st.objects))
		for name := range st.objects {
			objs = append(objs, controlplane.ManifestObject{Name: name, Kind: controlplane.KindFlags})
		}

		st.manif = &controlplane.Manifest{Objects: objs}
	}

	return st.manif
}

// validateDoc checks the entries of a document against the object names; the result is kept in a copy of the
// state of the document, valid for the names signature.
func (st *stage) validateDocs(names []string, sig uint64) {
	for _, name := range names {
		held := st.docs[name]
		if held.parsed == nil || held.parsed.doc == nil || (held.validated && held.sig == sig) {
			continue
		}

		probs := map[int][]EntryProblem{}

		err := controlplane.ValidateFlagSet(held.parsed.doc, st.manifest())

		var invalid *controlplane.ValidationError

		switch {
		case err == nil:
		case errors.As(err, &invalid):
			for idx := range invalid.Problems {
				problem := &invalid.Problems[idx]
				probs[problem.Index] = append(probs[problem.Index], entryProblemOf(name, problem))
			}
		default:
			probs[controlplane.NoIndex] = []EntryProblem{{
				Object: name, Reason: err.Error(), Code: controlplane.CodeInvalidValue, Index: controlplane.NoIndex,
			}}
		}

		checked := *held
		checked.probs, checked.sig, checked.validated = probs, sig, true

		st.putDoc(name, &checked)
	}
}

// hardProblems drops the problems that this pass decides itself, across all documents: duplicate names and keys.
func hardProblems(list []EntryProblem) []EntryProblem {
	var out []EntryProblem

	for _, p := range list {
		if p.Code == controlplane.CodeDuplicateKey || p.Code == controlplane.CodeDuplicateName {
			continue
		}

		out = append(out, p)
	}

	return out
}

func newSpec(entry *controlplane.FlagSet) *setSpec {
	sum := hashString(entry.Name)
	for _, source := range entry.Sources {
		sum = mixString(sum, source)
	}

	sum = mixUint(sum, uint64(len(entry.Sources)))

	for _, key := range entry.APIKeys {
		sum = mixString(sum, key)
	}

	return &setSpec{
		name: entry.Name, sources: entry.Sources, declKeys: entry.APIKeys, keys: entry.APIKeys, declHash: sum,
	}
}

// fullPass reads every entry of the documents. It runs when a flag-set document changed or the set of object
// names changed; its cost is O(entries + keys) with no parse and no flag work for a flag set that is reused.
func (st *stage) fullPass(ctx context.Context, sig uint64) (*state, error) {
	prev := st.prev

	docNames := slices.Sorted(maps.Keys(st.docs))
	st.validateDocs(docNames, sig)

	next := &state{objects: st.objects, docs: st.docs, objStatus: st.objectStatus()}

	col := st.candidates(docNames)
	entries := st.resolveKeys(col.cands, col.entries)

	next.sets = make(map[string]*setState, len(col.cands))
	next.flagSets = make(map[string]*FlagSet, len(col.cands))
	next.setStatus = make(map[string]*FlagSetStatus, len(col.cands))
	next.rev = make(map[string][]string, len(st.objects))
	next.byKey = make(map[string]*FlagSet, len(prev.byKey))

	for idx, cand := range col.cands {
		if idx%checkEvery == 0 && ctx.Err() != nil {
			return nil, fmt.Errorf("build: %w", ctx.Err())
		}

		set := st.setFor(cand)
		name := cand.spec.name

		next.sets[name] = set
		next.flagSets[name] = set.fs
		next.setStatus[name] = set.status

		for _, key := range set.spec.keys {
			next.byKey[key] = set.fs
		}

		for _, source := range set.spec.sources {
			next.rev[source] = append(next.rev[source], name)
		}
	}

	st.withdraw(next)

	for name, problems := range col.pendingOnly {
		if _, ok := next.setStatus[name]; !ok {
			next.setStatus[name] = &FlagSetStatus{Name: name, State: StatePending, Problems: problems}
		}
	}

	slices.SortStableFunc(entries, compareEntries)

	next.entries = entries

	return next, nil
}

func compareEntries(left, right EntryProblem) int {
	return cmp.Or(
		cmp.Compare(left.Object, right.Object),
		cmp.Compare(left.Index, right.Index),
		cmp.Compare(left.Code, right.Code),
	)
}

// setFor returns the flag set of a definition: the running one when it is still right, else a new one.
func (st *stage) setFor(cand *candidate) *setState {
	old := st.prev.sets[cand.spec.name]
	if old != nil && !old.tomb && st.reusable(old, cand) {
		return old
	}

	set := st.buildSet(cand.spec, cand.notes)

	var oldSet *FlagSet
	if old != nil && !old.tomb {
		oldSet = old.fs
	}

	st.built.Rebuilt = append(st.built.Rebuilt, Rebuilt{Name: cand.spec.name, Old: oldSet, New: set.fs})

	return set
}

// collector gathers the definitions in force and the problems found on the way.
type collector struct {
	claimed     map[string]struct{}
	pendingOnly map[string][]Problem
	cands       []*candidate
	entries     []EntryProblem
}

// candidates collects the definitions in force: the entries in document order, a flag set name being held by the
// first entry. An invalid entry keeps the last valid definition of its flag set when there is one.
func (st *stage) candidates(docNames []string) *collector {
	col := &collector{claimed: map[string]struct{}{}, pendingOnly: map[string][]Problem{}}

	for _, docName := range docNames {
		held := st.docs[docName]
		if held.parsed == nil || held.parsed.doc == nil {
			continue
		}

		col.entries = append(col.entries, held.probs[controlplane.NoIndex]...)

		for idx := range held.parsed.doc.FlagSets {
			st.collect(col, held, idx)
		}
	}

	return col
}

func (st *stage) collect(col *collector, held *docState, idx int) {
	entry := &held.parsed.doc.FlagSets[idx]

	if hard := hardProblems(held.probs[idx]); len(hard) > 0 {
		st.collectInvalid(col, held.name, idx, entry, hard)

		return
	}

	if _, dup := col.claimed[entry.Name]; dup {
		col.entries = append(col.entries, EntryProblem{
			Object: held.name, Name: entry.Name, Index: idx, Code: controlplane.CodeDuplicateName,
			Reason: "a flag set with this name is already defined",
		})

		return
	}

	col.claimed[entry.Name] = struct{}{}
	col.cands = append(col.cands, &candidate{spec: newSpec(entry), object: held.name, index: idx})
}

// collectInvalid handles an invalid entry: it keeps the last valid definition of its flag set, or is reported
// as pending when there is none.
func (st *stage) collectInvalid(
	col *collector, object string, idx int, entry *controlplane.FlagSet, hard []EntryProblem,
) {
	col.entries = append(col.entries, hard...)

	if old := st.prev.sets[entry.Name]; old != nil && !old.tomb {
		if _, dup := col.claimed[entry.Name]; !dup {
			col.claimed[entry.Name] = struct{}{}
			col.cands = append(col.cands, &candidate{
				spec: &setSpec{
					name: old.spec.name, sources: old.spec.sources, declKeys: old.spec.declKeys,
					keys: old.spec.declKeys, declHash: old.spec.declHash,
				},
				object: object, index: idx,
				notes: []Problem{{
					Code: CodeDefinitionKept, Reason: "the entry is invalid; the last valid definition is kept",
				}},
			})
		}

		return
	}

	if entry.Name == "" {
		return
	}

	for _, problem := range hard {
		col.pendingOnly[entry.Name] = append(
			col.pendingOnly[entry.Name],
			Problem{Code: problem.Code, Reason: problem.Reason},
		)
	}
}

// resolveKeys gives every API key to one flag set. A key held by several flag sets stays with the flag set that
// held it in the running runtime, else with the first in document order; the others lose the key and report it
// (the key is never printed).
func (st *stage) resolveKeys(cands []*candidate, entries []EntryProblem) []EntryProblem {
	owner := make(map[string]string, len(st.prev.byKey))

	var conflicts []string

	for _, cand := range cands {
		for _, key := range cand.spec.declKeys {
			if first, taken := owner[key]; !taken {
				owner[key] = cand.spec.name
			} else if first != cand.spec.name {
				conflicts = append(conflicts, key)
			}
		}
	}

	if len(conflicts) == 0 {
		return entries
	}

	st.preferHeld(owner, conflicts, cands)

	return dropLostKeys(cands, owner, entries)
}

// preferHeld gives a contested key back to the flag set that holds it in the running runtime.
func (st *stage) preferHeld(owner map[string]string, conflicts []string, cands []*candidate) {
	byName := make(map[string]*candidate, len(cands))
	for _, cand := range cands {
		byName[cand.spec.name] = cand
	}

	for _, key := range conflicts {
		held := st.prev.byKey[key]
		if held == nil || held.State == StateWithdrawn {
			continue
		}

		if cand := byName[held.Name]; cand != nil && slices.Contains(cand.spec.declKeys, key) {
			owner[key] = held.Name
		}
	}
}

// dropLostKeys removes from every definition the keys that another flag set holds, and reports it.
func dropLostKeys(cands []*candidate, owner map[string]string, entries []EntryProblem) []EntryProblem {
	const reason = "an API key of this flag set is held by another flag set"

	for _, cand := range cands {
		kept := make([]string, 0, len(cand.spec.declKeys))

		for _, key := range cand.spec.declKeys {
			if owner[key] == cand.spec.name {
				kept = append(kept, key)
			}
		}

		if len(kept) == len(cand.spec.declKeys) {
			continue
		}

		cand.spec.keys = kept
		cand.notes = append(slices.Clone(cand.notes), Problem{Code: controlplane.CodeDuplicateKey, Reason: reason})
		entries = append(entries, EntryProblem{
			Object: cand.object, Name: cand.spec.name, Index: cand.index, Code: controlplane.CodeDuplicateKey,
			Reason: reason,
		})
	}

	return entries
}

// reusable says whether a built flag set is still right for a definition: same definition, same notes, and the
// same source objects (by pointer, so a changed, stale or removed object makes a new flag set).
func (st *stage) reusable(old *setState, cand *candidate) bool {
	if old.spec.declHash != cand.spec.declHash || len(old.spec.sources) != len(cand.spec.sources) ||
		!slices.Equal(old.spec.keys, cand.spec.keys) || !slices.Equal(old.notes, cand.notes) {
		return false
	}

	for idx, source := range cand.spec.sources {
		if st.objects[source] != old.inputs[idx] {
			return false
		}
	}

	return true
}

// withdraw keeps the flag sets that the documents no longer define as withdrawn tombstones: their keys still
// route to a flag set that answers "not available". A tombstone is kept until a flag set with its name returns,
// and costs one small struct per withdrawn flag set.
func (st *stage) withdraw(next *state) {
	for name, old := range st.prev.sets {
		if _, ok := next.sets[name]; ok {
			continue
		}

		tomb := old

		if !old.tomb {
			tomb = tombstone(name, old)

			st.built.Rebuilt = append(st.built.Rebuilt, Rebuilt{Name: name, Old: old.fs, New: tomb.fs})
		}

		next.sets[name] = tomb
		next.flagSets[name] = tomb.fs
		next.setStatus[name] = tomb.status

		for _, key := range tomb.spec.keys {
			if _, taken := next.byKey[key]; !taken {
				next.byKey[key] = tomb.fs
			}
		}
	}
}

func tombstone(name string, old *setState) *setState {
	return &setState{
		spec: old.spec, tomb: true,
		fs: &FlagSet{Name: name, State: StateWithdrawn, APIKeys: len(old.spec.keys)},
		status: &FlagSetStatus{
			Name: name, State: StateWithdrawn, Sources: old.spec.sources, APIKeys: len(old.spec.keys),
		},
	}
}
