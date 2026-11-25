package helper_test

import (
	"testing"

	"github.com/pterm/pterm"
)

// TestMain disables the styling of pterm once for the whole package: pterm keeps it in package-level
// variables, so the tests must not toggle it.
func TestMain(m *testing.M) {
	pterm.DisableStyling()
	pterm.DisableColor()
	m.Run()
}
