package events

import (
	"errors"
	"fmt"
)

var (
	// ErrUnknownKind is matched when an exporter or notifier has a kind the package does not build.
	ErrUnknownKind = errors.New("events: unknown kind")
	// ErrInvalidSettings is matched when the settings of an entry do not fit its kind.
	ErrInvalidSettings = errors.New("events: invalid settings")
	// ErrUnsupportedEventType is matched by an exporter of an event type the dispatcher does not carry yet.
	ErrUnsupportedEventType = errors.New("events: unsupported event type")
	// ErrUnknownConnection is matched when an entry names a connection that is not defined.
	ErrUnknownConnection = errors.New("events: unknown connection")
	// ErrWrongConnectionKind is matched when the connection of an entry has the wrong type for its kind.
	ErrWrongConnectionKind = errors.New("events: wrong connection type")
	// ErrUnresolvedSecret is matched when a secret of an entry cannot be read.
	ErrUnresolvedSecret = errors.New("events: secret cannot be resolved")
	// ErrConnectionClient is matched when the connection factory cannot give the client of a connection.
	ErrConnectionClient = errors.New("events: connection client")
	// ErrClosed is matched by an operation on a component that was shut down.
	ErrClosed = errors.New("events: closed")
	// ErrAlreadyStarted is matched by a second Start.
	ErrAlreadyStarted = errors.New("events: already started")
)

// EntryError names the exporter or notifier that could not be built: its section, its index in the sorted
// names of the section and its name. It never holds the value of a secret.
type EntryError struct {
	Err      error
	Resource string
	Name     string
	Index    int
}

// Error implements error.
func (e *EntryError) Error() string {
	return fmt.Sprintf("%s[%d] %q: %v", e.Resource, e.Index, e.Name, e.Err)
}

// Unwrap returns the cause.
func (e *EntryError) Unwrap() error { return e.Err }

func settingsError(field, why string) error {
	return fmt.Errorf("%w: %s %s", ErrInvalidSettings, field, why)
}
