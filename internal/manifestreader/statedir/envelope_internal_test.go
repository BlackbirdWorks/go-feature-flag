package statedir

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIsOurTmp_AcceptsWhatNewTmpNameMakes: the matcher and the generator are one definition: every name the generator
// makes is recognised, 100 times over.
func TestIsOurTmp_AcceptsWhatNewTmpNameMakes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		draws     int
		wantMatch bool
		wantLen   int
	}{
		{name: "a hundred generated names", draws: 100, wantMatch: true, wantLen: len(tmpPrefix) + 2*tmpRandomBytes},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for range tt.draws {
				name, err := newTmpName()
				require.NoError(t, err)
				assert.Equal(t, tt.wantMatch, isOurTmp(name), name)
				assert.Len(t, name, tt.wantLen)
			}
		})
	}
}

func TestIsOurTmp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
		want bool
	}{
		{name: "16 lower case hex digits", file: ".tmp-0123456789abcdef", want: true},
		{name: "15 digits", file: ".tmp-0123456789abcde"},
		{name: "17 digits", file: ".tmp-0123456789abcdef0"},
		{name: "upper case", file: ".tmp-0123456789ABCDEF"},
		{name: "not hex", file: ".tmp-0123456789abcdeg"},
		{name: "a short foreign name", file: ".tmp-abc"},
		{name: "a legacy decimal name, never in a release", file: ".tmp-1"},
		{name: "no suffix", file: ".tmp-"},
		{name: "another prefix", file: "tmp-0123456789abcdef"},
		{name: "a user file", file: ".tmp-user"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, isOurTmp(tt.file))
		})
	}
}
