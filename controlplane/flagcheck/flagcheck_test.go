package flagcheck_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/controlplane/flagcheck"
)

const (
	phraseQuery     = "invalid query"
	phraseVariation = "invalid variation"
	formatYAML      = "yaml"
	formatJSON      = "json"
	flagA           = "flag-a"
)

var (
	errVariationX = errors.New("invalid variation: x")
	errDuplicateY = errors.New("duplicated rule name: y")
	errLongerWord = errors.New("invalid variations of Zq9Xw8")
	errSumLarge   = errors.New("invalid percentages: the sum (130) is too large")
	errSentinel   = errors.New("invalid variation")
	errQuoted     = errors.New("quoted Sx9Qv3")
	errUnknown    = errors.New("brand new rule about Zq8Wm4Xk")
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
			name: "yaml duplicate key", format: formatYAML,
			body:      "f:\n  Dup7Qx9Zk: 1\n  Dup7Qx9Zk: 2\n",
			secrets:   []string{"Dup7Qx9Zk"},
			wantInMsg: "invalid YAML value at line",
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
			wantInMsg: phraseQuery,
		},
		{
			name: "yaml query with quotes that does not compile", format: formatYAML,
			body:      base + "  targeting:\n    - query: \"Ab3'Cd\\\"Ef9Gh`Ij5 eq (\"\n      variation: on\n",
			secrets:   []string{"Ab3'Cd\"Ef9Gh`Ij5", "Ef9Gh", "Ij5 eq"},
			wantInMsg: phraseQuery,
		},
		{
			name:      "yaml metadata value next to a flag that is invalid",
			format:    formatYAML,
			body:      "f:\n  variations: {on: true}\n  defaultRule: {variation: Nv6PzL1xT}\n  metadata: {owner: Mt5RxLp9Q}\n",
			secrets:   []string{"Nv6PzL1xT", "Mt5RxLp9Q"},
			wantInMsg: phraseVariation,
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
			wantInMsg: phraseVariation,
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

// A flag key is a name and is kept, made single-line and cut at 80 characters with an ellipsis, and nothing of the key
// follows the ellipsis; the engine's own error stays in Problems.
func TestFlagKeysAreKeptAndBounded(t *testing.T) {
	t.Parallel()

	const wantCategory = "invalid query: parsing the query"

	tests := []struct {
		name    string
		key     string
		wantKey string
		wantLen int
	}{
		{name: "plain key", key: "my-flag", wantKey: "my-flag", wantLen: 7},
		{name: "key with a newline", key: "a\nb", wantKey: "a b", wantLen: 3},
		{name: "key of exactly the limit", key: strings.Repeat("k", 80), wantKey: strings.Repeat("k", 80), wantLen: 80},
		{name: "long key", key: strings.Repeat("k", 300), wantKey: strings.Repeat("k", 79) + "…", wantLen: 80},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			engine := fmt.Errorf("invalid query: %w", fmt.Errorf("parsing the query: %w", errQuoted))
			err := &flagcheck.Error{Problems: []flagcheck.FlagProblem{{Flag: tt.key, Err: engine}}}
			msg := err.Error()

			require.True(t, strings.HasPrefix(msg, "flag "))
			require.True(t, strings.HasSuffix(msg, ": "+wantCategory), msg)

			key := strings.TrimSuffix(strings.TrimPrefix(msg, "flag "), ": "+wantCategory)

			assert.Equal(t, tt.wantKey, key, "nothing of the key follows the ellipsis")
			assert.Equal(t, tt.wantLen, utf8.RuneCountInString(key))
			assertNoFragment(t, msg, "quoted Sx9Qv3")
			assert.Contains(t, err.Problems[0].Err.Error(), "Sx9Qv3", "Problems[i].Err is the engine's error as it is")
		})
	}
}

// The category comes from the layers of the error chain, a fixed phrase only as the start of a layer's own message:
// a phrase inside a query or a value never adds a reason.
func TestCategoryIsTheChainNotTheText(t *testing.T) {
	t.Parallel()

	const queryCategory = "flag f: validating the flag: invalid query: parsing the query"

	phrases := []string{
		"invalid variation", "invalid percentages: the sum", "missing default rule", "invalid query",
		"all variations should have the same type", "each targeting should have a query", "nil value for variation",
		"invalid jsonlogic query",
	}

	tests := make([]struct {
		name string
		body string
		want string
	}, 0, len(phrases)+1)

	for _, phrase := range phrases {
		tests = append(tests, struct {
			name string
			body string
			want string
		}{
			name: "a malformed query that holds " + phrase,
			body: base + "  targeting:\n    - query: \"" + phrase + " eq (\"\n      variation: on\n",
			want: queryCategory,
		})
	}

	tests = append(tests, struct {
		name string
		body string
		want string
	}{
		name: "a variation error whose value holds invalid query",
		body: "f:\n  variations: {on: true, off: false}\n  defaultRule: {variation: \"invalid query\"}\n",
		want: "flag f: validating the flag: invalid variation",
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := flagcheck.Check([]byte(tt.body), formatYAML)

			require.Error(t, err)
			assert.Equal(
				t,
				strings.ReplaceAll(tt.want, "flag f:", "flag flag-a:"),
				strings.ReplaceAll(err.Error(), "flag f:", "flag flag-a:"),
			)
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

// The category of an engine error is taken from the layers of the chain, whatever its shape.
func TestCategoryShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		name string
		want string
	}{
		{name: "no error", err: nil, want: "flag f: rejected by the engine"},
		{
			name: "several errors joined",
			err:  errors.Join(errVariationX, errDuplicateY),
			want: "flag f: invalid variation: duplicated rule name",
		},
		{
			name: "a phrase is not a prefix of a longer word", err: errLongerWord,
			want: "flag f: rejected by the engine",
		},
		{
			name: "the longest phrase of a layer wins",
			err:  errSumLarge,
			want: "flag f: invalid percentages: the sum",
		},
		{
			name: "a wrapped sentinel with the same prefix is named once",
			err:  fmt.Errorf("invalid variation: %w", errSentinel),
			want: "flag f: invalid variation",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := &flagcheck.Error{Problems: []flagcheck.FlagProblem{{Flag: "f", Err: tt.err}}}

			assert.Equal(t, tt.want, err.Error())
		})
	}
}
