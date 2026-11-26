package flagruntime

import (
	"encoding/json"
	"fmt"

	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/pkgs/evalcore"
)

// Configuration returns the flags of the flag set as the flag configuration endpoint lists them: deep copies, the
// caller may change them. A name in keys that the flag set does not hold is left out; with no keys every flag is
// returned. The map is never nil.
func (f *FlagSet) Configuration(keys []string) map[string]flagdef.Flag {
	if f == nil {
		return map[string]flagdef.Flag{}
	}

	if len(keys) == 0 {
		out := make(map[string]flagdef.Flag, len(f.Flags))
		for key, flag := range f.Flags {
			out[key] = flag.DeepCopy()
		}

		return out
	}

	out := make(map[string]flagdef.Flag, len(keys))

	for _, key := range keys {
		if flag, ok := f.Flags[key]; ok {
			out[key] = flag.DeepCopy()
		}
	}

	return out
}

// FlagChange is the answer of the flag change endpoint: a hash of the flag set and one of every flag.
type FlagChange struct {
	// Flags are the hashes of the flags by key.
	Flags map[string]uint32 `json:"flags"`
	// Hash is the hash of the whole flag set.
	Hash uint32 `json:"hash"`
}

// Change computes the flag change answer, exactly as the version 1 endpoint does: the hashes are those of the JSON
// encoding of the flags. It reads the flags without copying them. A nil flag set has the hash of the empty set.
func (f *FlagSet) Change() (FlagChange, error) {
	flags := map[string]flagdef.Flag{}

	if f != nil {
		for key, flag := range f.Flags {
			flags[key] = flag
		}
	}

	all, err := json.Marshal(flags)
	if err != nil {
		return FlagChange{}, fmt.Errorf("computing the hash of the flag set: %w", err)
	}

	hashes := make(map[string]uint32, len(flags))

	for key, flag := range flags {
		data, flagErr := json.Marshal(flag)
		if flagErr != nil {
			return FlagChange{}, fmt.Errorf("encoding the flag %q: %w", key, flagErr)
		}

		hashes[key] = evalcore.Hash(string(data))
	}

	return FlagChange{Hash: evalcore.Hash(string(all)), Flags: hashes}, nil
}
