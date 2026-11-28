package resolver_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

const spaced = " A "

func TestNormalizeID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		norm   resolver.Normalization
		id     string
		want   string
		wantOK bool
	}{
		{name: "none keeps the id", norm: resolver.NormalizeNone, id: spaced, want: spaced, wantOK: true},
		{name: "empty means none", id: spaced, want: spaced, wantOK: true},
		{name: "none keeps invalid UTF-8", norm: resolver.NormalizeNone, id: "\xff", want: "\xff", wantOK: true},
		{name: "trimLower", norm: resolver.NormalizeTrimLower, id: "  Ünï@X.COM\t", want: "ünï@x.com", wantOK: true},
		{name: "trimLower refuses invalid UTF-8", norm: resolver.NormalizeTrimLower, id: "a\xff"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := resolver.NormalizeID(tt.norm, tt.id)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantOK, ok)
		})
	}
}
