package flag

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/utils"
)

// TestEvaluatePercentageRollout_PinnedAssignments pins the bucketing of known keys. The expected values were
// produced by the code before the fix (only keys whose result was stable over many evaluations are listed): a user
// must keep the same variation, including when the sum of the percentages is not exact.
func TestEvaluatePercentageRollout_PinnedAssignments(t *testing.T) {
	t.Parallel()
	keys := []string{"user-1", "user-2", "user-3", "user-4", "user-5", "user-6", "user-7", "user-8"}
	tests := []struct {
		name        string
		percentages map[string]float64
		want        []string
	}{
		{"0.2/0.7 (modulus 899)", map[string]float64{"A": 0.2, "B": 0.7}, []string{"B", "B", "B", "B", "B", "A", "B", "A"}},
		{"33.3x3", map[string]float64{"A": 33.3, "B": 33.3, "C": 33.3}, []string{"A", "A", "A", "A", "A", "C", "C", "C"}},
		{"0.1/0.2/0.3", map[string]float64{"A": 0.1, "B": 0.2, "C": 0.3}, []string{"C", "C", "B", "B", "C", "C", "A", "A"}},
		{"10/80/10", map[string]float64{"A": 10, "B": 80, "C": 10}, []string{"B", "C", "B", "B", "B", "A", "B", "B"}},
		{"33.3/33.3/33.4", map[string]float64{"A": 33.3, "B": 33.3, "C": 33.4}, []string{"B", "C", "A", "B", "C", "A", "A", "B"}},
		{"0.5/0.25/0.25", map[string]float64{"A": 0.5, "B": 0.25, "C": 0.25}, []string{"A", "A", "B", "B", "C", "C", "A", "A"}},
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
			for i := range 200000 {
				got, err := r.EvaluatePercentageRollout(fmt.Sprint("user-", i), "my-flag")
				require.NoError(t, err, "key %d", i)
				require.Contains(t, tt.percentages, got)
			}
		})
	}
}

// TestVariationFromBuckets_TopBucket covers a hash beyond the end of the last bucket: it takes the last variation
// that has a share, never one without.
func TestVariationFromBuckets_TopBucket(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		p    map[string]float64
		hash uint32
		want string
	}{
		{"fuzz failure", map[string]float64{"A": 0.16666666666666666, "B": 0.5833333333333333, "C": 1.5}, 2250, "A"},
		{"last bucket without share", map[string]float64{"A": 0, "B": 0.5, "C": 0.5}, 1000, "B"},
		{"inside", map[string]float64{"A": 0.5, "B": 0.5}, 999, "A"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			names := []string{"A", "B", "C"}[:len(tt.p)]
			got, err := variationFromBuckets(tt.p, names, tt.hash)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// legacyEvaluate is the evaluation before the fix: the percentages are summed in the given order (it used to be the
// map iteration order) to get the modulus, the buckets are walked in reverse sorted order, and a hash beyond the
// last bucket is an error (returned as "").
func legacyEvaluate(p map[string]float64, sumOrder []string, key, flagName string) string {
	sum := 0.0
	for _, n := range sumOrder {
		sum += p[n]
	}
	hash := utils.BuildHash(flagName, key, uint32(sum*PercentageMultiplier))
	names := make([]string, 0, len(p))
	for n := range p {
		names = append(names, n)
	}
	slices.Sort(names)
	start := 0.0
	for _, name := range slices.Backward(names) {
		end := start + p[name]*PercentageMultiplier
		if uint32(start) <= hash && uint32(end) > hash {
			return name
		}
		start = end
	}
	return ""
}

func permutations(names []string) [][]string {
	if len(names) <= 1 {
		return [][]string{slices.Clone(names)}
	}
	var out [][]string
	for i := range names {
		rest := slices.Concat(names[:i:i], names[i+1:])
		for _, p := range permutations(rest) {
			out = append(out, append([]string{names[i]}, p...))
		}
	}
	return out
}

func percentageConfigs() []map[string]float64 {
	letters := []string{"A", "B", "C", "D", "E", "F"}
	weights := [][]float64{
		{0.2, 0.7}, {0.1, 0.2}, {0.1, 0.2, 0.7}, {0.1, 0.2, 0.3}, {33.3, 33.3, 33.3}, {33.3, 33.3, 33.4},
		{12.5, 12.5, 25, 50}, {0.1, 0.2, 0.3, 0.4}, {50, 50}, {10, 80, 10}, {1, 99}, {60, 40},
		{0.16666666666666666, 0.5833333333333333, 1.5}, {0.001, 0.002, 0.003}, {150, 75}, {0.7, 0.1, 0.1, 0.1},
		{16.7, 16.7, 16.6, 16.7, 16.6, 16.7}, {0.1, 0.1, 0.1, 0.1, 0.1, 0.1}, {0.3, 0.6, 0.1}, {0, 0.3, 0.7},
		{0.7, 0.2, 0}, {0.0001, 0.0002},
	}
	out := make([]map[string]float64, 0, len(weights))
	for _, w := range weights {
		m := map[string]float64{}
		for i, v := range w {
			m[letters[i]] = v
		}
		out = append(out, m)
	}
	return out
}

// TestEvaluatePercentageRollout_LegacyCompatibility checks, against the legacy algorithm over every summation order:
//   - when the legacy result is the same for every order, the result is the same (users do not move);
//   - when the legacy result depends on the order, the result is deterministic and one of the legacy results, or the
//     last variation with a share when the legacy code failed for some orders;
//   - when the legacy code failed for every order, a variation with a share is served.
func TestEvaluatePercentageRollout_LegacyCompatibility(t *testing.T) {
	t.Parallel()
	for i, cfg := range percentageConfigs() {
		t.Run(fmt.Sprint(i, cfg), func(t *testing.T) {
			t.Parallel()
			names := make([]string, 0, len(cfg))
			for n := range cfg {
				names = append(names, n)
			}
			slices.Sort(names)
			orders := permutations(names)
			r := Rule{Percentages: &cfg}
			for k := range 3000 {
				key := fmt.Sprint("user-", k)
				legacy := map[string]bool{}
				for _, o := range orders {
					legacy[legacyEvaluate(cfg, o, key, "my-flag")] = true
				}
				got, err := r.EvaluatePercentageRollout(key, "my-flag")
				require.NoError(t, err, key)
				require.Positive(t, cfg[got], "variation without share for %s", key)
				again, _ := r.EvaluatePercentageRollout(key, "my-flag")
				require.Equal(t, got, again, "not deterministic for %s", key)
				switch {
				case len(legacy) == 1 && !legacy[""]:
					require.True(t, legacy[got], "user %s moved to %s", key, got)
				case len(legacy) == 1: // the legacy code failed for every order
				case legacy[""]:
					// legacy failed for some orders: the variation is one of the legacy ones or the fallback.
				default:
					require.True(t, legacy[got], "user %s: %s is not one of %v", key, got, legacy)
				}
			}
		})
	}
}
