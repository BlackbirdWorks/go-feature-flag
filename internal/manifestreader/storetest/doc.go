// Package storetest is the conformance suite of [manifestreader.ObjectStore]: the contract of a store as
// executable cases. A store implementation passes when [Run] passes on it.
//
// An implementation gives [Run] a [Factory]. The factory returns the store under test together with a [Backend],
// the handle through which the suite arranges content and faults behind the store (puts a file, deletes it,
// makes the next reads fail, hangs them). A fault that a backend cannot arrange (a permission error on an
// emulator that has no permissions, say) is declared in [Fixture.Unsupported] with its reason: the cases that
// need it are then reported as unsupported for that backend, never skipped silently. [Run] returns a [Report] of
// what ran and what did not, and logs it.
package storetest
