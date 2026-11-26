package controlplane

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	// ErrUnsupportedAPIVersion is matched by an UnsupportedVersionError: a document whose apiVersion this
	// package does not read.
	ErrUnsupportedAPIVersion = errors.New("controlplane: unsupported apiVersion")
	// ErrWrongKind is matched by a KindError: a document of another kind than the one asked for.
	ErrWrongKind = errors.New("controlplane: wrong document kind")
	// ErrInvalid is matched by every ValidationError, UnsupportedVersionError and KindError.
	ErrInvalid = errors.New("controlplane: invalid document")
	// ErrChecksumMismatch is returned by VerifyChecksum when the bytes do not match.
	ErrChecksumMismatch = errors.New("controlplane: checksum mismatch")
	// ErrUnsupportedChecksum is returned for a checksum algorithm other than sha256.
	ErrUnsupportedChecksum = errors.New("controlplane: unsupported checksum algorithm")
	// ErrInvalidChecksum is returned for a checksum value that is not 64 lower-case hex characters.
	ErrInvalidChecksum = errors.New("controlplane: invalid checksum value")
	// ErrInvalidExtension is returned by ObjectKey for an extension that is not [a-z0-9]+.
	ErrInvalidExtension = errors.New("controlplane: invalid object extension")
	// ErrNilDocument is returned when a nil document is marshalled or validated.
	ErrNilDocument = errors.New("controlplane: nil document")
	// ErrUnsupportedFormat is returned for a text format this package does not read (for example toml).
	ErrUnsupportedFormat = errors.New("controlplane: unsupported format")
	// ErrUnsupportedValue is returned by a Marshal function for a value that cannot be written: NaN, infinity,
	// a map with non-string keys, a string that is not valid UTF-8, or a type outside the format.
	ErrUnsupportedValue = errors.New("controlplane: unsupported value")
	// ErrDecode is matched by every DecodeError: a document that is not readable.
	ErrDecode = errors.New("controlplane: decode")
)

// DecodeError says where and why a document cannot be read. It never contains the text of a value (a value
// may be a secret such as an API key); Path holds field names and indexes only.
type DecodeError struct {
	// Path addresses the field, such as "flagSets[0].apiKeys". Empty for the document.
	Path string
	// Reason says what is wrong, for example "expected a list".
	Reason string
	// Line is the line of the problem (1-based), or 0 when unknown.
	Line int
}

// Error implements error.
func (e *DecodeError) Error() string {
	var out strings.Builder

	out.WriteString("controlplane: decode: ")

	if e.Path != "" {
		out.WriteString(e.Path + ": ")
	}

	out.WriteString(e.Reason)

	if e.Line > 0 {
		out.WriteString(" (line " + strconv.Itoa(e.Line) + ")")
	}

	return out.String()
}

// Is reports a match with ErrDecode.
func (*DecodeError) Is(target error) bool {
	return target == ErrDecode
}

// UnsupportedVersionError is returned for a document whose apiVersion is not supported.
type UnsupportedVersionError struct {
	// Got is the apiVersion of the document.
	Got string
	// Supported is the apiVersion this package reads.
	Supported string
}

// Error implements error.
func (e *UnsupportedVersionError) Error() string {
	return fmt.Sprintf("%s: got %q, supported %q", ErrUnsupportedAPIVersion.Error(), shorten(e.Got), e.Supported)
}

// Is reports a match with ErrUnsupportedAPIVersion and ErrInvalid.
func (*UnsupportedVersionError) Is(target error) bool {
	return target == ErrUnsupportedAPIVersion || target == ErrInvalid
}

// KindError is returned for a document of another kind than the one asked for.
type KindError struct {
	// Got is the kind of the document.
	Got string
	// Want is the kind that was asked for.
	Want string
}

// Error implements error.
func (e *KindError) Error() string {
	return fmt.Sprintf("%s: got %q, want %q", ErrWrongKind.Error(), shorten(e.Got), e.Want)
}

// Is reports a match with ErrWrongKind and ErrInvalid.
func (*KindError) Is(target error) bool {
	return target == ErrWrongKind || target == ErrInvalid
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

const maxEchoed = 64

// shorten cuts a value that an error repeats.
func shorten(text string) string {
	if len(text) <= maxEchoed {
		return text
	}

	return strings.ToValidUTF8(text[:maxEchoed], "") + "..."
}
