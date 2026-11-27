package flagdef_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// evalAllocs is the allocations of one evaluation of a rule over the segment "beta" with the given definition, in its
// cheapest run among several single runs, so a stray allocation of the runtime does not decide the result.
func evalAllocs(t *testing.T, def segment.Definition) (float64, string) {
	t.Helper()

	catalog, err := segment.NewCatalog(def)
	require.NoError(t, err)

	svc := flagdef.NewService(flagdef.WithSegments(catalog))
	t.Cleanup(svc.Close)

	flag := onOffFlag(queryRule("r", nikunjyInclude))
	require.NoError(t, flag.IsValid(svc))

	// a provider that does not know the segment, so the failure policy of the definition applies as well
	provider := segment.NewMemoryProvider(nil).WithMissing(segment.MissingUnknown)
	evalCtx := ffcontext.NewEvaluationContext(keyAlice)
	flagCtx := flagdef.Context{Service: svc, Segments: provider}

	var variant string

	cheapest := math.MaxFloat64

	for range 30 {
		cheapest = min(cheapest, testing.AllocsPerRun(1, func() {
			_, details := flag.Value("f", evalCtx, flagCtx)
			variant = details.Variant
		}))
	}

	return cheapest, variant
}

// TestAVersionedSegmentAllocatesNothingExtraInAnEvaluation runs the real evaluation call: a definition with a version
// spec costs the same allocations as one without, so the evaluation path does not copy the definition.
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestAVersionedSegmentAllocatesNothingExtraInAnEvaluation(t *testing.T) {
	plain := segment.Definition{Name: "beta", Type: segment.TypeRedis, OnError: segment.OnErrorMember}
	versioned := plain
	versioned.Version = &segment.VersionSpec{Key: "seg:beta:version"}

	plainAllocs, plainVariant := evalAllocs(t, plain)
	versionedAllocs, versionedVariant := evalAllocs(t, versioned)

	assert.Equal(t, plainVariant, versionedVariant, "the same answer")
	assert.InDelta(t, plainAllocs, versionedAllocs, 0, "a versioned segment adds no allocation to the evaluation")
}
