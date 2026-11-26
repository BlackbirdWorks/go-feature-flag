package runtime //nolint:revive,nolintlint // the name is fixed: the package is cmd/relayproxy/runtime

import (
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

	for key, before := range oldFlags {
		if _, ok := newFlags[key]; !ok {
			diff.Deleted[key] = before
		}
	}

	return diff
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
		case cur.before != cur.after:
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
