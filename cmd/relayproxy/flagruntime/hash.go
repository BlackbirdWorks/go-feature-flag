package flagruntime

const (
	fnvOffset uint64 = 14695981039346656037
	fnvPrime  uint64 = 1099511628211
	// separator ends a string in the hash, so that ("ab","c") and ("a","bc") differ.
	separator uint64 = 0xff
	byteMask  uint64 = 0xff
	byteBits         = 8
)

func hashString(text string) uint64 {
	return mixString(fnvOffset, text)
}

func mixString(seed uint64, text string) uint64 {
	sum := seed
	for idx := range len(text) {
		sum ^= uint64(text[idx])
		sum *= fnvPrime
	}

	sum ^= separator
	sum *= fnvPrime

	return sum
}

func mixUint(seed, value uint64) uint64 {
	sum := seed
	for range byteBits {
		sum ^= value & byteMask
		sum *= fnvPrime
		value >>= byteBits
	}

	return sum
}

// hashBytes hashes a byte slice.
func hashBytes(data []byte) uint64 {
	sum := fnvOffset
	for _, b := range data {
		sum ^= uint64(b)
		sum *= fnvPrime
	}

	return sum
}

// entryHash combines a flag key with the content hash of its flag. Tables are combined by summing these, so the
// result does not depend on the order of the flags.
func entryHash(key string, content uint64) uint64 {
	return mixUint(hashString(key), content)
}

// tableHash is the order independent hash of the effective flags of a table.
func tableHash(hashes map[string]uint64) uint64 {
	var total uint64
	for key, content := range hashes {
		total += entryHash(key, content)
	}

	return total
}
