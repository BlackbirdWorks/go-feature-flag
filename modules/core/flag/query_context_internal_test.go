package flag

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
)

func TestQueryContext_MapOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ctx  ffcontext.Context
		want map[string]any
		name string
	}{
		{
			name: "context with custom attributes",
			ctx:  ffcontext.NewEvaluationContextBuilder("user-1").AddCustom("plan", "pro").Build(),
			want: map[string]any{"plan": "pro", "anonymous": false, "key": "user-1", "targetingKey": "user-1"},
		},
		{
			name: "context without custom attributes",
			ctx:  ffcontext.NewEvaluationContext("user-1"),
			want: map[string]any{"anonymous": false, "key": "user-1", "targetingKey": "user-1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			queryCtx := &queryContext{svc: NewService(), ctx: tt.ctx}

			first := queryCtx.mapOnce()
			second := queryCtx.mapOnce()

			assert.Equal(t, tt.want, first)
			assert.Equal(
				t,
				reflect.ValueOf(first).Pointer(),
				reflect.ValueOf(second).Pointer(),
				"the map must be built once",
			)
		})
	}
}

func TestQueryContext_JSONOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  ffcontext.Context
		want string
	}{
		{
			name: "context with custom attributes",
			ctx:  ffcontext.NewEvaluationContextBuilder("user-1").AddCustom("plan", "pro").Build(),
			want: `{"anonymous": false, "key": "user-1", "plan": "pro", "targetingKey": "user-1"}`,
		},
		{
			name: "context without custom attributes",
			ctx:  ffcontext.NewEvaluationContext("user-1"),
			want: `{"anonymous": false, "key": "user-1", "targetingKey": "user-1"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			queryCtx := &queryContext{svc: NewService(), ctx: tt.ctx}

			first, err := queryCtx.jsonOnce()
			require.NoError(t, err)
			second, err := queryCtx.jsonOnce()
			require.NoError(t, err)

			assert.JSONEq(t, tt.want, string(first))
			assert.Same(t, &first[0], &second[0], "the JSON must be marshalled once")
		})
	}
}

func TestQueryContext_JSONOnceDoesNotCacheAnError(t *testing.T) {
	t.Parallel()

	queryCtx := &queryContext{
		svc: NewService(),
		ctx: ffcontext.NewEvaluationContextBuilder("user-1").AddCustom("unsupported", make(chan int)).Build(),
	}

	_, err := queryCtx.jsonOnce()

	require.Error(t, err)
	assert.Nil(t, queryCtx.parsedEvalCtxAsBytes)
}

func TestQueryContext_DataOnceDoesNotCacheAnError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		// wantErr is the error of dataOnce, wantMatch whether a rule evaluates to true on that context
		wantErr   string
		wantMatch bool
	}{
		{name: "context that does not encode", value: make(chan int), wantErr: "json: unsupported type: chan int"},
		{
			name:    "context that encodes but does not decode",
			value:   json.Number("1e1000"),
			wantErr: "json: cannot unmarshal number 1e1000 into Go struct field .bad of type float64",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			queryCtx := &queryContext{
				svc: NewService(),
				ctx: ffcontext.NewEvaluationContextBuilder("user-1").AddCustom("bad", tt.value).Build(),
			}
			query := `{"==": [{"var": "key"}, "user-1"]}`

			_, err := queryCtx.dataOnce()

			require.EqualError(t, err, tt.wantErr)
			assert.Nil(t, queryCtx.parsedEvalCtxAsData)
			assert.Equal(t, tt.wantMatch, evaluateRule(queryCtx.svc.compiledQuery(query), queryCtx), "first rule")
			assert.Equal(
				t,
				tt.wantMatch,
				evaluateRule(queryCtx.svc.compiledQuery(query), queryCtx),
				"later rules must not see partial data",
			)
		})
	}
}

func TestRule_EvaluateBuildsTheMapOnlyForAQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		rule       Rule
		isDefault  bool
		wantMapped bool
	}{
		{
			name:       "rule with a query",
			rule:       Rule{Query: new(`key eq "user-1"`), VariationResult: new("A")},
			wantMapped: true,
		},
		{
			name: "rule without a query",
			rule: Rule{VariationResult: new("A")},
		},
		{
			name: "percentage rollout without a query",
			rule: Rule{Percentages: &map[string]float64{"A": 50, "B": 50}},
		},
		{
			name:      "default rule with a query",
			rule:      Rule{Query: new(`key eq "user-1"`), VariationResult: new("A")},
			isDefault: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			queryCtx := &queryContext{svc: NewService(), ctx: ffcontext.NewEvaluationContext("user-1")}

			_, err := tt.rule.evaluate("user-1", queryCtx, "my-flag", tt.isDefault)

			require.NoError(t, err)
			assert.Equal(t, tt.wantMapped, queryCtx.parsedEvalCtxAsMap != nil)
		})
	}
}

func TestInternalFlag_RulesOfBothQueryFormatsShareTheQueryContext(t *testing.T) {
	t.Parallel()

	f := InternalFlag{
		Variations: &map[string]*any{
			"nikunjy":     new(any("nikunjy")),
			"jsonlogic":   new(any("jsonlogic")),
			"jsonlogic-2": new(any("jsonlogic-2")),
			"default":     new(any("default")),
		},
		Rules: &[]Rule{
			{Query: new(`plan eq "free"`), VariationResult: new("nikunjy")},
			{Query: new(`{"==": [{"var": "plan"}, "pro"]}`), VariationResult: new("jsonlogic")},
			{Query: new(`{"==": [{"var": "plan"}, "team"]}`), VariationResult: new("jsonlogic-2")},
		},
		DefaultRule: &Rule{VariationResult: new("default")},
	}
	tests := []struct {
		name string
		plan string
		want string
	}{
		{name: "nikunjy rule matches", plan: "free", want: "nikunjy"},
		{name: "nikunjy rule misses and jsonlogic rule matches", plan: "pro", want: "jsonlogic"},
		{name: "first jsonlogic rule misses and second jsonlogic rule matches", plan: "team", want: "jsonlogic-2"},
		{name: "no rule matches", plan: "enterprise", want: "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := ffcontext.NewEvaluationContextBuilder("user-1").AddCustom("plan", tt.plan).Build()

			value, details := f.Value(
				"mixed-formats",
				ctx,
				Context{Service: NewService(), DefaultSdkValue: "sdk-default"},
			)

			assert.Equal(t, tt.want, value)
			assert.Equal(t, tt.want, details.Variant)
		})
	}
}
