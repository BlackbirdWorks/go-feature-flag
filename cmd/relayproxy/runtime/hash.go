package runtime //nolint:revive,nolintlint // the name is fixed: the package is cmd/relayproxy/runtime

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
