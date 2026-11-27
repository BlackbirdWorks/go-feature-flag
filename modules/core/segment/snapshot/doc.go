// Package snapshot holds the member set of a segment of the snapshot mode: it pulls the members through a
// [segment.SnapshotSource], keeps them in memory behind an atomic pointer and swaps them when the version changes.
package snapshot
