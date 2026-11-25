package evalcore_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/pkgs/evalcore"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

func BenchmarkIsJSONObject(b *testing.B) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "nikunjy query",
			input: `language eq "ar" and isNewUser eq true and clubsTimeSpent gt 600`,
		},
		{
			name:  "jsonlogic query",
			input: `{"and":[{"==":[{"var":"language"},"ar"]},{">":[{"var":"clubsTimeSpent"},600]}]}`,
		},
		{name: fixtures.NullLiteral, input: fixtures.NullLiteral},
		{
			name:  "whitespace-prefixed object",
			input: "  \n\t{\"==\":[{\"var\":\"language\"},\"ar\"]}",
		},
	}
	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_ = evalcore.IsJSONObject(tt.input)
			}
		})
	}
}
