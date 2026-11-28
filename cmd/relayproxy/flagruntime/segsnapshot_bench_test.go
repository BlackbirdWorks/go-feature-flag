package flagruntime_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

const staticAudienceDoc = segHeader + "  - name: audience\n    members: [alice, carol]\n"

// loadedSnapshotSet is a flag set whose audience segment is a loaded snapshot of the same members.
func loadedSnapshotSet(tb testing.TB) *flagruntime.FlagSet {
	tb.Helper()

	source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice, carolKey}})
	builder := viewBuilder(tb, &memBinder{source: source})
	built, err := builder.Build(tb.Context(), viewContent(tb, audienceDoc(connAudiences, "")))
	require.NoError(tb, err)

	set := built.Runtime.FlagSets["a"]
	require.NoError(tb, set.Segments.View.RefreshVersions(tb.Context(), time.Unix(1_000, 0)))

	return set
}

func staticSet(tb testing.TB) *flagruntime.FlagSet {
	tb.Helper()

	built, err := viewBuilder(tb, &memBinder{}).Build(tb.Context(), viewContent(tb, staticAudienceDoc))
	require.NoError(tb, err)

	return built.Runtime.FlagSets["a"]
}

// BenchmarkSnapshotSegment is the evaluation of a flag that uses a loaded snapshot segment against the same flag over
// a static segment with the same members. Allocations are the gate: a snapshot segment calls no store.
func BenchmarkSnapshotSegment(b *testing.B) {
	snap := loadedSnapshotSet(b)
	eval := flagruntime.NewEvaluator(newService(b), nil)
	evalCtx := ffcontext.NewEvaluationContext(memberAlice)

	for name, set := range map[string]*flagruntime.FlagSet{"snapshot": snap, "static": staticSet(b)} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, err := eval.BoolVariationDetails(b.Context(), set, audienceFlag, evalCtx, false)
				require.NoError(b, err)
			}
		})
	}
}
