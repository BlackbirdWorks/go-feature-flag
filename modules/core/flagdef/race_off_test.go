//go:build !race

package flagdef_test

// raceEnabled is false when the tests run without the race detector, see race_on_test.go.
const raceEnabled = false
