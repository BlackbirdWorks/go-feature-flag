package valkeysegment

import (
	"fmt"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
)

// The defaults and limits are the ones of package redissegment.
const (
	// DefaultTimeout is the per-call timeout when Options.Timeout is zero.
	DefaultTimeout = redissegment.DefaultTimeout
	// DefaultKeyPrefix is the prefix of the keys of the segments when Options.KeyPrefix is empty.
	DefaultKeyPrefix = redissegment.DefaultKeyPrefix
	// DefaultMaxIDBytes is the longest id that is looked up when Options.MaxIDBytes is zero.
	DefaultMaxIDBytes = redissegment.DefaultMaxIDBytes
	// MaxIDsPerCommand is the most ids that one SMISMEMBER carries: a segment with more ids in a call is several
	// commands in the same DoMulti.
	MaxIDsPerCommand = redissegment.MaxIDsPerCommand
	// MaxInitProbes is the most namespaces that Init probes.
	MaxInitProbes = redissegment.MaxInitProbes
)

// Options configures a Provider. There is no Atomic mode: the provider sends no script.
type Options struct {
	// Segments maps a segment name to where its members are. A lookup of a segment that is not here stays Unknown:
	// a flag can never choose a key.
	Segments map[string]SegmentOptions
	// Observer (optional) receives one Observation per call, except for a call with no lookup and a call after
	// Shutdown.
	Observer Observer
	// KeyPrefix is the default prefix of the keys. Empty means DefaultKeyPrefix.
	KeyPrefix string
	// MissingKey is what a missing set key means. Empty means MissingUnknown.
	MissingKey MissingKey
	// MaxIDBytes is the longest id, in bytes, that the provider looks up. A longer id stays Unknown and is not sent.
	// Zero means DefaultMaxIDBytes.
	MaxIDBytes int
	// Timeout bounds one call, under the deadline of the caller. Zero means DefaultTimeout.
	Timeout time.Duration
}

func (o *Options) validate() error {
	if o.Timeout < 0 {
		return fmt.Errorf("%w: %w", ErrInvalidOptions, errNegativeTimeout)
	}

	if o.MaxIDBytes < 0 {
		return fmt.Errorf("%w: %w", ErrInvalidOptions, errNegativeMaxID)
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
