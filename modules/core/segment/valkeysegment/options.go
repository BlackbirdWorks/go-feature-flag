package valkeysegment

import (
	"fmt"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// Layout is how the members of a segment are stored.
type Layout string

const (
	// LayoutSet stores the members in one set. A lookup is SMISMEMBER (Redis 6.2, Valkey 7.2 and later). The
	// lookups of a segment in a call are one SMISMEMBER.
	LayoutSet Layout = "set"
	// LayoutKeys stores one key per member, named <prefix><id>. A lookup is EXISTS.
	LayoutKeys Layout = "keys"
)

// MissingKey is what a lookup of a set segment becomes when the key of the set does not exist.
type MissingKey string

const (
	// MissingUnknown answers Unknown: the segment is not loaded. It is the default, and it is fail-closed because
	// the engine applies the failure policy to it. An empty set does not exist in Valkey, so an emptied segment is
	// also Unknown.
	MissingUnknown MissingKey = "unknown"
	// MissingNotMember answers NotMember: a missing key is an empty segment. It saves the EXISTS command.
	MissingNotMember MissingKey = "notMember"
)

const (
	// DefaultTimeout is the per-call timeout when Options.Timeout is zero.
	DefaultTimeout = 100 * time.Millisecond
	// DefaultKeyPrefix is the prefix of the keys of the segments when Options.KeyPrefix is empty.
	DefaultKeyPrefix = "semaphore:segment:"
)

// SegmentOptions says where the members of one segment are.
type SegmentOptions struct {
	// Layout is LayoutSet (default) or LayoutKeys.
	Layout Layout
	// Key is the key of the set (LayoutSet) or the prefix of the member keys (LayoutKeys). Empty means
	// <KeyPrefix>{<name>} for a set (the name is a hash tag, so a temporary key named with the same tag lands in
	// the same cluster slot and RENAME works) and <KeyPrefix><name>: for keys.
	Key string
}

// Options configures a Provider.
type Options struct {
	// Segments maps a segment name to where its members are. A lookup of a segment that is not here stays Unknown:
	// a flag can never choose a key.
	Segments map[string]SegmentOptions
	// Observer (optional) receives one Observation per call.
	Observer Observer
	// KeyPrefix is the default prefix of the keys. Empty means DefaultKeyPrefix.
	KeyPrefix string
	// MissingKey is what a missing set key means. Empty means MissingUnknown.
	MissingKey MissingKey
	// Timeout bounds one call, under the deadline of the caller. Zero means DefaultTimeout.
	Timeout time.Duration
}

func (o Options) validate() error {
	if o.Timeout < 0 {
		return fmt.Errorf("%w: %w", ErrInvalidOptions, errNegativeTimeout)
	}

	switch o.MissingKey {
	case "", MissingUnknown, MissingNotMember:
	default:
		return fmt.Errorf("%w: %w, got %q", ErrInvalidOptions, errInvalidMissing, o.MissingKey)
	}

	for name, seg := range o.Segments {
		if !segment.ValidName(name) {
			return fmt.Errorf("%w: %w: %q", ErrInvalidOptions, errInvalidName, name)
		}

		switch seg.Layout {
		case "", LayoutSet, LayoutKeys:
		default:
			return fmt.Errorf("%w: %q: %w, got %q", ErrInvalidOptions, name, errInvalidLayout, seg.Layout)
		}
	}

	return nil
}
