package manifestreader

import (
	"slices"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// State is the state of one object name in a [Snapshot]. The set is closed.
type State string

const (
	// StateOK means the bytes are the verified content of the checksum the manifest of this generation lists.
	StateOK State = "ok"
	// StateStale means the fetch or the verification of the listed content failed, and an earlier verified
	// content of the same name exists: the snapshot carries those bytes and their own checksum.
	StateStale State = "stale"
	// StateMissing means the fetch or the verification failed and no content of the name was ever loaded.
	StateMissing State = "missing"
)

// Reasons of a failed object, besides [ReasonError] and [ReasonDenied] of the store. The set is closed.
const (
	// ReasonMissing is a 404 answer: the manifest lists an object that is not there.
	ReasonMissing = "missing"
	// ReasonChecksumMismatch is a download whose sha256 is not the listed one.
	ReasonChecksumMismatch = "checksum-mismatch"
	// ReasonTooLarge is an entry whose listed size is above the object limit: it is not requested.
	ReasonTooLarge = "too-large"
	// ReasonNotRestored is an object that the restored state did not have: it is fetched after the first poll of
	// the live manifest.
	ReasonNotRestored = "not-restored"
	// ReasonInvalidKey is an entry whose key is empty or starts with "/": keys are absolute bucket keys, and the
	// entry is not requested.
	ReasonInvalidKey = "invalid-key"
	// ReasonInvalidSize is an entry whose listed size is below 1: it is not requested.
	ReasonInvalidSize = "invalid-size"
	// ReasonSizeMismatch is a download that is shorter or longer than the listed size.
	ReasonSizeMismatch = "size-mismatch"
	// ReasonUnknownConnection is an object of a connection the reader has no store for.
	ReasonUnknownConnection = "unknown-connection"
	// ReasonUnsupportedVersion is an object whose content version is newer than this reader supports: it is not
	// requested, keeping its last good content as stale (or missing if none).
	ReasonUnsupportedVersion = string(controlplane.CodeUnsupportedVersion)
)

// ObjectState is what a snapshot says about one object name.
type ObjectState struct {
	// Kind and Format are those of the entry that Bytes belong to ("flags" or "flagSets", "json", "yaml", "toml").
	Kind   string
	Format string
	// Checksum is the checksum of Bytes. For a missing or pending object it is the listed checksum.
	Checksum controlplane.Checksum
	// State and Reason: Reason is empty for an ok object, and for the others the closed code of the failure
	// (the Reason* constants).
	State  State
	Reason string
	// Bytes is the verified content, nil for a missing or pending object. It is shared with other names of the
	// same checksum and with later snapshots: never change it.
	Bytes []byte
}

// Snapshot is the immutable result of one generation of the manifest. A later snapshot of the same
// generation carries a state change of an object (a retried object that succeeded or failed again).
type Snapshot struct {
	// Objects maps the name of every object of the manifest to its state. Never change it.
	Objects map[string]ObjectState
	// Channel is the channel of the manifest.
	Channel string
	// Generation is the generation of the manifest.
	Generation uint64
	// Revision is the sequence number of the snapshot: the reader numbers every snapshot it emits, from 1, so a second
	// snapshot at the same generation (an object that recovered) has a revision of its own.
	Revision uint64
}

// Names returns the object names in sorted order.
func (s *Snapshot) Names() []string {
	names := make([]string, 0, len(s.Objects))
	for name := range s.Objects {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}
