package flagdef

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The validation of a flag compiles queries that may never run: it must not leave them in the Service, which keeps
// what the evaluations compile until it is closed.
func TestValidationDoesNotMemoizeQueries(t *testing.T) {
	t.Parallel()

	svc := NewService()

	rule := Rule{Name: new("r"), Query: new(`{"inSegment": [{"var": "k"}, "a"]}`), VariationResult: new("on")}
	flag := &InternalFlag{
		Variations: &map[string]*any{"on": new(any(true)), "off": new(any(false))},
		Rules:      &[]Rule{rule},
		DefaultRule: &Rule{
			Query:           new(`{"==": [{"var": "k"}, "x"]}`),
			VariationResult: new("off"),
		},
	}

	require.NoError(t, flag.IsValid(svc))
	assert.Equal(t, []string{"a"}, flag.SegmentNames(svc))
	assert.True(t, flag.UsesSegments(svc))

	count := 0

	svc.compiled.Range(func(_, _ any) bool {
		count++

		return true
	})

	assert.Equal(t, 0, count, "nothing is memoized by the validation")
}
