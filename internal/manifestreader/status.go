package manifestreader

import "time"

// Results of a poll, in [PollStatus.Result]. The set is closed.
const (
	// PollNone is the result before the first round ended.
	PollNone = "none"
	// PollNotModified is a 304 answer to the conditional GET.
	PollNotModified = "not_modified"
	// PollUpdated is a 200 answer that was read: see [ManifestStatus] for what was done with it.
	PollUpdated = "updated"
	// PollNotFound is a 404 answer: the manifest is gone, the previous snapshot stays.
	PollNotFound = "not_found"
	// PollTooLarge is a manifest larger than the byte limit.
	PollTooLarge = "too_large"
	// PollError is any other failed request; Reason says what kind.
	PollError = "error"
)

// Outcomes of the last manifest the reader read, in [ManifestStatus.Outcome]. The set is closed.
const (
	// OutcomeNone: no manifest was read yet.
	OutcomeNone = "none"
	// OutcomeAccepted: the manifest is valid and is the held one.
	OutcomeAccepted = "accepted"
	// OutcomeInvalid: the manifest could not be decoded or is not valid; the previous one stays.
	OutcomeInvalid = "invalid"
	// OutcomeLowerGeneration: the generation is lower than the held one; the manifest is ignored.
	OutcomeLowerGeneration = "lower-generation"
	// OutcomeConflict: same generation as the held one with another object set or checksum (a publisher bug);
	// the manifest is rejected.
	OutcomeConflict = "generation-conflict"
	// OutcomeChannelMismatch: the manifest is of another channel than the expected one.
	OutcomeChannelMismatch = "channel-mismatch"
)

// PollStatus describes the last poll.
type PollStatus struct {
	// At is the time the last round ended (the repository clock). It is zero before the first round.
	At time.Time
	// Result is a Poll* constant.
	Result string
	// Reason is the closed code of a failed poll (a Reason* constant of the store or the reader), else empty.
	Reason string
}

// ManifestStatus describes the manifest the reader holds and the last one it read.
type ManifestStatus struct {
	// Outcome is an Outcome* constant: what happened to the last manifest the reader read. A 304 does not
	// change it: it confirms the same bytes.
	Outcome string
	// Reason is the closed code of the problem (a controlplane validation code, "decode", "unsupported-version",
	// "wrong-kind", "too-many-objects", ...), empty when accepted.
	Reason string
	// Channel and Generation are those of the held manifest, zero before the first accepted one.
	Channel    string
	Generation uint64
	// Objects is the number of objects of the held manifest.
	Objects int
	// UnknownFields counts the fields of the last read manifest that this version does not know.
	UnknownFields int
}

// ObjectFailure is an object that is not ok. It never holds bytes.
type ObjectFailure struct {
	// Name is the object name of the manifest.
	Name string
	// State is [StateStale] or [StateMissing].
	State State
	// Reason is the closed code of the failure.
	Reason string
	// Checksum is the listed (wanted) sha256 value.
	Checksum string
	// Retryable tells whether a retry can succeed without a change of configuration.
	Retryable bool
}

// Status is the immutable state of the reader at the end of a round. Take it with [Reader.Status]; never
// change the slice.
type Status struct {
	LastSync time.Time
	Poll     PollStatus
	Failures []ObjectFailure
	// HookPanics lists the hooks that panicked (recovered), with counts; empty when none.
	HookPanics []HookPanic
	// PollResults counts the polls by result (the Poll* constants), sorted by result; results that did not
	// happen are absent.
	PollResults []PollCount
	Manifest    ManifestStatus
	State       StateStatus
	// Backoff is the backoff state: the consecutive failures of the manifest poll and the wait planned after
	// this round.
	Backoff     BackoffStatus
	Counts      Counts
	Polls       uint64
	NotModified uint64
}

// ReasonTotalTooLarge is the [ManifestStatus.Reason] of a manifest whose distinct objects add up to more than
// [Config.MaxTotalBytes]: it is rejected whole, before any object request.
const ReasonTotalTooLarge = "too-large-total"

// Results of the restore of the saved state, in [StateStatus.Restore]. The set is closed.
const (
	// RestoreNone: the restore did not run, or no state hook is installed.
	RestoreNone = ""
	// RestoreRestored: a manifest and all its objects were restored.
	RestoreRestored = "restored"
	// RestorePartial: the manifest was restored, some objects were discarded (missing or corrupt).
	RestorePartial = "partial"
	// RestoreEmpty: there was no usable saved state (empty, other layout version, corrupt manifest).
	RestoreEmpty = "empty"
	// RestoreFailed: the saved state could not be read at all (an unreadable directory).
	RestoreFailed = "failed"
)

// StateStatus is what the persistence behind the hooks reports. A hook that is not installed reports the zero value.
type StateStatus struct {
	// LastSave is the time of the last write that ended, zero when none did.
	LastSave time.Time
	// Restore is a Restore* constant.
	Restore string
	// LastSaveError is the closed code of the failure of the last write, empty when it worked.
	LastSaveError string
	// Saves and SaveErrors count the writes that ended, and the ones that failed.
	Saves      uint64
	SaveErrors uint64
	// RestoredObjects and DiscardedObjects count the saved objects that were restored and the ones that were
	// discarded as missing or corrupt.
	RestoredObjects  int
	DiscardedObjects int
	// Enabled tells that a state hook is installed.
	Enabled bool
	// Pending tells that a write is queued or running.
	Pending bool
}
