package segmentadmin

import (
	"fmt"
	"strings"

	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
)

const (
	slots   = 16384
	crcBits = 8
	crcPoly = 0x1021
	crcTop  = 0x8000
)

// hashTag returns the part of a key that a cluster hashes, and whether the key has a hash tag.
func hashTag(key string) (string, bool) {
	start := strings.IndexByte(key, '{')
	if start < 0 {
		return key, false
	}

	end := strings.IndexByte(key[start+1:], '}')
	if end <= 0 {
		return key, false // no closing brace, or an empty tag: the whole key is hashed
	}

	return key[start+1 : start+1+end], true
}

// Slot is the cluster slot of a key: CRC16 (XMODEM) of its hash tag, or of the key, modulo 16384.
func Slot(key string) int {
	tag, _ := hashTag(key)

	var crc uint16

	for i := range len(tag) {
		crc ^= uint16(tag[i]) << crcBits

		for range crcBits {
			if crc&crcTop != 0 {
				crc = crc<<1 ^ crcPoly
			} else {
				crc <<= 1
			}
		}
	}

	return int(crc) % slots
}

// RunKeys are the keys of one import. All are in the cluster slot of the live key, which the script of the swap needs.
type RunKeys struct {
	// Lock holds the token of the run that may import the segment.
	Lock string
	// Last holds the name of the temporary key of the run that holds the lock, so that the next run removes exactly
	// that key when the run died (no SCAN, no KEYS).
	Last string
	// Committed holds the token of the run whose swap was applied.
	Committed string
	// Source holds the fingerprint of the input that the live set was last imported from.
	Source string
	// Tmp is filled by the run; its name holds the token of the run, so nothing else can recreate or share it.
	Tmp string
	// Old holds the replaced set until it is removed.
	Old string
	// base is the prefix of the keys: the live key, or the live key wrapped in a hash tag.
	base string
}

// ImportKeys returns the keys of the run of an import with a token. They keep the hash tag of the live key, or, when
// the live key has none, wrap the live key in one (which hashes like the key). A key whose tag cannot be shared is
// refused.
func ImportKeys(live, token string) (RunKeys, error) {
	base := live
	if _, tagged := hashTag(live); !tagged {
		base = "{" + live + "}"
	}

	keys := RunKeys{
		Lock:      base + ":import:lock",
		Last:      base + ":import:last",
		Committed: base + ":import:committed",
		Source:    base + ":import:source",
		Tmp:       base + ":import:" + token + ":new",
		Old:       base + ":import:" + token + ":old",
		base:      base,
	}

	for _, key := range []string{keys.Lock, keys.Last, keys.Committed, keys.Source, keys.Tmp, keys.Old} {
		if Slot(key) != Slot(live) {
			return RunKeys{}, ErrNoSlotTag
		}
	}

	return keys, nil
}

// Owns tells whether a key is one that an import of the segment names (a leftover of a dead run).
func (k *RunKeys) Owns(key string) bool { return strings.HasPrefix(key, k.base+":import:") }

// CheckKeys refuses two segments whose keys can meet: an import of one writes and removes keys under
// `<key>:import:` (and `{<key>}:import:` for a key without a hash tag), so no key of the other segment (its set, its
// version key, the prefix of its member keys) may be one of them, and the two segments may not share a set or a prefix.
// The check is made both ways.
func CheckKeys(target, other *Target) error {
	if target.Key == "" || other.Key == "" {
		return nil
	}

	for _, pair := range [2][2]*Target{{target, other}, {other, target}} {
		err := derivedClash(pair[0], pair[1])
		if err != nil {
			return err
		}
	}

	if target.Key == other.Key || target.Key == other.VersionKey || other.Key == target.VersionKey {
		return fmt.Errorf("%w: the segments %q and %q use the same key", ErrKeyOverlap, target.Name, other.Name)
	}

	return nil
}

// derivedClash checks that no key of other is one of the keys that an import of owner derives from its key.
func derivedClash(owner, other *Target) error {
	if owner.Layout == redissegment.LayoutKeys {
		return nil // no import of the keys layout: nothing is derived
	}

	base := owner.Key
	if _, tagged := hashTag(owner.Key); !tagged {
		base = "{" + owner.Key + "}"
	}

	prefix := base + ":import:"

	for _, key := range []string{other.Key, other.VersionKey} {
		if key != "" && strings.HasPrefix(key, prefix) {
			return fmt.Errorf(
				"%w: a key of the segment %q is inside the keys that an import of the segment %q uses (%s)",
				ErrKeyOverlap,
				other.Name,
				owner.Name,
				prefix,
			)
		}
	}

	if other.Layout == redissegment.LayoutKeys && strings.HasPrefix(prefix, other.Key) {
		return fmt.Errorf(
			"%w: the member keys of the segment %q can be the keys that an import of the segment %q uses (%s)",
			ErrKeyOverlap,
			other.Name,
			owner.Name,
			prefix,
		)
	}

	return nil
}
