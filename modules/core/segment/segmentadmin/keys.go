package segmentadmin

import "strings"

// Suffixes of the temporary keys of an import.
const (
	tmpSuffix = ":import:new"
	oldSuffix = ":import:old"
	slots     = 16384
	crcBits   = 8
	crcPoly   = 0x1021
	crcTop    = 0x8000
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

// TempKeys returns the key that an import fills (tmp) and the key that holds the replaced set until it is removed
// (old). Both are in the cluster slot of the live key: they keep its hash tag, or, when the live key has none, are
// wrapped in one that is the whole live key (which hashes like the key). The names are fixed for a segment, so a run
// that follows a killed one finds and removes what it left.
func TempKeys(live string) (string, string, error) {
	base := live
	if _, tagged := hashTag(live); !tagged {
		base = "{" + live + "}"
	}

	tmp, old := base+tmpSuffix, base+oldSuffix
	if Slot(tmp) != Slot(live) || Slot(old) != Slot(live) {
		return "", "", ErrNoSlotTag
	}

	return tmp, old, nil
}
