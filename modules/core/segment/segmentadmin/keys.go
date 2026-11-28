package segmentadmin

import "strings"

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
		Lock: base + ":import:lock", Last: base + ":import:last", Tmp: base + ":import:" + token + ":new",
		Old: base + ":import:" + token + ":old", base: base,
	}

	for _, key := range []string{keys.Lock, keys.Last, keys.Tmp, keys.Old} {
		if Slot(key) != Slot(live) {
			return RunKeys{}, ErrNoSlotTag
		}
	}

	return keys, nil
}

// Owns tells whether a key is one that an import of the segment names (a leftover of a dead run).
func (k *RunKeys) Owns(key string) bool { return strings.HasPrefix(key, k.base+":import:") }
