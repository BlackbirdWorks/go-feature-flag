// Package segmenttest is the conformance suite of [segment.Provider]: the contract of the seam as executable
// cases. A provider conforms when [Run] passes on it.
//
// An implementation gives [Run] a [Factory]. The factory returns a provider that holds the members of the
// [Data] the suite asks for, together with a [Backend], the handle through which the suite makes the store
// fail or hang. A fault that a backend cannot arrange (an in-memory provider has no store to fail) is declared in
// [Fixture.Unsupported] with its reason: the cases that need it are then reported as unsupported, never skipped
// silently. A provider without a backend is wrapped in a decorator of the suite that injects the faults: the cases that
// need one then check what the provider answers when healthy and the expectations of the suite, not the error paths
// of the provider itself (a documented limit). [Check] returns the [Report] of what ran, what failed and what is unsupported; [Run] logs it and
// fails the test for each failed case.
package segmenttest
