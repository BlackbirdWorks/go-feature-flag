package segment

import (
	"errors"
	"fmt"
	"time"
)

// Mode is how a segment that lives in a store is served. The mode is a property of the definition; the store kind
// decides which modes it can serve (see [RequireMode]).
type Mode string

const (
	// ModeSnapshot pulls the whole member set from the store like a retriever, holds it in memory, polls a version
	// token for change and swaps the set atomically. It is the default. When the store is down the last good set
	// keeps answering.
	ModeSnapshot Mode = "snapshot"
	// ModeLookup asks the store for each evaluation (one batched call) and keeps a bounded LRU of the answers, each
	// stamped with the segment version it was fetched under. It holds the least memory and costs a network call on a
	// miss.
	ModeLookup Mode = "lookup"
)

const (
	// MaxVersionLen is the longest Version.Value or Version.Key.
	MaxVersionLen = 512
	// MinVersionInterval is the shortest Version.Interval.
	MinVersionInterval = 100 * time.Millisecond
	// MaxVersionInterval is the longest Version.Interval.
	MaxVersionInterval = 24 * time.Hour
)

var (
	errInvalidMode        = errors.New("invalid mode: want snapshot or lookup")
	errVersionBoth        = errors.New("invalid version: value and key are mutually exclusive")
	errVersionIntervalKey = errors.New("invalid version: interval needs a key")
	errVersionTooLong     = errors.New("invalid version: value or key is longer than 512 bytes")
	errVersionInterval    = errors.New("invalid version: interval must be between 100ms and 24h")
)

// Effective returns the mode that applies: ModeSnapshot when the mode is empty.
func (m Mode) Effective() Mode {
	if m == "" {
		return ModeSnapshot
	}

	return m
}

// Valid reports whether the mode is empty (the default), snapshot or lookup.
func (m Mode) Valid() bool {
	return m == "" || m == ModeSnapshot || m == ModeLookup
}

// VersionSpec says how the version of a segment is learned. The version is an opaque token that is compared for
// equality: a cached answer or a pulled member set is valid for the version it was read under.
//
// With Value, the publisher of the definition bumps the version. With Key, the version is read from a key of the store
// that the writer of the members bumps, and Interval is how often it is polled (zero means the process default).
// With neither, no version exists and only the time-to-live of the cache applies.
type VersionSpec struct {
	// Value is a version that the definition carries.
	Value string
	// Key is the name of the version key in the store.
	Key string
	// Interval is the poll period of Key.
	Interval time.Duration
}

// Validate returns an error when the spec is not valid. A nil or zero spec is valid.
func (v *VersionSpec) Validate() error {
	if v == nil {
		return nil
	}

	switch {
	case v.Value != "" && v.Key != "":
		return errVersionBoth
	case v.Interval != 0 && v.Key == "":
		return errVersionIntervalKey
	case len(v.Value) > MaxVersionLen || len(v.Key) > MaxVersionLen:
		return errVersionTooLong
	case v.Interval != 0 && (v.Interval < MinVersionInterval || v.Interval > MaxVersionInterval):
		return errVersionInterval
	}

	return nil
}

func (d Definition) validateModeVersion() error {
	if !d.Mode.Valid() {
		return fmt.Errorf("%w: %q: %w, got %q", ErrInvalidDefinition, d.Name, errInvalidMode, d.Mode)
	}

	err := d.Version.Validate()
	if err != nil {
		return fmt.Errorf("%w: %q: %w", ErrInvalidDefinition, d.Name, err)
	}

	return nil
}
