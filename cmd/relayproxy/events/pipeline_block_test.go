package events_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
)

// dueTimers is an injected clock and timer: a wait fires when the test moves the clock to its due time.
type dueTimers struct {
	at      time.Time
	armed   chan struct{}
	pending []dueTimer
	mu      sync.Mutex
}

type dueTimer struct {
	due time.Time
	ch  chan time.Time
}

func newDueTimers() *dueTimers {
	return &dueTimers{at: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), armed: make(chan struct{}, 1<<10)}
}

func (d *dueTimers) now() time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.at
}

func (d *dueTimers) after(wait time.Duration) <-chan time.Time {
	d.mu.Lock()
	wake := make(chan time.Time, 1)
	d.pending = append(d.pending, dueTimer{due: d.at.Add(wait), ch: wake})
	d.mu.Unlock()

	d.armed <- struct{}{}

	return wake
}

func (d *dueTimers) advance(by time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.at = d.at.Add(by)

	left := d.pending[:0]

	for _, timer := range d.pending {
		if timer.due.After(d.at) {
			left = append(left, timer)

			continue
		}

		timer.ch <- d.at
	}

	d.pending = left
}

// TestDispatcher_BlockTimeoutRunsFromThePark: the block timeout of a batch runs from the moment it is parked, not
// from the moment it reaches the head of the waiting room: batches parked behind a blocked head are dropped (and
// counted as block_timeout) at their own deadline, and the batches that find room before it keep their order.
func TestDispatcher_BlockTimeoutRunsFromThePark(t *testing.T) {
	t.Parallel()

	const timeout = 10 * time.Second

	tests := []struct {
		name string
		// gaps are the moves of the clock after each parked batch (the first is the head)
		gaps        []time.Duration
		wantKeys    []string
		wantBlocked uint64
		release     bool
	}{
		{
			name: "dropped at their own deadline", gaps: []time.Duration{3 * time.Second, 3 * time.Second},
			wantKeys: []string{keyFlag0, eventOneKey, eventTwoKey}, wantBlocked: 3,
		},
		{
			name: "room before the deadline", gaps: []time.Duration{3 * time.Second, 3 * time.Second}, release: true,
			wantKeys: []string{keyFlag0, eventOneKey, eventTwoKey, "flag-3", keyFlag4, keyFlag5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fac := newFactory()
			fac.gate = make(chan struct{})
			fac.entered = make(chan struct{}, 1)
			tim := newDueTimers()

			t.Cleanup(func() { safeClose(fac.gate) })

			disp := startDispatcher(t, fac, &events.DispatcherConfig{
				Workers: 1, BatchSize: 1, HandoffSize: 2, After: tim.after, Now: tim.now,
			})

			expDef := def("a", "v1")
			expDef.Pipeline = events.Pipeline{Overflow: bootstrap.OverflowBlock, BlockTimeout: timeout}

			_, err := disp.Apply(context.Background(), []events.ExporterDef{expDef})
			require.NoError(t, err)

			require.True(t, disp.Collect(ev(0)))
			await(t, fac.entered, "the manager to hold the first event")

			for i := 1; i < 3; i++ {
				require.True(t, disp.Collect(ev(i)))
			}

			handed := func(want uint64) {
				require.Eventually(t, func() bool {
					return statusOf(t, new(disp.Status()), "a").Handed == want
				}, wait, time.Millisecond, "the event to be handed")
			}

			// the three batches are parked 0, 3 and 6 seconds into the test; the first is at the head
			handed(3)
			require.True(t, disp.Collect(ev(3)))
			handed(4)
			await(t, tim.armed, "the head to wait for room")

			parkedAt := 3

			for _, gap := range tt.gaps {
				tim.advance(gap)
				require.True(t, disp.Collect(ev(parkedAt+1)))
				parkedAt++
				handed(uint64(parkedAt + 1))
			}

			if tt.release {
				close(fac.gate)
				fac.of("a", 0).waitFor(t, len(tt.wantKeys))
			} else {
				// 10 s after the first park: only the first batch is due (the others were parked later)
				tim.advance(timeout - 6*time.Second)

				wantAfter := []uint64{1, 2, 3}

				for step, want := range wantAfter {
					require.Eventually(t, func() bool {
						return statusOf(t, new(disp.Status()), "a").Dropped.BlockTimeout == want
					}, wait, time.Millisecond, "batch %d to be dropped at its own deadline", step+1)

					if want < 3 {
						await(t, tim.armed, "the next batch to wait for its own deadline")
						tim.advance(3 * time.Second)
					}
				}

				close(fac.gate)
				fac.of("a", 0).waitFor(t, len(tt.wantKeys))
			}

			_, err = disp.Shutdown(bounded(t))
			require.NoError(t, err)

			got := statusOf(t, new(disp.Status()), "a")
			assert.Equal(t, tt.wantBlocked, got.Dropped.BlockTimeout)
			assert.Zero(t, got.Dropped.Drain)
			assert.Equal(t, tt.wantKeys, keys(fac.of("a", 0).got()), "the events that were delivered keep their order")
		})
	}
}
