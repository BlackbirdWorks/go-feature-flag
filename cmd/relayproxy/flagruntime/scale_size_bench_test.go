//go:build bench

package flagruntime_test

// scaleSets is the number of flag sets of the scale suite: the full size of the owner's target, selected by
// -tags=bench (the normal run uses a smaller fleet, see scale_size_test.go).
const scaleSets = 5000
