package resolver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// With no deadline on the first call, the grace is counted from that call: a slow drain uses it up.
func TestTheGraceWithoutADeadlineIsCountedFromTheFirstCall(t *testing.T) {
	t.Parallel()

	const grace = 5 * time.Second

	first := baseTime()

	tests := []struct {
		name          string
		sinceFirst    time.Duration
		wantRemaining time.Duration
	}{
		{name: "the store shutdown starts at once", sinceFirst: 0, wantRemaining: grace},
		{name: "a drain that took part of the grace", sinceFirst: 2 * time.Second, wantRemaining: 3 * time.Second},
		{name: "a drain that took all of it", sinceFirst: grace, wantRemaining: 0},
		{name: "a drain that took more", sinceFirst: 2 * grace, wantRemaining: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bound := shutdownBound{at: first}

			assert.Equal(t, tt.wantRemaining, bound.remaining(first.Add(tt.sinceFirst), grace))
		})
	}
}
