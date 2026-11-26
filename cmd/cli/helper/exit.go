package helper

import "errors"

// The exit codes of the commands that report more than success or failure. A command returns an [ExitError] to
// choose one; every other error exits with [ExitFailure].
const (
	// ExitFailure is the exit code of a failure with no more specific code (also bad usage).
	ExitFailure = 1
	// ExitInvalid is the exit code of invalid input: documents, flags or a configuration file.
	ExitInvalid = 2
	// ExitConflict is the exit code of a lost race: another publisher changed the manifest. Nothing visible changed.
	ExitConflict = 3
	// ExitTransport is the exit code of a failed call to the store (network, access, throttling). Repeating is safe.
	ExitTransport = 4
	// ExitVerify is the exit code of a verification that found problems in the bucket.
	ExitVerify = 5
)

// ExitError is an error that carries the exit code of the process.
type ExitError struct {
	// Err is the error to print.
	Err error
	// Code is the exit code.
	Code int
}

// Error implements error.
func (e *ExitError) Error() string { return e.Err.Error() }

// Unwrap returns the wrapped error.
func (e *ExitError) Unwrap() error { return e.Err }

// WithExitCode wraps err so that the process exits with code. A nil err stays nil.
func WithExitCode(err error, code int) error {
	if err == nil {
		return nil
	}

	return &ExitError{Err: err, Code: code}
}

// ExitCodeOf returns the exit code that err asks for, [ExitFailure] when it asks for none.
func ExitCodeOf(err error) int {
	var exit *ExitError
	if errors.As(err, &exit) && exit.Code > 0 {
		return exit.Code
	}

	return ExitFailure
}
