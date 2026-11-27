package valkeysegment

import (
	"errors"
	"fmt"
)

// Class is the kind of failure of a call.
type Class uint8

const (
	// ClassNone means no failure.
	ClassNone Class = iota
	// ClassCanceled means the context of the caller ended (cancelled or its own deadline passed).
	ClassCanceled
	// ClassTimeout means the per-call timeout of the provider (or a network timeout) passed.
	ClassTimeout
	// ClassUnavailable means the store could not be reached or the connection broke.
	ClassUnavailable
	// ClassServer means the store answered with an error (no permission, wrong key type, loading, cluster down).
	ClassServer
	// ClassProtocol means the store answered with a reply that is not the one the command returns.
	ClassProtocol

	numClasses
)

// Classes lists the failure classes, ClassNone excluded.
func Classes() []Class {
	return []Class{ClassCanceled, ClassTimeout, ClassUnavailable, ClassServer, ClassProtocol}
}

// String implements [fmt.Stringer].
func (c Class) String() string {
	switch c {
	case ClassNone:
		return "none"
	case ClassCanceled:
		return "canceled"
	case ClassTimeout:
		return "timeout"
	case ClassUnavailable:
		return "unavailable"
	case ClassServer:
		return "server"
	case ClassProtocol:
		return "protocol"
	case numClasses:
		return "invalid"
	}

	return "invalid"
}

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

	// ErrInvalidOptions is wrapped by the errors of options that are not valid.
	ErrInvalidOptions = errors.New("valkey segment: invalid options")
	// ErrClosed is returned by a call after Shutdown.
	ErrClosed = errors.New("valkey segment: closed")
	// ErrContextTimeoutDisabled is wrapped by the error of New for a go-redis client without ContextTimeoutEnabled.
	ErrContextTimeoutDisabled = errors.New("valkey segment: the client must set ContextTimeoutEnabled")
	// ErrNilClient is returned when the client is nil.
	ErrNilClient = errors.New("valkey segment: nil client")

	errNegativeTimeout = errors.New("timeout must not be negative")
	errInvalidName     = errors.New("invalid segment name")
	errInvalidLayout   = errors.New("invalid layout: want set or keys")
	errInvalidMissing  = errors.New("invalid missingKey: want unknown or notMember")
)

// LookupError is returned by Contains when at least one lookup could not be answered. It holds the class of the
// failure and counts only: never an id, a key or an address.
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

// Unwrap returns the sentinel of the class, so [errors.Is] with ErrTimeout works.
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
	case ClassNone, numClasses:
		return nil
	}

	return nil
}
