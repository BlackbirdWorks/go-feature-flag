//go:build !bench

package flagruntime_test

// scaleSets is the number of flag sets of the scale suite. The normal run keeps the package test run short under
// -race; the full size (5000 flag sets x 100 flags) runs with -tags=bench, see docs/scale.md.
const scaleSets = 500
