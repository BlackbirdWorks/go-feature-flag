package flag

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thomaspoignant/go-feature-flag/modules/core/utils"
)

// TestEvaluatePercentageRollout_PinnedAssignments pins the bucketing of known keys, a user must keep the same
// variation when the algorithm is touched.
func TestEvaluatePercentageRollout_PinnedAssignments(t *testing.T) {
	t.Parallel()
	keys := []string{"user-1", "user-2", "user-3", "user-4", "user-5", "user-6"}
	tests := []struct {
		name        string
		percentages map[string]float64
		want        []string
	}{
		{"10/80/10", map[string]float64{"A": 10, "B": 80, "C": 10}, []string{"B", "C", "B", "B", "B", "A"}},
		{"33.3/33.3/33.4", map[string]float64{"A": 33.3, "B": 33.3, "C": 33.4}, []string{"B", "C", "A", "B", "C", "A"}},
		{"0.5/0.25/0.25", map[string]float64{"A": 0.5, "B": 0.25, "C": 0.25}, []string{"A", "A", "B", "B", "C", "C"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := Rule{Percentages: &tt.percentages}
			for i, key := range keys {
				got, err := r.EvaluatePercentageRollout(key, "my-flag")
				require.NoError(t, err)
				assert.Equal(t, tt.want[i], got, key)
			}
		})
	}
}

// TestEvaluatePercentageRollout_AlwaysFindsAVariation checks that no bucket is left without a variation when the
// float sum of the percentages is not exact (the sum used to depend on the map iteration order and could be 1 above
// the end of the last bucket).
func TestEvaluatePercentageRollout_AlwaysFindsAVariation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		percentages map[string]float64
	}{
		{"fuzz failure, sum above 100", map[string]float64{"A": 0.16666666666666666, "B": 0.5833333333333333, "C": 1.5}},
		{"thirds", map[string]float64{"A": 100.0 / 3, "B": 100.0 / 3, "C": 100.0 / 3}},
		{"tenths", map[string]float64{"A": 0.1, "B": 0.2, "C": 0.3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := Rule{Percentages: &tt.percentages}
			// Repeated: the map order used to be what made the evaluation fail.
			for i := 0; i < 200000; i++ {
				got, err := r.EvaluatePercentageRollout(fmt.Sprint("user-", i), "my-flag")
				require.NoError(t, err, "key %d", i)
				require.Contains(t, tt.percentages, got)
			}
		})
	}
}

// TestVariationFromBuckets_TopBucket covers the highest hash of the space directly, whatever the key.
func TestVariationFromBuckets_TopBucket(t *testing.T) {
	t.Parallel()
	p := map[string]float64{"A": 0.16666666666666666, "B": 0.5833333333333333, "C": 1.5}
	names := []string{"A", "B", "C"}
	total := 0.0
	for i := len(names) - 1; i >= 0; i-- {
		total += p[names[i]] * PercentageMultiplier
	}
	maxHash := uint32(total) - 1
	got, err := variationFromBuckets(p, names, maxHash)
	require.NoError(t, err)
	assert.Equal(t, "A", got)
	// the hash computed for the space size never goes out of it.
	assert.Less(t, utils.BuildHash("f", "k", uint32(total)), uint32(total))
}
