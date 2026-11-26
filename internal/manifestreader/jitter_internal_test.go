package manifestreader

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDefaultJitter_IsAProbabilitySource checks the real jitter source the reader uses when none is injected: every
// value is in [0, 1), the values are not constant, and they spread over the range.
func TestDefaultJitter_IsAProbabilitySource(t *testing.T) {
	t.Parallel()

	const draws = 4000

	var (
		sum    float64
		first  = defaultJitter()
		varies bool
		bucket [4]int
	)

	for range draws {
		value := defaultJitter()

		assert.GreaterOrEqual(t, value, 0.0)
		assert.Less(t, value, 1.0)

		varies = varies || value != first
		sum += value
		bucket[int(value*float64(len(bucket)))]++
	}

	assert.True(t, varies, "the source is not constant")
	assert.InDelta(t, 0.5, sum/draws, 0.05, "the mean of uniform draws")

	for i, count := range bucket {
		assert.Greater(t, count, draws/8, "quarter %d of the range is used", i)
	}
}
