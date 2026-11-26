package controlplane

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrUnsupportedAPIVersion is returned for a document whose apiVersion this package does not read.
	ErrUnsupportedAPIVersion = errors.New("controlplane: unsupported apiVersion")
	// ErrWrongKind is returned for a document of another kind than the one asked for.
	ErrWrongKind = errors.New("controlplane: wrong document kind")
	// ErrInvalid is matched (errors.Is) by every ValidationError.
	ErrInvalid = errors.New("controlplane: invalid document")
	// ErrChecksumMismatch is returned by VerifyChecksum when the bytes do not match.
	ErrChecksumMismatch = errors.New("controlplane: checksum mismatch")
	// ErrUnsupportedChecksum is returned for a checksum algorithm other than sha256.
	ErrUnsupportedChecksum = errors.New("controlplane: unsupported checksum algorithm")
	// ErrNilDocument is returned when a nil document is marshalled.
	ErrNilDocument = errors.New("controlplane: nil document")
	// ErrUnsupportedFormat is returned for a text format this package does not read (for example toml).
	ErrUnsupportedFormat = errors.New("controlplane: unsupported format")
	// ErrDecode wraps a syntax or type error of a document.
	ErrDecode = errors.New("controlplane: decode")
)

// FieldError is one problem of a document, addressed by a path such as "objects[1].checksum.value".
// The reason never contains a secret such as an API key.
type FieldError struct {
	// Path addresses the field.
	Path string
	// Reason says what is wrong.
	Reason string
}

// Error implements error.
func (e FieldError) Error() string {
	return e.Path + ": " + e.Reason
}

// ValidationError lists every problem found by a Validate function.
type ValidationError struct {
	// Problems are the problems in document order.
	Problems []FieldError
}

// Error implements error.
func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		parts[i] = p.Error()
	}

	return "controlplane: invalid document: " + strings.Join(parts, "; ")
}

// Is reports a match with ErrInvalid.
func (*ValidationError) Is(target error) bool {
	return target == ErrInvalid
}

// ChecksumMismatchError carries the two digests of a failed VerifyChecksum.
type ChecksumMismatchError struct {
	// Want is the listed value.
	Want string
	// Got is the computed value.
	Got string
}

// Error implements error.
func (e *ChecksumMismatchError) Error() string {
	return fmt.Sprintf("controlplane: checksum mismatch: want %s, got %s", e.Want, e.Got)
}

// Is reports a match with ErrChecksumMismatch.
func (*ChecksumMismatchError) Is(target error) bool {
	return target == ErrChecksumMismatch
}

// problems collects FieldErrors.
type problems struct {
	list []FieldError
}

func (p *problems) addf(path, format string, args ...any) {
	p.list = append(p.list, FieldError{Path: path, Reason: fmt.Sprintf(format, args...)})
}

func (p *problems) err() error {
	if len(p.list) == 0 {
		return nil
	}

	return &ValidationError{Problems: p.list}
}
