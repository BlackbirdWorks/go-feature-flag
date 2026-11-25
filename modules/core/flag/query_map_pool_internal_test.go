package flag

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

// The map given to the query engines is recycled between evaluations: an attribute of an
// evaluation must never be visible to another one.
func TestInternalFlag_Value_RecycledQueryMapDoesNotLeak(t *testing.T) {
	t.Parallel()

	flg := &InternalFlag{
		Variations: &map[string]*any{
			"A": new(any("a")),
			"B": new(any("b")),
		},
		Rules:       &[]Rule{{Query: new(`secret eq "x"`), VariationResult: new("A")}},
		DefaultRule: &Rule{VariationResult: new("B")},
	}
	svc := NewService()
	require.NoError(t, flg.IsValid(svc))

	tests := []struct {
		name string
		ctx  ffcontext.Context
		want string
	}{
		{name: "context with the attribute matches", want: "a",
			ctx: ffcontext.NewEvaluationContextBuilder("u1").AddCustom(fixtures.SecretValue, "x").Build()},
		{name: "context without the attribute does not match", want: "b",
			ctx: ffcontext.NewEvaluationContextBuilder("u2").AddCustom("other", "x").Build()},
		{name: "attribute with another value does not match", want: "b",
			ctx: ffcontext.NewEvaluationContextBuilder("u3").AddCustom(fixtures.SecretValue, "y").Build()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runParallel(300, func(int) {
				// interleave with a matching context to recycle a map holding the attribute
				_, _ = flg.Value(
					fixtures.FlagKeyBare,
					ffcontext.NewEvaluationContextBuilder("m").AddCustom(fixtures.SecretValue, "x").Build(),
					Context{Service: svc},
				)
				v, _ := flg.Value(fixtures.FlagKeyBare, tt.ctx, Context{Service: svc})
				assert.Equal(t, tt.want, v)
			})
		})
	}
}
