package bootstrap

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors of the package. Every error returned by the loader wraps one of them inside an [Error].
var (
	// ErrVersionRequired is returned when the file has no "version" key or a version other than 2.
	ErrVersionRequired = errors.New("bootstrap config: version 2 required")
	// ErrSyntax is returned when the document is not valid YAML or has the wrong shape.
	ErrSyntax = errors.New("invalid YAML")
	// ErrUnknownKey is returned for a key the schema does not define.
	ErrUnknownKey = errors.New("unknown key")
	// ErrReservedKey is returned for a key that is reserved for a later release.
	ErrReservedKey = errors.New("reserved key, not supported yet")
	// ErrDuplicateName is returned when a key or a name appears twice.
	ErrDuplicateName = errors.New("duplicate name")
	// ErrInvalidName is returned for a name that does not match the name rules.
	ErrInvalidName = errors.New("invalid name")
	// ErrReservedName is returned for a reserved name.
	ErrReservedName = errors.New("reserved name")
	// ErrDanglingReference is returned for a reference to a connection that does not exist.
	ErrDanglingReference = errors.New("unknown connection")
	// ErrWrongConnectionType is returned for a reference to a connection of the wrong type.
	ErrWrongConnectionType = errors.New("connection has the wrong type")
	// ErrInvalidValue is returned for a value that is not allowed.
	ErrInvalidValue = errors.New("invalid value")
	// ErrInvalidDuration is returned for a duration that is not a positive duration string such as "60s".
	ErrInvalidDuration = errors.New("invalid duration")
	// ErrMissingValue is returned when a required key is not set.
	ErrMissingValue = errors.New("required value missing")
	// ErrNotAllowed is returned for a key that is not allowed for the type or kind of its parent.
	ErrNotAllowed = errors.New("key not allowed here")
	// ErrUnknownType is returned for a connection type or an exporter or notifier kind that does not exist.
	ErrUnknownType = errors.New("unknown type")
	// ErrUnsupportedType is returned for a connection type that is planned but not supported yet.
	ErrUnsupportedType = errors.New("type not supported yet")
	// ErrInlineSecret is returned for an inline secret when inline secrets are not enabled.
	ErrInlineSecret = errors.New("inline secret not allowed, use fromEnv or fromFile")
	// ErrInvalidSecret is returned for a secret that is neither a scalar nor a fromEnv or fromFile reference.
	ErrInvalidSecret = errors.New("invalid secret")
	// ErrDuplicateKey is returned when an admin key appears twice. The key is never printed.
	ErrDuplicateKey = errors.New("duplicate key")
	// ErrSecretUnresolved is returned when a secret reference cannot be resolved.
	ErrSecretUnresolved = errors.New("secret cannot be resolved")
	// ErrNoConfigPath is returned when neither the flag nor SEMAPHORE_CONFIG gives a file path.
	ErrNoConfigPath = errors.New("no bootstrap file given: use --config or SEMAPHORE_CONFIG")
	// ErrNoReader is returned when the options have no ReadFile function.
	ErrNoReader = errors.New("no ReadFile function in the load options")
)

// Error is a path- and line-addressed configuration error.
// It never contains the value of a secret or of an admin key. It does carry key names, type names and reference
// names written in the file, and it does not echo a key written directly under "auth".
type Error struct {
	// Err is the sentinel that tells the kind of the error.
	Err error
	// Path is the dotted path of the key, for example "connections.control.bucket".
	Path string
	// Detail is an optional explanation.
	Detail string
	// Line is the 1-based line in the file, or 0 when the value does not come from the file.
	Line int
}

// Error implements the error interface.
func (e *Error) Error() string {
	var out strings.Builder

	if e.Path != "" {
		out.WriteString(e.Path)

		if e.Line > 0 {
			fmt.Fprintf(&out, " (line %d)", e.Line)
		}

		out.WriteString(": ")
	} else if e.Line > 0 {
		fmt.Fprintf(&out, "line %d: ", e.Line)
	}

	out.WriteString(e.Err.Error())

	if e.Detail != "" {
		out.WriteString(": ")
		out.WriteString(e.Detail)
	}

	return out.String()
}

// Unwrap returns the sentinel.
func (e *Error) Unwrap() error { return e.Err }
