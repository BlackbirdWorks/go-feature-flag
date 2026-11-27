package relaycore

import "errors"

// Sentinel errors of the package.
var (
	// ErrNoControlPlane is returned by [New] and [Core.Reload] for a bootstrap file without a controlPlane section.
	ErrNoControlPlane = errors.New("the bootstrap file has no controlPlane section")
	// ErrUnknownConnection is returned when a section names a connection that the file does not define.
	ErrUnknownConnection = errors.New("unknown connection")
	// ErrNilConfig is returned when no bootstrap configuration is given.
	ErrNilConfig = errors.New("no bootstrap configuration")
	// ErrClosed is returned by [Core.Run] and [Core.Reload] after [Core.Shutdown].
	ErrClosed = errors.New("the relay core is shut down")
	// ErrAlreadyRunning is returned by a second [Core.Run].
	ErrAlreadyRunning = errors.New("the relay core runs already")
	// ErrNotDrained is returned by [Core.Shutdown] when the deadline passed before everything stopped.
	ErrNotDrained = errors.New("the relay core did not stop in time")
)

// BuildError says which setting of the bootstrap file made a build step fail. Path is the dotted path of the
// setting (connections.control.path); Err never holds a secret, only names.
type BuildError struct {
	Err  error
	Path string
}

// Error implements error.
func (e *BuildError) Error() string { return e.Path + ": " + e.Err.Error() }

// Unwrap returns the cause.
func (e *BuildError) Unwrap() error { return e.Err }
