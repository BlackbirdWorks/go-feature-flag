package flagdef_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/pkgs/evalcore"
)

func TestOnErrorRoundTripsInEveryFileFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		format  string
		content string
	}{
		{
			name:   "yaml",
			format: "yaml",
			content: "f:\n  variations: {a: 1, b: 2}\n  targeting:\n    - query: key in segment(\"x\")\n      onError: match\n" +
				"      variation: b\n  defaultRule:\n    variation: a\n",
		},
		{
			name: "json", format: "json",
			content: `{"f": {"variations": {"a": 1, "b": 2}, "targeting": [{"query": "key in segment(\"x\")", ` +
				`"onError": "match", "variation": "b"}], "defaultRule": {"variation": "a"}}}`,
		},
		{
			name:   "toml",
			format: "toml",
			content: "[f.variations]\na = 1\nb = 2\n[[f.targeting]]\nquery = 'key in segment(\"x\")'\nonError = \"match\"\n" +
				"variation = \"b\"\n[f.defaultRule]\nvariation = \"a\"\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flags, err := cache.ConvertToFlagStruct([]byte(tt.content), tt.format)
			require.NoError(t, err)

			dto := flags["f"]
			flag := dto.Convert()
			require.NotNil(t, flag.Rules)
			require.NotNil(t, (*flag.Rules)[0].OnError)
			assert.Equal(t, "match", *(*flag.Rules)[0].OnError)
			require.NoError(t, flag.IsValid(flagdef.NewService()))

			// the flag survives its own JSON form (the form of the copies and the hashes)
			raw, err := json.Marshal(flag)
			require.NoError(t, err)
			assert.Contains(t, string(raw), `"onError":"match"`)
		})
	}
}

func TestOnErrorOfAFlagChangesItsHash(t *testing.T) {
	t.Parallel()

	hash := func(onError *string) uint32 {
		rule := queryRule("r", `key in segment("x")`)
		rule.OnError = onError
		raw, err := json.Marshal(onOffFlag(rule))
		require.NoError(t, err)

		return evalcore.Hash(string(raw))
	}

	none, skip, match := hash(nil), hash(new("skip")), hash(new("match"))
	assert.NotEqual(t, none, match)
	assert.NotEqual(t, skip, match)
	assert.NotEqual(t, none, skip)
	assert.Equal(t, match, hash(new("match")), "the hash is stable")
}

func TestInvalidOnErrorIsRejectedWithTheRule(t *testing.T) {
	t.Parallel()

	rule := queryRule("deny", `key in segment("x")`)
	rule.OnError = new("ignore")

	err := onOffFlag(queryRule("first", `age gt 1`), rule).IsValid(flagdef.NewService())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `targeting rule "deny" (index 1)`)
	assert.Contains(t, err.Error(), "invalid onError")
}
