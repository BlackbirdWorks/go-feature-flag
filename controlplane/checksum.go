package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	sha256HexLen = 64
	prefixLen    = 2
)

// ChecksumOf returns the sha256 of the exact bytes, as lower-case hex. (The function is not named Checksum
// because that name is the wire type.)
func ChecksumOf(data []byte) Checksum {
	sum := sha256.Sum256(data)

	return Checksum{Algorithm: AlgorithmSHA256, Value: hex.EncodeToString(sum[:])}
}

// VerifyChecksum checks that data has the checksum want. It returns ErrUnsupportedChecksum for an algorithm
// other than sha256 and a *ChecksumMismatchError (matches ErrChecksumMismatch) when the digest differs.
func VerifyChecksum(data []byte, want Checksum) error {
	if want.Algorithm != AlgorithmSHA256 {
		return fmt.Errorf("%w: %q", ErrUnsupportedChecksum, want.Algorithm)
	}

	got := ChecksumOf(data).Value
	if got != want.Value {
		return &ChecksumMismatchError{Want: want.Value, Got: got}
	}

	return nil
}

// ObjectKey returns the immutable content-addressed key of an object:
// objects/sha256/<2 hex>/<hex>.<ext>. A leading dot of ext is ignored.
func ObjectKey(sum Checksum, ext string) string {
	prefix := sum.Value
	if len(prefix) > prefixLen {
		prefix = prefix[:prefixLen]
	}

	return "objects/" + sum.Algorithm + "/" + prefix + "/" + sum.Value + "." + strings.TrimPrefix(ext, ".")
}
