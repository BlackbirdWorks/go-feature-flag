// Package controlplane is the document format of the Semaphore control plane: the manifest, the flag-set
// documents and the flag objects that a publisher writes to a store (for example S3) and that the relay proxy
// reads.
//
// A publisher fills the exported structs, calls a Marshal function and writes the bytes. The relay decodes the
// same bytes with an Unmarshal function and runs the same Validate functions, so "valid for the publisher" and
// "accepted by the relay" are one definition.
//
// # Format
//
// The manifest and the flag-set documents are YAML (JSON is accepted and can be written with the JSON variants)
// and start with a header: apiVersion "semaphore/v1" and kind "Manifest" or "FlagSet". Flag objects are
// headerless: a flag file is a top-level map keyed by flag names, which is the flag file format that the engine
// loads today (flag format v1). Their version and text format are listed in the manifest entry.
//
// Output is deterministic: fields keep the order of the structs, map keys are sorted, no anchors or aliases are
// written and every string that a YAML 1.1 or 1.2 reader could read as another type (yes, no, on, off, y, n,
// 007, 1e3, 2026-01-01, null, ...) is double quoted.
//
// # Reading and validating
//
// The Unmarshal functions read YAML and JSON through one reader. Field names are case sensitive; duplicate keys,
// non-string keys, NaN, infinity, a second YAML document and an integer field with a fraction or an overflow are a
// *DecodeError, which never contains the text of a value. A float keeps its type (written 2.0) and an integer
// stays an integer, in both formats. Anchors, aliases and merge keys are accepted on read and never written.
//
// The Validate functions return a *ValidationError whose FieldErrors carry a stable Code, the Kind of the entry
// (object, flag set, flag, document), its Name and Index, so a reader can keep the good entries and drop the bad
// ones without parsing text. Of two objects or flag sets with one name, and of an API key held by two flag sets,
// the first in document order keeps it and the later one has the problem. An object with a newer content version
// is an entry problem (CodeUnsupportedVersion), not a failure of the manifest. A wrong kind or apiVersion rejects
// the document whole (*KindError, *UnsupportedVersionError; they match ErrInvalid too).
//
// # Compatibility
//
// Within a version only additive optional fields are allowed. Readers ignore unknown fields of published
// documents (the Unmarshal functions report them in DecodeInfo) so an older relay reads the documents of a newer
// publisher. A document with an unsupported apiVersion or the wrong kind is rejected whole. A breaking change is
// a new apiVersion with new types side by side.
//
// The Go API of this package follows semantic versioning from its first release. It is the only public,
// stability-guaranteed Go package of the module; the production files import only the standard library and
// gopkg.in/yaml.v3.
package controlplane
