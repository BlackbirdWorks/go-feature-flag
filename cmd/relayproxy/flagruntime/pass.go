package flagruntime

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
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
	mode   candidateMode
}

// candidateMode says how a definition is turned into a flag set.
type candidateMode uint8

const (
	// modeBuild: a valid entry, built from its sources.
	modeBuild candidateMode = iota
	// modeFrozen: an invalid entry whose flag set keeps the flags it had.
	modeFrozen
	// modePlaceholder: an invalid entry with no previous flags; it only routes its keys.
	modePlaceholder
)

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
func (st *stage) validateDocs(names []string, namesVer uint64) {
	for _, name := range names {
		held := st.docs[name]
		if held.parsed == nil || held.parsed.doc == nil || (held.validated && held.sig == namesVer) {
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
		checked.probs, checked.sig, checked.validated = probs, namesVer, true

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

// collector gathers the definitions in force and the problems found on the way.
type collector struct {
	claimed     map[string]struct{}
	pendingOnly map[string][]Problem
	cands       []*candidate
	entries     []EntryProblem
}

// minKeysToCheck is the number of keys from which a list can have a repeat.
const minKeysToCheck = 2

// uniqueKeys returns the keys without their repeats (the input itself when there is none), and the number of
// repeats.
func uniqueKeys(keys []string) ([]string, int) {
	if len(keys) < minKeysToCheck {
		return keys, 0
	}

	var (
		unique []string
		seen   = make(map[string]struct{}, len(keys))
	)

	for idx, key := range keys {
		if _, repeated := seen[key]; repeated {
			if unique == nil {
				unique = slices.Clone(keys[:idx])
			}

			continue
		}

		seen[key] = struct{}{}

		if unique != nil {
			unique = append(unique, key)
		}
	}

	if unique == nil {
		return keys, 0
	}

	return unique, len(keys) - len(unique)
}

// newSpec makes the definition of an entry. The keys of a flag set are unique: a key listed twice is one route,
// and the second listing is reported by the caller (repeats).
func newSpec(entry *controlplane.FlagSet) (*setSpec, int) {
	sum := hashString(entry.Name)
	for _, source := range entry.Sources {
		sum = mixString(sum, source)
	}

	sum = mixUint(sum, uint64(len(entry.Sources)))

	for _, key := range entry.APIKeys {
		sum = mixString(sum, key)
	}

	keys, repeats := uniqueKeys(entry.APIKeys)

	return &setSpec{
		name: entry.Name, sources: entry.Sources, declKeys: keys, keys: keys, declHash: sum,
	}, repeats
}

// repeatedKeys reports the keys that an entry lists more than once, by position and never by value.
func (col *collector) repeatedKeys(spec *setSpec, repeats int, object string, idx int) []Problem {
	if repeats == 0 {
		return nil
	}

	const reason = "an API key is listed more than once in this flag set"

	col.entries = append(col.entries, EntryProblem{
		Object: object, Name: spec.name, Index: idx, Code: controlplane.CodeDuplicateKey, Reason: reason,
	})

	return []Problem{{Code: controlplane.CodeDuplicateKey, Reason: reason}}
}

// fullPass reads every entry of the documents. It runs when a flag-set document changed or the set of object
// names changed; its cost is O(entries + keys) with no parse and no flag work for a flag set that is reused.
func (st *stage) fullPass(ctx context.Context, namesVer uint64) (*state, error) {
	prev := st.prev

	docNames := slices.Sorted(maps.Keys(st.docs))
	st.validateDocs(docNames, namesVer)

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

		if set.frozen || set.placeholder {
			continue // its sources are not read: a changed source does not rebuild it
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
	if old != nil && st.reusable(old, cand) {
		return old
	}

	var set *setState

	switch cand.mode {
	case modeFrozen:
		set = frozenSet(old, cand.spec, cand.notes, st.now)
	case modePlaceholder:
		set = placeholderSet(cand.spec, cand.notes, st.now)
	case modeBuild:
		set = st.buildSet(cand.spec, cand.notes)
	}

	var oldSet *FlagSet
	if old != nil {
		oldSet = old.fs
	}

	st.built.Rebuilt = append(st.built.Rebuilt, Rebuilt{Name: cand.spec.name, Old: oldSet, New: set.fs})

	return set
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

	spec, repeats := newSpec(entry)
	col.cands = append(col.cands, &candidate{
		spec: spec, object: held.name, index: idx, notes: col.repeatedKeys(spec, repeats, held.name, idx),
	})
}

// collectInvalid handles an invalid entry, the same way for every cause and on first load or later:
//   - the flag set had a definition: it keeps it (frozen, degraded);
//   - it had none and its name and keys are valid: it gets a placeholder that routes its keys and serves no flag
//     (pending), so a valid key reaches the "not available" answer;
//   - otherwise the entry is reported in the status only.
func (st *stage) collectInvalid(
	col *collector, object string, idx int, entry *controlplane.FlagSet, hard []EntryProblem,
) {
	col.entries = append(col.entries, hard...)

	if _, dup := col.claimed[entry.Name]; dup {
		return
	}

	notes := make([]Problem, 0, len(hard)+1)
	for _, problem := range hard {
		notes = append(notes, Problem{Code: problem.Code, Reason: problem.Reason})
	}

	if old := st.prev.sets[entry.Name]; old != nil && !old.placeholder {
		col.claimed[entry.Name] = struct{}{}

		spec := &setSpec{
			name: old.spec.name, sources: old.spec.sources, declKeys: old.spec.declKeys,
			keys: old.spec.declKeys, declHash: old.spec.declHash,
		}

		notes = append([]Problem{{
			Code: CodeDefinitionKept, Reason: "the entry is invalid; the last valid definition is kept",
		}}, notes...)
		col.cands = append(
			col.cands,
			&candidate{spec: spec, object: object, index: idx, notes: notes, mode: modeFrozen},
		)

		return
	}

	if routable(entry) {
		col.claimed[entry.Name] = struct{}{}

		spec, repeats := newSpec(entry)
		notes = append(notes, col.repeatedKeys(spec, repeats, object, idx)...)
		col.cands = append(
			col.cands,
			&candidate{spec: spec, object: object, index: idx, notes: notes, mode: modePlaceholder},
		)

		return
	}

	if entry.Name != "" {
		col.pendingOnly[entry.Name] = notes
	}
}

// nameRule is the rule of a flag set name (the same as the validation of the documents).
var nameRule = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// routable says whether the name and every API key of an entry are valid, so its keys can be routed.
func routable(entry *controlplane.FlagSet) bool {
	return nameRule.MatchString(entry.Name) && !slices.Contains(entry.APIKeys, "")
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

	// the declared keys of a candidate, as a set, built when a contested key needs it: linear in the keys
	declared := map[*candidate]map[string]struct{}{}

	for _, key := range conflicts {
		held := st.prev.byKey[key]
		if held == nil {
			continue
		}

		cand := byName[held.Name]
		if cand == nil {
			continue
		}

		keys, ok := declared[cand]
		if !ok {
			keys = make(map[string]struct{}, len(cand.spec.declKeys))
			for _, declaredKey := range cand.spec.declKeys {
				keys[declaredKey] = struct{}{}
			}

			declared[cand] = keys
		}

		if _, holds := keys[key]; holds {
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
		!slices.Equal(old.spec.keys, cand.spec.keys) || !slices.Equal(old.notes, cand.notes) ||
		old.frozen != (cand.mode == modeFrozen) || old.placeholder != (cand.mode == modePlaceholder) {
		return false
	}

	if cand.mode != modeBuild {
		return true
	}

	for idx, source := range cand.spec.sources {
		if st.objects[source] != old.inputs[idx] {
			return false
		}
	}

	return true
}

// withdraw removes the flag sets that the documents no longer define: they leave the flag-set and key tables in
// this generation (their keys are unknown from now on and free for another flag set). The status keeps the most
// recent withdrawn names, never their keys, up to the cap of the builder; the oldest are dropped.
func (st *stage) withdraw(next *state) {
	var removed []string

	for name := range st.prev.sets {
		if _, ok := next.sets[name]; !ok {
			removed = append(removed, name)
		}
	}

	slices.Sort(removed)

	for _, name := range removed {
		old := st.prev.sets[name]
		gone := &FlagSet{Name: name, State: StateWithdrawn}

		st.built.Rebuilt = append(st.built.Rebuilt, Rebuilt{Name: name, Old: old.fs, New: gone})
	}

	list := st.prev.withdrawn

	back := false

	for _, entry := range list {
		if _, live := next.sets[entry.Name]; live {
			back = true
		}
	}

	if len(removed) == 0 && !back {
		next.withdrawn = list

		return
	}

	list = make([]WithdrawnSet, 0, len(st.prev.withdrawn)+len(removed))

	for _, entry := range st.prev.withdrawn {
		if _, live := next.sets[entry.Name]; !live {
			list = append(list, entry)
		}
	}

	for _, name := range removed {
		list = append(list, WithdrawnSet{Name: name, Generation: st.gen})
	}

	if over := len(list) - st.b.opts.WithdrawnCap; over > 0 {
		list = slices.Clone(list[over:])
	}

	next.withdrawn = list
}
