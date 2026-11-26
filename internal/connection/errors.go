package connection

import (
	"errors"
	"fmt"
)

// ErrUnknownConnection matches (errors.Is) the [UnknownConnectionError].
var ErrUnknownConnection = errors.New("unknown connection")

// ErrInvalidSpec matches (errors.Is) the [SpecError].
var ErrInvalidSpec = errors.New("invalid connection spec")

// ErrWrongKind matches (errors.Is) the [KindError].
var ErrWrongKind = errors.New("connection kind mismatch")

// UnknownConnectionError is returned when a name has no spec in the factory.
type UnknownConnectionError struct {
	Name string
}

// Error implements error.
func (e *UnknownConnectionError) Error() string {
	return fmt.Sprintf("unknown connection %q", e.Name)
}

// Is reports whether target is [ErrUnknownConnection].
func (*UnknownConnectionError) Is(target error) bool { return target == ErrUnknownConnection }

// SpecError is returned for a spec that fails validation. It never contains a secret value.
type SpecError struct {
	Connection string
	Field      string
	Reason     string
}

// Error implements error.
func (e *SpecError) Error() string {
	if e.Connection == "" {
		return fmt.Sprintf("invalid connection spec: %s: %s", e.Field, e.Reason)
	}

	return fmt.Sprintf("invalid connection %q: %s: %s", e.Connection, e.Field, e.Reason)
}

// Is reports whether target is [ErrInvalidSpec].
func (*SpecError) Is(target error) bool { return target == ErrInvalidSpec }

// KindError is returned when a client is requested from a connection of another kind.
type KindError struct {
	Name string
	Want string
	Got  string
}

// Error implements error.
func (e *KindError) Error() string {
	return fmt.Sprintf("connection %q is of kind %q, not %q", e.Name, e.Got, e.Want)
}

// Is reports whether target is [ErrWrongKind].
func (*KindError) Is(target error) bool { return target == ErrWrongKind }
