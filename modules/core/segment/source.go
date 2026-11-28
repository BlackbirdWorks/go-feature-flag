package segment

import (
	"context"
	"errors"
	"fmt"
	"iter"
)

// ErrSegmentMissing is yielded by [SnapshotSource.Members] for a segment that does not exist in the store. A segment
// that exists and has no member yields nothing: missing and empty are different. What a missing segment means (no
// member by default, or Unknown) is the policy of the consumer, see [MissingKey].
var ErrSegmentMissing = errors.New("the segment does not exist in the store")

// ErrNoVersion is wrapped by the error of a [Versioner] that has no version for the segment (no version key is
// configured). A snapshot holder then cannot tell a change and pulls the members on every Refresh: that costs one full
// read for each poll.
var ErrNoVersion = errors.New("the segment has no version")

// ErrUnsupportedMode is wrapped by the error of [RequireMode].
var ErrUnsupportedMode = errors.New("the store does not support the segment mode")

// Versioner tells the current version of a segment. It is the baseline of change detection: a caller polls it off the
// request path. A token is opaque and only compared for equality; a segment that has no version yet has a fixed token
// of the store (not an error), so "never bumped" is stable. A store may instead report a segment that does not exist
// with [ErrSegmentMissing]: that is an answer, not a failure, and a snapshot holder treats it like a stream that
// reports it. A writer that deletes or replaces a segment must change its version: a holder that sees the same token
// does not pull again, so a deletion under an unchanged token is not seen. Version must honour ctx and be safe for
// concurrent use.
type Versioner interface {
	Version(ctx context.Context, segment string) (string, error)
}

// SnapshotSource is the contract of a store that serves the snapshot mode. Version is the cheap change token;
// Members streams the members, never as one slice: a failure while streaming is yielded as ("", err) and ends the
// sequence, a segment that does not exist yields ("", [ErrSegmentMissing]) and an existing empty one yields nothing.
// The caller reads the version before the members, so a write between the two is picked up by the next poll.
type SnapshotSource interface {
	Versioner
	Members(ctx context.Context, segment string) iter.Seq2[string, error]
}

// Change reports that a segment is now at Version.
type Change struct {
	Segment string
	Version string
}

// ChangeNotifier is implemented by a store that can tell that a segment changed, instead of being polled. The
// subscription starts when Changes returns and ends with ctx; the sequence yields ("", err) on a failure of the
// subscription and then ends. A consumer that misses a change is still correct when it also polls.
type ChangeNotifier interface {
	Changes(ctx context.Context) (iter.Seq2[Change, error], error)
}

// SupportsMode reports whether store can serve the mode: a [Provider] serves lookup, a [SnapshotSource] serves
// snapshot. An empty mode is snapshot.
func SupportsMode(store any, mode Mode) bool {
	switch mode.Effective() {
	case ModeLookup:
		_, ok := store.(Provider)

		return ok
	case ModeSnapshot:
		_, ok := store.(SnapshotSource)

		return ok
	}

	return false
}

// RequireMode returns nil when store can serve the mode, else an error that wraps [ErrUnsupportedMode] and names the
// segment, the mode and the kind of the store (for example "redis").
func RequireMode(segmentName string, mode Mode, kind string, store any) error {
	if SupportsMode(store, mode) {
		return nil
	}

	return fmt.Errorf("%w: segment %q: mode %q, store %q", ErrUnsupportedMode, segmentName, mode.Effective(), kind)
}

// RequireVersioner returns nil when store can tell versions (a segment with a version key needs one), else an error
// that wraps [ErrUnsupportedMode].
func RequireVersioner(segmentName, kind string, store any) error {
	if _, ok := store.(Versioner); ok {
		return nil
	}

	return fmt.Errorf("%w: segment %q: a version key needs a store that can tell versions, store %q",
		ErrUnsupportedMode, segmentName, kind)
}

// IsStoreAnswered reports whether err, or an error that it wraps, says that the store answered with an error about one
// set: the store itself works. An error says so with a method StoreAnswered() bool that returns true; the failure
// classes of the stores that the relay serves for a key of the wrong type or a reply of the wrong shape do.
func IsStoreAnswered(err error) bool {
	var answered interface{ StoreAnswered() bool }

	return errors.As(err, &answered) && answered.StoreAnswered()
}
