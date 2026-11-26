//go:build race

package flagdef_test

// raceEnabled is true when the tests run with the race detector, which randomly drops the items of a sync.Pool and
// so changes the allocations of an evaluation.
const raceEnabled = true
