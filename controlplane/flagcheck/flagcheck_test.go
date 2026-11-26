package flagcheck_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane/flagcheck"
)

const (
	formatYAML = "yaml"
	flagA      = "flag-a"
)

const base = `
flag-a:
  variations: {on: true, off: false}
  defaultRule: {variation: off}
`

func TestCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		format     string
		wantFlags  []string
		wantDecode bool
	}{
		{name: "valid flag file", body: base, format: formatYAML},
		{
			name:   "valid targeting query",
			body:   base + "  targeting:\n    - query: 'beta eq true'\n      variation: on\n",
			format: formatYAML,
		},
		{
			name:      "query that does not compile",
			body:      base + "  targeting:\n    - query: 'beta eq ('\n      variation: on\n",
			format:    formatYAML,
			wantFlags: []string{flagA},
		},
		{
			name:   "percentages that do not sum to 100 are accepted: the engine only needs a non-zero sum",
			body:   base + "  targeting:\n    - query: 'beta eq true'\n      percentage: {on: 30, off: 30}\n",
			format: formatYAML,
		},
		{
			name:      "percentages that sum to zero",
			body:      base + "  targeting:\n    - query: 'beta eq true'\n      percentage: {on: 0, off: 0}\n",
			format:    formatYAML,
			wantFlags: []string{flagA},
		},
		{
			name:      "percentage of an unknown variation",
			body:      base + "  targeting:\n    - query: 'beta eq true'\n      percentage: {on: 50, nope: 50}\n",
			format:    formatYAML,
			wantFlags: []string{flagA},
		},
		{
			name:      "rule without a variation or a percentage",
			body:      base + "  targeting:\n    - query: 'beta eq true'\n",
			format:    formatYAML,
			wantFlags: []string{flagA},
		},
		{
			name:      "unknown variation in a rule",
			body:      base + "  targeting:\n    - query: 'beta eq true'\n      variation: maybe\n",
			format:    formatYAML,
			wantFlags: []string{flagA},
		},
		{
			name:      "unknown variation in the default rule",
			body:      "flag-b:\n  variations: {on: true, off: false}\n  defaultRule: {variation: nope}\n",
			format:    formatYAML,
			wantFlags: []string{"flag-b"},
		},
		{
			name: "two bad flags are both reported, sorted",
			body: "z:\n  variations: {a: 1}\n  defaultRule: {variation: q}\n" +
				"b:\n  variations: {a: 1}\n  defaultRule: {variation: q}\n",
			format:    formatYAML,
			wantFlags: []string{"b", "z"},
		},
		{
			name:   "json is read",
			body:   `{"f":{"variations":{"a":1,"b":2},"defaultRule":{"variation":"a"}}}`,
			format: "json",
		},
		{name: "not decodable", body: "f: [", format: formatYAML, wantDecode: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := flagcheck.Check([]byte(tt.body), tt.format)

			switch {
			case tt.wantDecode:
				require.ErrorIs(t, err, flagcheck.ErrInvalid)

				var typed *flagcheck.Error

				require.NotErrorAs(t, err, &typed)
			case len(tt.wantFlags) == 0:
				require.NoError(t, err)
			default:
				require.ErrorIs(t, err, flagcheck.ErrInvalid)

				var typed *flagcheck.Error

				require.ErrorAs(t, err, &typed)

				names := make([]string, 0, len(typed.Problems))
				for _, p := range typed.Problems {
					names = append(names, p.Flag)

					assert.Contains(t, err.Error(), "flag "+p.Flag+": ")
				}

				assert.Equal(t, tt.wantFlags, names)
			}
		})
	}
}

// The message of an error carries where and why the file is wrong, not a value of the file: neither a scalar the
// decoder could not read nor the text of a targeting query.
func TestErrorMessagesDoNotQuoteTheFile(t *testing.T) {
	t.Parallel()

	const canary = "canary-api-key-0009"

	tests := []struct {
		name      string
		body      string
		format    string
		wantInMsg string
	}{
		{
			name:      "scalar where a mapping is expected",
			body:      "f: " + canary + "\n",
			format:    formatYAML,
			wantInMsg: "cannot unmarshal",
		},
		{
			name:      "unterminated yaml",
			body:      canary + ": [unterminated\nflag: {a",
			format:    formatYAML,
			wantInMsg: "line 1",
		},
		{
			name:      "query that does not compile",
			body:      base + "  targeting:\n    - query: '" + canary + " eq ('\n      variation: on\n",
			format:    formatYAML,
			wantInMsg: "invalid query",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := flagcheck.Check([]byte(tt.body), tt.format)

			require.ErrorIs(t, err, flagcheck.ErrInvalid)
			assert.NotContains(t, err.Error(), canary)
			assert.NotContains(t, err.Error(), "canary-")
			assert.Contains(t, err.Error(), tt.wantInMsg)
		})
	}
}
