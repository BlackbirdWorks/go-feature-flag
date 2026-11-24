package clocktest_test

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/clock"
	"github.com/blackbirdworks/semaphore/modules/core/testutils/clocktest"
)

const testStep = time.Second

// TestMain pins the clock once for this package, like every package that uses clocktest.
func TestMain(m *testing.M) {
	clocktest.FreezeStepping(testStep)
	os.Exit(m.Run())
}

func TestInstant(t *testing.T) {
	t.Parallel()

	want := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	assert.Equal(t, want, clocktest.Instant())
}

func TestFreezeStepping_ClockIsStrictlyIncreasingAndUnique(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		goroutines int
		calls      int
	}{
		{name: "one goroutine", goroutines: 1, calls: 100},
		{name: "concurrent goroutines", goroutines: 8, calls: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var mu sync.Mutex
			seen := map[time.Time]bool{}
			var wg sync.WaitGroup
			for range tt.goroutines {
				wg.Go(func() {
					var previous time.Time
					for range tt.calls {
						now := clock.Now()
						assert.True(t, now.After(previous), "each call is later than the previous one of the goroutine")
						previous = now
						mu.Lock()
						seen[now] = true
						mu.Unlock()
					}
				})
			}
			wg.Wait()

			assert.Len(t, seen, tt.goroutines*tt.calls, "no two calls return the same instant")
			for now := range seen {
				assert.Zero(
					t,
					now.Sub(clocktest.Instant())%testStep,
					"the instants are Instant plus a multiple of the step",
				)
			}
		})
	}
}
