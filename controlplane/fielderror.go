package controlplane

import (
	"fmt"
	"strings"
)

// Code is a stable, machine-readable reason of a FieldError.
type Code string

// The codes of a FieldError.
const (
	// CodeUnresolvedSource: a flag set source names no flags object of the manifest.
	CodeUnresolvedSource Code = "unresolved-source"
	// CodeDuplicateKey: an API key that an earlier flag set already holds.
	CodeDuplicateKey Code = "duplicate-key"
	// CodeDuplicateName: a name that an earlier entry already holds.
	CodeDuplicateName Code = "duplicate-name"
	// CodeInvalidName: a name that does not match ^[a-z][a-z0-9-]{0,62}$.
	CodeInvalidName Code = "invalid-name"
	// CodeUnsupportedVersion: an object whose content version is not supported.
	CodeUnsupportedVersion Code = "unsupported-version"
	// CodeInvalidChecksum: a checksum with another algorithm than sha256 or a malformed value.
	CodeInvalidChecksum Code = "invalid-checksum"
	// CodeInvalidValue: a value that is not allowed.
	CodeInvalidValue Code = "invalid-value"
	// CodeMissingValue: a required value is absent.
	CodeMissingValue Code = "missing-value"
	// CodeTooManyObjects: the manifest lists more objects than the limit.
	CodeTooManyObjects Code = "too-many-objects"
	// CodeUndeclaredVariation: a rule names a variation that the flag does not declare.
	CodeUndeclaredVariation Code = "undeclared-variation"
)

// EntryKind says which entry of a document a FieldError belongs to.
type EntryKind string

// The entry kinds of a FieldError.
const (
	// EntryDocument: the document as a whole (header, channel, generation, object count).
	EntryDocument EntryKind = "document"
	// EntryObject: an entry of manifest.objects.
	EntryObject EntryKind = "object"
	// EntryFlagSet: an entry of flagSets.
	EntryFlagSet EntryKind = "flagSet"
	// EntryFlag: a flag of a flag file.
	EntryFlag EntryKind = "flag"
)

// NoIndex is the Index of a FieldError whose entry has no position in a list.
const NoIndex = -1

// FieldError is one problem of a document. A caller can keep the entries without a problem and drop the others
// with Kind, Name and Index, without parsing text. The Reason never contains a secret such as an API key.
type FieldError struct {
	// Path addresses the field, such as "objects[1].checksum.value".
	Path string
	// Reason says what is wrong, in words.
	Reason string
	// Code is the stable reason.
	Code Code
	// Kind is the kind of entry that has the problem.
	Kind EntryKind
	// Name is the name of the entry (object name, flag set name, flag name); empty for a document.
	Name string
	// Index is the position of the entry in its list, or NoIndex.
	Index int
}

// Error implements error.
func (e *FieldError) Error() string {
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
	for i := range e.Problems {
		parts[i] = e.Problems[i].Error()
	}

	return "controlplane: invalid document: " + strings.Join(parts, "; ")
}

// Is reports a match with ErrInvalid.
func (*ValidationError) Is(target error) bool {
	return target == ErrInvalid
}

// problems collects FieldErrors.
type problems struct {
	list []FieldError
}

// entry names the entry a problem belongs to.
type entry struct {
	kind  EntryKind
	name  string
	index int
}

func (p *problems) addf(at entry, code Code, path, format string, args ...any) {
	p.list = append(p.list, FieldError{
		Path: path, Reason: fmt.Sprintf(format, args...), Code: code, Kind: at.kind, Name: at.name, Index: at.index,
	})
}

func (p *problems) err() error {
	if len(p.list) == 0 {
		return nil
	}

	return &ValidationError{Problems: p.list}
}
