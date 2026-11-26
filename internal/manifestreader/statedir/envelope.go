package statedir

import (
	"crypto/sha256"
	"encoding/hex"
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

// isOurTmp reports whether name is a temporary file of this package: ".tmp-" and lower case hex (the name is random).
func isOurTmp(name string) bool {
	suffix, ok := strings.CutPrefix(name, tmpPrefix)
	if !ok || suffix == "" {
		return false
	}

	for _, char := range suffix {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}

	return true
}
