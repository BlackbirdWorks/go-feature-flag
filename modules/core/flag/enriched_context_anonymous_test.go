package flag_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/modules/core/testutils/testconvert"
)

// the enrichment is merged in the attributes of the context and wins over them, so a targeting
// rule on "anonymous" must see the configured value (not the one of the caller's context).
func TestInternalFlag_Value_EnrichmentOverridesAnonymous(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		query      string
		callerAnon any // nil: attribute not set by the caller
		enrichment map[string]any
		wantMatch  bool
	}{
		{name: "nikunjy, enrichment sets anonymous", query: "anonymous eq true", enrichment: map[string]any{"anonymous": true}, wantMatch: true},
		{name: "jsonlogic, enrichment sets anonymous", query: `{"==":[{"var":"anonymous"},true]}`, enrichment: map[string]any{"anonymous": true}, wantMatch: true},
		{name: "enrichment overrides an anonymous caller", query: "anonymous eq true", callerAnon: true, enrichment: map[string]any{"anonymous": false}, wantMatch: false},
		{name: "enrichment without anonymous keeps the caller's", query: "anonymous eq true", callerAnon: true, enrichment: map[string]any{"other": 1}, wantMatch: true},
		{name: "no anonymous anywhere", query: "anonymous eq true", enrichment: map[string]any{"other": 1}, wantMatch: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &flag.InternalFlag{
				Variations: &map[string]*any{"A": testconvert.Interface("a"), "B": testconvert.Interface("b")},
				Rules: &[]flag.Rule{{
					Query: new(tt.query), VariationResult: new("B"),
				}},
				DefaultRule: &flag.Rule{VariationResult: new("A")},
			}
			b := ffcontext.NewEvaluationContextBuilder("user")
			if tt.callerAnon != nil {
				b = b.AddCustom("anonymous", tt.callerAnon)
			}
			ctx := b.Build()
			got, _ := f.Value("flag", ctx, flag.Context{EvaluationContextEnrichment: tt.enrichment})
			want := "a"
			if tt.wantMatch {
				want = "b"
			}
			assert.Equal(t, want, got)
		})
	}
}
