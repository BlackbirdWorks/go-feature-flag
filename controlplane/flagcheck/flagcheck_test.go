package flagcheck_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/controlplane/flagcheck"
)

const (
	formatYAML = "yaml"
	formatJSON = "json"
	flagA      = "flag-a"
)

var (
	errQuoted  = errors.New("invalid query: parsing the query: quoted Sx9Qv3")
	errUnknown = errors.New("brand new rule about Zq8Wm4Xk")
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

// assertNoFragment fails when text contains the secret or any fragment of it of fragmentLen characters or more.
func assertNoFragment(t *testing.T, text, secret string) {
	t.Helper()

	const fragmentLen = 6

	runes := []rune(secret)
	for i := 0; i+fragmentLen <= len(runes); i++ {
		fragment := string(runes[i : i+fragmentLen])
		if strings.TrimSpace(fragment) == "" {
			continue
		}

		assert.NotContains(t, text, fragment, "the message repeats a fragment of %q", secret)
	}
}

// The message of an error carries where and what kind of failure it is, never a value of the file: not a scalar the
// decoder could not read, not a variation value, not a metadata value, not the text of a targeting query, whatever
// quotes, backticks or newlines they hold. Flag keys are names and are kept.
func TestErrorMessagesDoNotQuoteTheFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      string
		format    string
		wantInMsg string
		secrets   []string
	}{
		{
			name: "yaml scalar where a mapping is expected, with an embedded backtick", format: formatYAML,
			body:      "f: \"a`SecretValue7Zq\"\n",
			secrets:   []string{"a`SecretValue7Zq", "SecretValue7Zq"},
			wantInMsg: "cannot unmarshal a !!str into dto.DTO",
		},
		{
			name: "yaml scalar with quotes and a newline", format: formatYAML,
			body:      "f: |\n  Q1w\"E2r'T3y`U4i\n  second Line9Pk\n",
			secrets:   []string{"Q1w\"E2r'T3y`U4i", "second Line9Pk"},
			wantInMsg: "cannot unmarshal a !!str into dto.DTO",
		},
		{
			name: "yaml scalar that holds the word into", format: formatYAML,
			body:      "f: \"x into dto.Evil Pv8Rk2\"\n",
			secrets:   []string{"Pv8Rk2", "dto.Evil"},
			wantInMsg: "into dto.DTO",
		},
		{
			name: "yaml syntax error with a value", format: formatYAML,
			body:      "Sy5Tq8Lm9: [unterminated\nkeyX: {a",
			secrets:   []string{"unterminated"},
			wantInMsg: "invalid YAML at line",
		},
		{
			name: "yaml variation values of different types", format: formatYAML,
			body:      "f:\n  variations: {on: true, off: Vr4NmK8dQ}\n  defaultRule: {variation: off}\n",
			secrets:   []string{"Vr4NmK8dQ"},
			wantInMsg: "all variations should have the same type",
		},
		{
			name: "yaml query that does not compile", format: formatYAML,
			body:      base + "  targeting:\n    - query: 'Qy7WvMk3 eq ('\n      variation: on\n",
			secrets:   []string{"Qy7WvMk3"},
			wantInMsg: "invalid query",
		},
		{
			name: "yaml query with quotes that does not compile", format: formatYAML,
			body:      base + "  targeting:\n    - query: \"Ab3'Cd\\\"Ef9Gh`Ij5 eq (\"\n      variation: on\n",
			secrets:   []string{"Ab3'Cd\"Ef9Gh`Ij5", "Ef9Gh", "Ij5 eq"},
			wantInMsg: "invalid query",
		},
		{
			name:      "yaml metadata value next to a flag that is invalid",
			format:    formatYAML,
			body:      "f:\n  variations: {on: true}\n  defaultRule: {variation: Nv6PzL1xT}\n  metadata: {owner: Mt5RxLp9Q}\n",
			secrets:   []string{"Nv6PzL1xT", "Mt5RxLp9Q"},
			wantInMsg: "invalid variation",
		},
		{
			name: "json string where an object is expected", format: formatJSON,
			body:      `{"f": "Js2Wq9ZxK"}`,
			secrets:   []string{"Js2Wq9ZxK"},
			wantInMsg: "a string where",
		},
		{
			name: "json syntax error", format: formatJSON,
			body:      `{"f": Jq7Pm3WzR}`,
			secrets:   []string{"Jq7Pm3WzR"},
			wantInMsg: "invalid JSON at byte",
		},
		{
			name: "json flag with an invalid default variation", format: formatJSON,
			body:      `{"f": {"variations": {"a": "Jv4Nm8KdS"}, "defaultRule": {"variation": "Zz9Yy8Xw7"}}}`,
			secrets:   []string{"Jv4Nm8KdS", "Zz9Yy8Xw7"},
			wantInMsg: "invalid variation",
		},
		{
			name: "toml string where a table is expected", format: "toml",
			body:      "f = \"Tm6Hv2QxP\"\n",
			secrets:   []string{"Tm6Hv2QxP"},
			wantInMsg: "TOML",
		},
		{
			name: "toml syntax error", format: "toml",
			body:      "Tk8Rn5Bcd = = Tx3Lq9\n",
			secrets:   []string{"Tx3Lq9", "Tk8Rn5Bcd"},
			wantInMsg: "invalid TOML at line",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := flagcheck.Check([]byte(tt.body), tt.format)

			require.ErrorIs(t, err, flagcheck.ErrInvalid)
			assert.Contains(t, err.Error(), tt.wantInMsg)

			for _, secret := range tt.secrets {
				assertNoFragment(t, err.Error(), secret)
			}
		})
	}
}

