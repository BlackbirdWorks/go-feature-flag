package flagruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/pkgs/evalcore"
)

// Configuration returns the flags of the flag set as the flag configuration endpoint lists them. A name in keys that
// the flag set does not hold is left out; with no keys every flag is returned. The map is new for each call, never
// nil, and the caller may change it; the flags in it are the flags of the flag set itself, shared with every other
// flag set that lists the same object and with the next generation, and are READ-ONLY: encode them, do not change
// them. A caller that must change a flag takes a copy with its DeepCopy method.
func (f *FlagSet) Configuration(keys []string) map[string]flagdef.Flag {
	if f == nil {
		return map[string]flagdef.Flag{}
	}

	if len(keys) == 0 {
		out := make(map[string]flagdef.Flag, len(f.Flags))
		for key, flag := range f.Flags {
			out[key] = flag
		}

		return out
	}

	out := make(map[string]flagdef.Flag, len(keys))

	for _, key := range keys {
		if flag, ok := f.Flags[key]; ok {
			out[key] = flag
		}
	}

	return out
}

// FlagChange is the answer of the flag change endpoint: a hash of the flag set and one of every flag.
type FlagChange struct {
	// Flags are the hashes of the flags by key. The map is shared by every call on the same flag set: it is
	// READ-ONLY.
	Flags map[string]uint32 `json:"flags"`
	// Hash is the hash of the whole flag set.
	Hash uint32 `json:"hash"`
}

// changeMemo is the memoized answer of [FlagSet.Change], error included.
type changeMemo struct {
	err    error
	change FlagChange
}

// Change returns the flag change answer: the hashes are those of the JSON encoding of the flags, byte for byte as
// the version 1 endpoint computes them. A flag set is immutable, so the answer is computed once, by the first
// call (each flag is encoded once), and every later call returns the same value without allocating; the map of
// the answer is shared and READ-ONLY. A flag set that a later generation carries over by pointer keeps its memo; a
// rebuilt one computes its own. A nil flag set has the hash of the empty set.
func (f *FlagSet) Change() (FlagChange, error) {
	if f == nil {
		memo := computeChange(nil, nil)

		return memo.change, memo.err
	}

	f.changeOnce.Do(func() { f.change = computeChange(f.Flags, f.flagSigs) })

	return f.change.change, f.change.err
}

// computeChange encodes every flag once and hashes the encoding of each flag and of the whole table. The table is
// the JSON object that [json.Marshal] writes for the map: the keys sorted, each followed by the encoding of its flag.
func computeChange(flags map[string]*flagdef.InternalFlag, sigs map[string]uint64) *changeMemo {
	keys := make([]string, 0, len(flags))
	for key := range flags {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	var (
		table  bytes.Buffer
		hashes = make(map[string]uint32, len(keys))
	)

	table.WriteByte('{')

	for idx, key := range keys {
		flagJSON, err := json.Marshal(flags[key])
		if err != nil {
			return &changeMemo{err: fmt.Errorf("encoding the flag %q: %w", key, err)}
		}

		keyJSON, err := json.Marshal(key)
		if err != nil {
			return &changeMemo{err: fmt.Errorf("encoding the flag key %q: %w", key, err)}
		}

		if idx > 0 {
			table.WriteByte(',')
		}

		table.Write(keyJSON)
		table.WriteByte(':')
		table.Write(flagJSON)

		hashes[key] = evalcore.Hash(string(flagJSON))

		if sig, ok := sigs[key]; ok {
			// the flag uses segments: its hash changes with the members of those segments
			hashes[key] = evalcore.Hash(string(flagJSON) + "#" + strconv.FormatUint(sig, 16))
		}
	}

	table.WriteByte('}')

	whole := table.String()
	if len(sigs) > 0 {
		whole += "#" + strconv.FormatUint(sigsHash(sigs), 16)
	}

	return &changeMemo{change: FlagChange{Hash: evalcore.Hash(whole), Flags: hashes}}
}
