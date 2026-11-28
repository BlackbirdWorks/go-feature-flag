package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// BenchmarkDynamicSegments is the cost of an evaluation that reaches a dynamic segment answered from the cache of
// the resolver (one batch, no call to the store), against a flag of the same set that uses none.
func BenchmarkDynamicSegments(b *testing.B) {
	source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
	builder := viewBuilder(b, &memBinder{source: source})
	built, buildErr := builder.Build(b.Context(), viewContent(b, lookupDoc))
	require.NoError(b, buildErr)

	builder.Commit(built)

	set := built.Runtime.FlagSets["a"]
	eval := flagruntime.NewEvaluator(newService(b), nil)
	evalCtx := ffcontext.NewEvaluationContext(memberAlice)

	for _, flag := range []string{"plain-flag", audienceFlag} {
		b.Run(flag, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, err := eval.BoolVariationDetails(set, flag, evalCtx, false)
				require.NoError(b, err)
			}
		})
	}

	b.Run("all-flags", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			_ = eval.AllFlagsState(set, evalCtx)
		}
	})
}