// A flag key is a name and is kept, made single-line and cut; the engine's own error stays in Problems.
func TestFlagKeysAreKeptAndBounded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		key       string
		wantInMsg string
		wantLen   int
	}{
		{name: "plain key", key: "my-flag", wantInMsg: "flag my-flag: ", wantLen: -1},
		{name: "key with a newline", key: "a\nb", wantInMsg: "flag a b: ", wantLen: -1},
		{name: "long key", key: strings.Repeat("k", 300), wantInMsg: strings.Repeat("k", 79) + "…", wantLen: 80},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := &flagcheck.Error{
				Problems: []flagcheck.FlagProblem{
					{Flag: tt.key, Err: errQuoted},
				},
			}

			assert.Contains(t, err.Error(), tt.wantInMsg)
			assert.Contains(t, err.Error(), "invalid query: parsing the query")
			assertNoFragment(t, err.Error(), "quoted Sx9Qv3")
			assert.Contains(t, err.Problems[0].Err.Error(), "Sx9Qv3", "Problems[i].Err is the engine's error as it is")
		})
	}
}

func TestUnknownEngineMessageIsOnlyNamedAsRejected(t *testing.T) {
	t.Parallel()

	err := &flagcheck.Error{
		Problems: []flagcheck.FlagProblem{{Flag: "f", Err: errUnknown}},
	}

	assert.Equal(t, "flag f: rejected by the engine", err.Error())
}

// The original errors stay reachable for the one who needs them (and knows they may quote the file).
func TestOriginalErrorsStayReachable(t *testing.T) {
	t.Parallel()

	t.Run("yaml", func(t *testing.T) {
		t.Parallel()

		err := flagcheck.Check([]byte("f: Wq3Zr8Lm\n"), formatYAML)

		var typed *yaml.TypeError

		require.ErrorAs(t, err, &typed)
		assert.Contains(t, typed.Errors[0], "Wq3Zr8L", "the decoder's own text has the start of the value")
	})

	t.Run("json", func(t *testing.T) {
		t.Parallel()

		var syntax *json.SyntaxError

		require.ErrorAs(t, flagcheck.Check([]byte(`{"f": Jq7`), formatJSON), &syntax)
	})

	t.Run("engine", func(t *testing.T) {
		t.Parallel()

		err := flagcheck.Check(
			[]byte(base+"  targeting:\n    - query: 'Qy7WvMk3 eq ('\n      variation: on\n"),
			formatYAML,
		)

		var checkErr *flagcheck.Error

		require.ErrorAs(t, err, &checkErr)
		assert.Contains(
			t,
			checkErr.Problems[0].Err.Error(),
			"Qy7WvMk3",
			"FlagProblem.Err is the engine's error as it is",
		)
	})
}
