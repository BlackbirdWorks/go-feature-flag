//go:build !bench

package controlsource_test

// scaleSets is the number of flag sets of the scale suite; the normal run keeps the package test run short under
// -race, -tags=bench runs the full size (see docs/scale.md).
const scaleSets = 500
