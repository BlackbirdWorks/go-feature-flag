package statedir

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// envelope is the content of manifest.json: the accepted manifest with its ETag, the checksums of the objects
// saved with it, and a digest of all of that. A flipped or missing byte anywhere makes the digest fail and the
// file is ignored.
type envelope struct {
	ETag     string   `json:"etag"`
	Digest   string   `json:"digest"`
	Objects  []string `json:"objects"`
	Manifest []byte   `json:"manifest"`
	Version  int      `json:"version"`
}

// digestOf is the sha256 over the length-prefixed parts, so no part can shift into another.
func digestOf(etag string, manifest []byte, sums []string) string {
	hash := sha256.New()

	write := func(part []byte) {
		_, _ = hash.Write([]byte(strconv.Itoa(len(part))))
		_, _ = hash.Write([]byte{':'})
		_, _ = hash.Write(part)
	}

	write([]byte(etag))
	write(manifest)

	for _, sum := range sums {
		write([]byte(sum))
	}

	return hex.EncodeToString(hash.Sum(nil))
}

// isSum reports whether name is a sha256 in lower case hex: the name of an object file.
func isSum(name string) bool {
	if len(name) != sha256.Size*2 {
		return false
	}

	for _, char := range name {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}

	return true
}
