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
