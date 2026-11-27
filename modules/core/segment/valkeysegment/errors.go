package valkeysegment

import (
	"errors"
	"fmt"

	"github.com/blackbirdworks/semaphore/modules/core/segment"

	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
)

// The observability types are the ones of the go-redis provider, so one metrics wiring serves both.
type (
	// Class is the kind of failure of a call.
	Class = redissegment.Class
	// Observation describes one call of Contains.
	Observation = redissegment.Observation
	// Observer receives one Observation per call of Contains that reached the provider.
	Observer = redissegment.Observer
	// Counters is an Observer that counts with atomics.
	Counters = redissegment.Counters
	// Layout is how the members of a segment are stored.
	Layout = redissegment.Layout
	// MissingKey is what a lookup of a set segment becomes when the key of the set does not exist.
	MissingKey = redissegment.MissingKey
	// SegmentOptions says where the members of one segment are.
	SegmentOptions = redissegment.SegmentOptions
)

// The classes, the layouts and the missing key modes, as in package redissegment.
const (
	ClassNone        = redissegment.ClassNone
	ClassCanceled    = redissegment.ClassCanceled
	ClassTimeout     = redissegment.ClassTimeout
	ClassUnavailable = redissegment.ClassUnavailable
	ClassServer      = redissegment.ClassServer
	ClassProtocol    = redissegment.ClassProtocol

	LayoutSet  = redissegment.LayoutSet
	LayoutKeys = redissegment.LayoutKeys

	MissingUnknown   = redissegment.MissingUnknown
	MissingNotMember = redissegment.MissingNotMember
)

var (
	// ErrCanceled is wrapped by a LookupError of ClassCanceled.
	ErrCanceled = errors.New("valkey segment: context ended")
	// ErrTimeout is wrapped by a LookupError of ClassTimeout.
	ErrTimeout = errors.New("valkey segment: timeout")
	// ErrUnavailable is wrapped by a LookupError of ClassUnavailable.
	ErrUnavailable = errors.New("valkey segment: store unavailable")
	// ErrServer is wrapped by a LookupError of ClassServer.
	ErrServer = errors.New("valkey segment: store returned an error")
	// ErrProtocol is wrapped by a LookupError of ClassProtocol.
	ErrProtocol = errors.New("valkey segment: unexpected reply")
	// ErrInvalidOptions is wrapped by the error of New and Dial for options that cannot work.
	ErrInvalidOptions = errors.New("valkey segment: invalid options")
	// ErrClosed is returned by a call after Shutdown.
	ErrClosed = errors.New("valkey segment: closed")
	// ErrNoVersionKey is returned by Version for a segment that has no SegmentOptions.VersionKey.
	ErrNoVersionKey = fmt.Errorf("%w: %s segment: no version key is configured", segment.ErrNoVersion, "valkey")
	// ErrSnapshotLayout is yielded by Members for a segment of LayoutKeys: a snapshot of the keys layout would need
	// SCAN over the whole keyspace of the node (and of every node of a cluster), see Members.
	ErrSnapshotLayout = errors.New("valkey segment: the snapshot mode needs the set layout")
	// ErrNilClient is returned by New and Dial for a nil client or nil options.
	ErrNilClient = errors.New("valkey segment: nil client")
	// ErrInit is wrapped, with the sentinel of the class, by the error of Init.
	ErrInit = errors.New("valkey segment: init failed")

	errNegativeTimeout   = errors.New("timeout is negative")
	errNegativeMaxID     = errors.New("max id bytes is negative")
	errInvalidMissing    = errors.New("missing key must be unknown or notMember")
	errInvalidName       = errors.New("invalid segment name")
	errNegativeBlocking  = errors.New("blocking pipeline is negative")
	errInvalidPipelining = errors.New("pipelining must be always or auto")
	errInvalidLayout     = errors.New("layout must be set or keys")
)

// LookupError is the error of a call that could not answer all its lookups. It holds the class and the counts only.
type LookupError struct {
	// Class is the most severe class among the failures of the call.
	Class Class
	// Failed is the number of lookups left Unknown by a failure.
	Failed int
	// Total is the number of lookups of the call.
	Total int
}

// Error implements error.
func (e *LookupError) Error() string {
	return fmt.Sprintf("valkey segment: %s: %d of %d lookups failed", e.Class, e.Failed, e.Total)
}

// Unwrap returns the sentinel of the class, for [errors.Is].
func (e *LookupError) Unwrap() error {
	switch e.Class {
	case ClassCanceled:
		return ErrCanceled
	case ClassTimeout:
		return ErrTimeout
	case ClassUnavailable:
		return ErrUnavailable
	case ClassServer:
		return ErrServer
	case ClassProtocol:
		return ErrProtocol
	case ClassNone:
	}

	return nil
}
