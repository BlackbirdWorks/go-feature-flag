package statedir

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// envelope is the content of manifest.json: the accepted manifest with its ETag, the objects saved with it (checksum
// and size), the names that were served stale (and the content each of them is served from), and a digest of all
// of that. A flipped or missing byte anywhere makes the digest fail and the file is ignored.
type envelope struct {
	ETag     string      `json:"etag"`
	Digest   string      `json:"digest"`
	Objects  []objectRef `json:"objects"`
	Stale    []staleRef  `json:"stale,omitempty"`
	Manifest []byte      `json:"manifest"`
	Version  int         `json:"version"`
}

// objectRef names an object file and the size it must have.
type objectRef struct {
	Sum  string `json:"sum"`
	Size int64  `json:"size"`
}

// staleRef is a name that was served from earlier content: the entry (kind, format, checksum) of that content.
type staleRef struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Format    string `json:"format"`
	Algorithm string `json:"algorithm"`
	Sum       string `json:"sum"`
}

// digestOf is the sha256 over the length-prefixed parts, so no part can shift into another.
func digestOf(etag string, manifest []byte, objects []objectRef, stale []staleRef) string {
	hash := sha256.New()

	write := func(part string) {
		_, _ = hash.Write([]byte(strconv.Itoa(len(part))))
		_, _ = hash.Write([]byte{':'})
		_, _ = hash.Write([]byte(part))
	}

	write(etag)
	write(string(manifest))

	for _, ref := range objects {
		write(ref.Sum + ":" + strconv.FormatInt(ref.Size, 10))
	}

	write("stale")

	for _, ref := range stale {
		write(strings.Join([]string{ref.Name, ref.Kind, ref.Format, ref.Algorithm, ref.Sum}, "\x00"))
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

// tmpRandomBytes is the number of random bytes in a temporary name: twice as many hex digits.
const tmpRandomBytes = 8

// newTmpName returns a new temporary name: ".tmp-" and exactly 2 x tmpRandomBytes lower case hex digits. It is the one
// definition of the names this package creates, and [isOurTmp] is the one definition of the names it recognises, so
// they cannot drift: anything else that starts with ".tmp-" (".tmp-abc", a decimal name, other lengths) belongs to
// somebody else.
func newTmpName() (string, error) {
	var random [tmpRandomBytes]byte

	_, err := rand.Read(random[:])
	if err != nil {
		return "", fmt.Errorf("statedir: random name: %w", err)
	}

	return tmpPrefix + hex.EncodeToString(random[:]), nil
}

// isOurTmp reports whether name is a temporary name that [newTmpName] makes. A name from before the random names
// (".tmp-" and a decimal number) was never in a release: it is somebody else's file.
func isOurTmp(name string) bool {
	suffix, ok := strings.CutPrefix(name, tmpPrefix)
	if !ok || len(suffix) != 2*tmpRandomBytes {
		return false
	}

	for _, char := range suffix {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}

	return true
}
