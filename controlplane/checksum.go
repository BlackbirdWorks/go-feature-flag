package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

// validChecksum checks the algorithm and the form of a checksum.
func validChecksum(sum Checksum) error {
	if sum.Algorithm != AlgorithmSHA256 {
		return fmt.Errorf("%w: %q", ErrUnsupportedChecksum, sum.Algorithm)
	}

	if !isLowerHex(sum.Value, sha256HexLen) {
		return fmt.Errorf("%w: want %d lower-case hex characters", ErrInvalidChecksum, sha256HexLen)
	}

	return nil
}

// VerifyChecksum checks that data has the checksum want. It returns ErrUnsupportedChecksum for an algorithm
// other than sha256, ErrInvalidChecksum for a value that is not 64 lower-case hex characters (upper-case hex is
// invalid, not a mismatch) and a *ChecksumMismatchError (matches ErrChecksumMismatch) when the digest differs.
func VerifyChecksum(data []byte, want Checksum) error {
	err := validChecksum(want)
	if err != nil {
		return err
	}

	got := ChecksumOf(data).Value
	if got != want.Value {
		return &ChecksumMismatchError{Want: want.Value, Got: got}
	}

	return nil
}

// ObjectKey returns the immutable content-addressed key of an object:
// objects/sha256/<2 hex>/<hex>.<ext>. The checksum must be a valid sha256 (ErrUnsupportedChecksum,
// ErrInvalidChecksum) and ext must match [a-z0-9]+ without a dot (ErrInvalidExtension), so a key can never
// contain a path separator or a ".." element.
func ObjectKey(sum Checksum, ext string) (string, error) {
	err := validChecksum(sum)
	if err != nil {
		return "", err
	}

	if !validExtension(ext) {
		return "", fmt.Errorf("%w: %q", ErrInvalidExtension, ext)
	}

	return "objects/" + sum.Algorithm + "/" + sum.Value[:prefixLen] + "/" + sum.Value + "." + ext, nil
}

func validExtension(ext string) bool {
	if ext == "" {
		return false
	}

	for i := range len(ext) {
		char := ext[i]
		if char < 'a' || char > 'z' {
			if char < '0' || char > '9' {
				return false
			}
		}
	}

	return true
}
