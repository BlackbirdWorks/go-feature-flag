package flagruntime

import (
	"encoding/json"
	"fmt"

	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/notifier"
)

// Change tells what changed in one flag set. It is emitted once per rebuilt flag set after a swap.
type Change struct {
	// Diff lists the flags added, deleted and updated. A flag whose pointer is the same in both flag sets is not
	// listed, so an unchanged flag costs nothing.
	Diff notifier.DiffCache
	// FlagSet is the name of the flag set.
	FlagSet string
	// Generation is the generation of the swap that produced the last of the coalesced changes.
	Generation uint64
	// Snapshot is the snapshot number of that swap.
	Snapshot uint64
	// Removed says that the flag set was withdrawn: every flag of the diff is deleted.
	Removed bool
}

// Rebuilt is a flag set that has a new pointer in a [Built]. Old is nil for a new flag set, and New is a
// withdrawn flag set (no flags) for a removed one.
type Rebuilt struct {
	Old  *FlagSet
	New  *FlagSet
	Name string
}

// flagState is the state of one flag before and after a change; nil means absent.
type flagState struct {
	before, after flagdef.Flag
}

// diffFlagSets compares two flag sets by flag pointer. old is nil for a new flag set; a withdrawn flag set has
// no flags.
func diffFlagSets(oldSet, newSet *FlagSet) notifier.DiffCache {
	diff := notifier.DiffCache{
		Added:   map[string]flagdef.Flag{},
		Deleted: map[string]flagdef.Flag{},
		Updated: map[string]notifier.DiffUpdated{},
	}

	var oldFlags, newFlags map[string]*flagdef.InternalFlag
	if oldSet != nil {
		oldFlags = oldSet.Flags
	}

	if newSet != nil {
		newFlags = newSet.Flags
	}

	for key, now := range newFlags {
		before, ok := oldFlags[key]

		switch {
		case !ok:
			diff.Added[key] = now
		case before != now:
			diff.Updated[key] = notifier.DiffUpdated{Before: before, After: now}
		}
	}

	// a flag that is the same but uses a segment whose members or definition changed (or whose segment source was
	// attached or detached) is updated: its evaluations changed. A flag with no signature has signature zero, so the
	// two directions are compared. Only the flags that use segments are looked at.
	updateReevaluated(&diff, oldSet, newSet, oldFlags, newFlags)

	for key, before := range oldFlags {
		if _, ok := newFlags[key]; !ok {
			diff.Deleted[key] = before
		}
	}

	return diff
}

// updateReevaluated adds, for the flags that are the same in both flag sets and whose segment signature differs, an
// update whose two sides carry the signature, so that composing changes can tell a revert from a change.
func updateReevaluated(
	diff *notifier.DiffCache, oldSet, newSet *FlagSet, oldFlags, newFlags map[string]*flagdef.InternalFlag,
) {
	var oldSigs, newSigs map[string]uint64

	if oldSet != nil {
		oldSigs = oldSet.flagSigs
	}

	if newSet != nil {
		newSigs = newSet.flagSigs
	}

	check := func(key string) {
		before, ok := oldFlags[key]
		if !ok || before != newFlags[key] || oldSigs[key] == newSigs[key] {
			return
		}

		diff.Updated[key] = notifier.DiffUpdated{
			Before: reevaluated{Flag: before, sig: oldSigs[key]},
			After:  reevaluated{Flag: before, sig: newSigs[key]},
		}
	}

	for key := range newSigs {
		check(key)
	}

	for key := range oldSigs {
		if _, seen := newSigs[key]; !seen {
			check(key)
		}
	}
}

// reevaluated is the "after" of a flag whose definition did not change but whose segments did: it is another value
// than the flag, so the change is not dropped when changes are composed, and it is the flag when it is encoded.
type reevaluated struct {
	flagdef.Flag

	sig uint64
}

// MarshalJSON encodes the flag.
func (r reevaluated) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(r.Flag)
	if err != nil {
		return nil, fmt.Errorf("encoding the flag: %w", err)
	}

	return data, nil
}

// flagIdentity is a flag and the signature of its segments: what two sides of a change have to share to be the same.
type flagIdentity struct {
	flag flagdef.Flag
	sig  uint64
}

// sameFlag says whether two sides of a change are the same flag with the same segments: the same flag pointer and the
// same signature, whatever wraps them.
func sameFlag(left, right flagdef.Flag) bool {
	return identityOf(left) == identityOf(right)
}

func identityOf(flag flagdef.Flag) flagIdentity {
	if wrapped, ok := flag.(reevaluated); ok {
		return flagIdentity{flag: wrapped.Flag, sig: wrapped.sig}
	}

	return flagIdentity{flag: flag}
}

// flagStates tracks, per flag key, the state before the first change and after the last one.
type flagStates map[string]flagState

// apply merges a diff that happens after the changes seen so far.
func (states flagStates) apply(diff *notifier.DiffCache) {
	for key, fl := range diff.Added {
		states.set(key, nil, fl)
	}

	for key, fl := range diff.Deleted {
		states.set(key, fl, nil)
	}

	for key, update := range diff.Updated {
		states.set(key, update.Before, update.After)
	}
}

func (states flagStates) set(key string, before, after flagdef.Flag) {
	cur, seen := states[key]
	if !seen {
		cur.before = before
	}

	cur.after = after
	states[key] = cur
}

// diff classifies the net change of every key; a key that ends as it began is dropped.
func (states flagStates) diff() notifier.DiffCache {
	out := notifier.DiffCache{
		Added:   map[string]flagdef.Flag{},
		Deleted: map[string]flagdef.Flag{},
		Updated: map[string]notifier.DiffUpdated{},
	}

	for key, cur := range states {
		switch {
		case cur.before == nil && cur.after == nil:
		case cur.before == nil:
			out.Added[key] = cur.after
		case cur.after == nil:
			out.Deleted[key] = cur.before
		case !sameFlag(cur.before, cur.after):
			out.Updated[key] = notifier.DiffUpdated{Before: cur.before, After: cur.after}
		}
	}

	return out
}

// compose merges a pending change (state A to B) with a later one (B to C) into A to C.
func compose(first, second *Change) {
	states := flagStates{}
	states.apply(&first.Diff)
	states.apply(&second.Diff)

	first.Diff = states.diff()
	first.Removed = second.Removed
	first.Generation, first.Snapshot = second.Generation, second.Snapshot
}
