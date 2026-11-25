package flag

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
)

func TestService_CompiledQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		raw         string
		wantTrimmed string
		wantFormat  QueryFormat
		wantDecoded bool
	}{
		{name: "nikunjy", raw: `key eq "abc"`, wantTrimmed: `key eq "abc"`, wantFormat: NikunjyQueryFormat},
		{
			name: "jsonlogic", raw: `{"==": [{"var": "key"}, "abc"]}`,
			wantTrimmed: `{"==": [{"var": "key"}, "abc"]}`, wantFormat: JSONLogicQueryFormat, wantDecoded: true,
		},
		{
			name: "multi-line jsonlogic", raw: "{\n  \"==\": [{\"var\": \"key\"}, \"abc\"]\n}",
			wantTrimmed: `{"==": [{"var": "key"}, "abc"]}`, wantFormat: JSONLogicQueryFormat, wantDecoded: true,
		},
		{
			name: "invalid json", raw: `{"==": [{"var": "key"}, "abc"]`,
			wantTrimmed: `{"==": [{"var": "key"}, "abc"]`, wantFormat: NikunjyQueryFormat,
		},
		{name: "empty", raw: "", wantTrimmed: "", wantFormat: NikunjyQueryFormat},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := NewService()
			c := svc.compiledQuery(tt.raw)
			assert.Equal(t, tt.wantTrimmed, c.trimmed)
			assert.Equal(t, tt.wantFormat, c.format)
			assert.Equal(t, tt.wantDecoded, c.jsonLogic != nil)
			assert.Same(t, c, svc.compiledQuery(tt.raw), "the query must be compiled once")
		})
	}
}

func TestService_CompiledQueryIsOwnedByTheService(t *testing.T) {
	t.Parallel()

	const raw = `{"==": [{"var": "key"}, "abc"]}`

	first, second := NewService(), NewService()

	assert.NotSame(
		t,
		first.compiledQuery(raw),
		second.compiledQuery(raw),
		"two Services must not share compiled queries",
	)
}

func TestService_CompiledQueryOnlyEvaluationStores(t *testing.T) {
	t.Parallel()

	query := "{\n  \"==\": [{\"var\": \"key\"}, \"abc\"]\n}"
	trimmed := `{"==": [{"var": "key"}, "abc"]}`

	tests := []struct {
		name           string
		wantTrimmed    string
		wantStoredSize int
		evaluate       bool
	}{
		{name: "validation does not store the query", evaluate: false, wantTrimmed: trimmed, wantStoredSize: 0},
		{name: "evaluation stores a multi-line query once", evaluate: true, wantTrimmed: trimmed, wantStoredSize: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := NewService()
			r := Rule{Query: &query, VariationResult: new("A")}
			require.NoError(t, r.isQueryValid(svc, false))
			assert.Equal(t, JSONLogicQueryFormat, r.GetQueryFormat())
			assert.Equal(t, tt.wantTrimmed, r.GetTrimmedQuery())

			if tt.evaluate {
				_, err := r.Evaluate(svc, "abc", ffcontext.NewEvaluationContext("abc"), "flag", false)
				require.NoError(t, err)
			}

			count := 0

			svc.compiled.Range(func(_, _ any) bool {
				count++

				return true
			})
			assert.Equal(t, tt.wantStoredSize, count)
		})
	}
}
