package evalcore_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/pkgs/evalcore"
)

func BenchmarkStrTrim(b *testing.B) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "single line", input: `language eq "ar" and isNewUser eq true`},
		{
			name:  "multiline",
			input: "language eq \"ar\"\n   and isNewUser eq true\n   and clubsTimeSpent gt 600",
		},
	}
	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_ = evalcore.StrTrim(tt.input)
			}
		})
	}
}
