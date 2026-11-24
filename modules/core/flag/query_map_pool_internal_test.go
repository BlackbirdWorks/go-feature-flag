package flag

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
)

// The map given to the query engines is recycled between evaluations: an attribute of an
// evaluation must never be visible to another one.
func TestInternalFlag_Value_RecycledQueryMapDoesNotLeak(t *testing.T) {
	t.Parallel()
	f := &InternalFlag{
		Variations: &map[string]*any{
			"A": new(any("a")),
			"B": new(any("b")),
		},
		Rules:       &[]Rule{{Query: new(`secret eq "x"`), VariationResult: new("A")}},
		DefaultRule: &Rule{VariationResult: new("B")},
	}
	require.NoError(t, f.IsValid())

	tests := []struct {
		name string
		ctx  ffcontext.Context
		want string
	}{
		{name: "context with the attribute matches", want: "a",
			ctx: ffcontext.NewEvaluationContextBuilder("u1").AddCustom("secret", "x").Build()},
		{name: "context without the attribute does not match", want: "b",
			ctx: ffcontext.NewEvaluationContextBuilder("u2").AddCustom("other", "x").Build()},
		{name: "attribute with another value does not match", want: "b",
			ctx: ffcontext.NewEvaluationContextBuilder("u3").AddCustom("secret", "y").Build()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runParallel(300, func(int) {
				// interleave with a matching context to recycle a map holding the attribute
				_, _ = f.Value(
					"flag",
					ffcontext.NewEvaluationContextBuilder("m").AddCustom("secret", "x").Build(),
					Context{},
				)
				v, _ := f.Value("flag", tt.ctx, Context{})
				assert.Equal(t, tt.want, v)
			})
		})
	}
}
