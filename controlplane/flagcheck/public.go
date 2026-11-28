package flagcheck

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// ErrPublicDuplicate is matched (errors.Is) by the error of CheckPublicDuplicates.
var ErrPublicDuplicate = errors.New("flagcheck: a flag key is defined in two keyless flag sets")

// duplicateAt is the number of flag sets that define a flag key for it to be a duplicate.
const duplicateAt = 2

// KeylessSet is a flag set that is published without API keys, with the flag files of its sources in order.
type KeylessSet struct {
	// Name is the name of the flag set.
	Name string
	// Flags are the flag files of its sources, in the order of the sources.
	Flags []FlagFile
}

// PublicOwner is one keyless flag set that defines a flag key.
type PublicOwner struct {
	// FlagSet is the name of the flag set.
	FlagSet string
	// Object is the flag object of the flag set whose definition it serves.
	Object string
}

// PublicProblem is one flag key that two or more keyless flag sets define.
type PublicProblem struct {
	// Flag is the flag key.
	Flag string
	// Owners are the keyless flag sets that define it, sorted by name.
	Owners []PublicOwner
}

// PublicError lists the flag keys that CheckPublicDuplicates rejects, sorted by key.
type PublicError struct {
	Problems []PublicProblem
}

// Error implements error: one entry per flag key, naming the flag sets (and their flag objects).
func (e *PublicError) Error() string {
	parts := make([]string, 0, len(e.Problems))

	for i := range e.Problems {
		owners := make([]string, 0, len(e.Problems[i].Owners))
		for _, owner := range e.Problems[i].Owners {
			owners = append(owners, fmt.Sprintf("%s (flag object %s)", owner.FlagSet, owner.Object))
		}

		parts = append(parts, fmt.Sprintf("flag %s: defined in the keyless flag sets %s; they form one public "+
			"namespace, so a flag key can be defined in only one of them",
			safeKey(e.Problems[i].Flag), strings.Join(owners, " and ")))
	}

	return strings.Join(parts, "; ")
}

// Is reports a match with ErrPublicDuplicate.
func (*PublicError) Is(target error) bool { return target == ErrPublicDuplicate }

// CheckPublicDuplicates checks the flag sets that are published without API keys. The relay merges them into one
// public view and rejects a generation in which two of them define the same flag key (flagruntime.PublicName), so a
// publication must not contain one. The flags of a flag set are those of its flag files merged in order (Winners). It
// returns nil or a *PublicError that lists every such flag key in key order, with its flag sets in name order.
func CheckPublicDuplicates(sets []KeylessSet) error {
	owners := map[string][]PublicOwner{}

	for i := range sets {
		for key, object := range Winners(sets[i].Flags) {
			owners[key] = append(owners[key], PublicOwner{FlagSet: sets[i].Name, Object: object})
		}
	}

	out := &PublicError{}

	for _, key := range slices.Sorted(maps.Keys(owners)) {
		if len(owners[key]) < duplicateAt {
			continue
		}

		slices.SortFunc(owners[key], func(a, b PublicOwner) int { return strings.Compare(a.FlagSet, b.FlagSet) })
		out.Problems = append(out.Problems, PublicProblem{Flag: key, Owners: owners[key]})
	}

	if len(out.Problems) == 0 {
		return nil
	}

	return out
}
