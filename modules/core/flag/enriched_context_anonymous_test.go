package flag_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

// the enrichment is merged in the attributes of the context and wins over them, so a targeting
// rule on "anonymous" must see the configured value (not the one of the caller's context).
func TestInternalFlag_Value_EnrichmentOverridesAnonymous(t *testing.T) {
	t.Parallel()

	tests := []struct {
		callerAnon any // nil: attribute not set by the caller
		enrichment map[string]any
		name       string
		query      string
		wantMatch  bool
	}{
		{
			name:       "nikunjy, enrichment sets anonymous",
			query:      fixtures.AnonymousEqTrue,
			enrichment: map[string]any{fixtures.Anonymous: true},
			wantMatch:  true,
		},
		{
			name:       "jsonlogic, enrichment sets anonymous",
			query:      `{"==":[{"var":"anonymous"},true]}`,
			enrichment: map[string]any{fixtures.Anonymous: true},
			wantMatch:  true,
		},
		{
			name:       "enrichment overrides an anonymous caller",
			query:      fixtures.AnonymousEqTrue,
			callerAnon: true,
			enrichment: map[string]any{fixtures.Anonymous: false},
			wantMatch:  false,
		},
		{
			name:       "enrichment without anonymous keeps the caller's",
			query:      fixtures.AnonymousEqTrue,
			callerAnon: true,
			enrichment: map[string]any{"other": 1},
			wantMatch:  true,
		},
		{
			name:       "no anonymous anywhere",
			query:      fixtures.AnonymousEqTrue,
			enrichment: map[string]any{"other": 1},
			wantMatch:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			targetFlag := &flag.InternalFlag{
				Variations: &map[string]*any{"A": new(any("a")), "B": new(any("b"))},
				Rules: &[]flag.Rule{{
					Query: new(tt.query), VariationResult: new("B"),
				}},
				DefaultRule: &flag.Rule{VariationResult: new("A")},
			}

			b := ffcontext.NewEvaluationContextBuilder(fixtures.User)
			if tt.callerAnon != nil {
				b = b.AddCustom(fixtures.Anonymous, tt.callerAnon)
			}

			ctx := b.Build()
			got, _ := targetFlag.Value(
				fixtures.FlagKeyBare,
				ctx,
				flag.Context{Service: flag.NewService(), EvaluationContextEnrichment: tt.enrichment},
			)

			want := "a"
			if tt.wantMatch {
				want = "b"
			}

			assert.Equal(t, want, got)
		})
	}
}
