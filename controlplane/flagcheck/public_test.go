package flagcheck_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane/flagcheck"
)

// flagsOf is a flag file that defines the given flag keys.
func flagsOf(name string, keys ...string) flagcheck.FlagFile {
	var body strings.Builder

	for _, key := range keys {
		body.WriteString(key + ":\n  variations: {\"on\": true, \"off\": false}\n  defaultRule: {variation: \"off\"}\n")
	}

	return flagcheck.FlagFile{Name: name, Format: formatYAML, Body: []byte(body.String())}
}

func TestCheckPublicDuplicates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		wantText     string
		sets         []flagcheck.KeylessSet
		wantProblems []flagcheck.PublicProblem
	}{
		{name: "no keyless flag set"},
		{
			name: "one flag set",
			sets: []flagcheck.KeylessSet{{Name: "a", Flags: []flagcheck.FlagFile{flagsOf("fa", "x", "y")}}},
		},
		{
			name: "disjoint flag keys",
			sets: []flagcheck.KeylessSet{
				{Name: "a", Flags: []flagcheck.FlagFile{flagsOf("fa", "x")}},
				{Name: "b", Flags: []flagcheck.FlagFile{flagsOf("fb", "y")}},
			},
		},
		{
			name: "a key shared by two",
			sets: []flagcheck.KeylessSet{
				{Name: "b", Flags: []flagcheck.FlagFile{flagsOf("fb", "x", "y")}},
				{Name: "a", Flags: []flagcheck.FlagFile{flagsOf("fa", "x")}},
			},
			wantProblems: []flagcheck.PublicProblem{{Flag: "x", Owners: []flagcheck.PublicOwner{
				{FlagSet: "a", Object: "fa"}, {FlagSet: "b", Object: "fb"},
			}}},
			wantText: "flag x: defined in the keyless flag sets a (flag object fa) and b (flag object fb)",
		},
		{
			name: "keys in key order, three flag sets",
			sets: []flagcheck.KeylessSet{
				{Name: "c", Flags: []flagcheck.FlagFile{flagsOf("fc", "z", "x")}},
				{Name: "a", Flags: []flagcheck.FlagFile{flagsOf("fa", "z", "x")}},
				{Name: "b", Flags: []flagcheck.FlagFile{flagsOf("fb", "z")}},
			},
			wantProblems: []flagcheck.PublicProblem{
				{Flag: "x", Owners: []flagcheck.PublicOwner{
					{FlagSet: "a", Object: "fa"}, {FlagSet: "c", Object: "fc"},
				}},
				{Flag: "z", Owners: []flagcheck.PublicOwner{
					{FlagSet: "a", Object: "fa"}, {FlagSet: "b", Object: "fb"}, {FlagSet: "c", Object: "fc"},
				}},
			},
		},
		{
			name: "a key defined twice inside one flag set is not a duplicate",
			sets: []flagcheck.KeylessSet{
				{Name: "a", Flags: []flagcheck.FlagFile{flagsOf("f1", "x"), flagsOf("f2", "x")}},
				{Name: "b", Flags: []flagcheck.FlagFile{flagsOf("fb", "y")}},
			},
		},
		{
			name: "the later source is the object that is named",
			sets: []flagcheck.KeylessSet{
				{Name: "a", Flags: []flagcheck.FlagFile{flagsOf("f1", "x"), flagsOf("f2", "x")}},
				{Name: "b", Flags: []flagcheck.FlagFile{flagsOf("fb", "x")}},
			},
			wantProblems: []flagcheck.PublicProblem{{Flag: "x", Owners: []flagcheck.PublicOwner{
				{FlagSet: "a", Object: "f2"}, {FlagSet: "b", Object: "fb"},
			}}},
		},
		{
			name: "a file that does not decode has no flag",
			sets: []flagcheck.KeylessSet{
				{Name: "a", Flags: []flagcheck.FlagFile{{Name: "bad", Format: formatYAML, Body: []byte(": [")}}},
				{Name: "b", Flags: []flagcheck.FlagFile{flagsOf("fb", "x")}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := flagcheck.CheckPublicDuplicates(tt.sets)
			if tt.wantProblems == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, flagcheck.ErrPublicDuplicate)

			var public *flagcheck.PublicError

			require.ErrorAs(t, err, &public)
			assert.Equal(t, tt.wantProblems, public.Problems)

			if tt.wantText != "" {
				assert.ErrorContains(t, err, tt.wantText)
			}
		})
	}
}
