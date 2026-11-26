package manifestreader_test

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// span is the accepted range of a wait; an exact wait has min == max.
type span struct{ min, max time.Duration }

func exact(d time.Duration) span { return span{min: d, max: d} }

func TestReader_WaitsAreSpreadAndBounded(t *testing.T) {
	t.Parallel()

	const minWait = manifestreader.MinPollInterval

	tests := []struct {
		name   string
		want   []span
		cfg    manifestreader.Config
		source float64
	}{
		{
			name: "low end of the default jitter: 54 s, and no start phase of zero length",
			cfg:  manifestreader.Config{}, source: 0, want: []span{exact(54 * time.Second), exact(54 * time.Second)},
		},
		{
			name: "middle of the jitter: a 2.5 s start phase, then the interval",
			cfg:  manifestreader.Config{}, source: 0.5,
			want: []span{exact(2500 * time.Millisecond), exact(60 * time.Second), exact(60 * time.Second)},
		},
		{
			name: "the start phase is bounded by the interval when it is shorter than 5 s",
			cfg:  manifestreader.Config{PollInterval: 2 * time.Second, JitterFraction: 0.5}, source: 0.5,
			want: []span{exact(time.Second), exact(2 * time.Second), exact(2 * time.Second)},
		},
		{
			name: "a source below 0 is clamped to 0",
			cfg:  manifestreader.Config{}, source: -5, want: []span{exact(54 * time.Second), exact(54 * time.Second)},
		},
		{
			name:   "NaN is clamped to 0",
			cfg:    manifestreader.Config{},
			source: math.NaN(),
			want:   []span{exact(54 * time.Second), exact(54 * time.Second)},
		},
		{
			name: "a source of 1 or more stays below 1: waits never pass interval x (1+jitter)",
			cfg:  manifestreader.Config{}, source: 7,
			want: []span{
				{min: 4900 * time.Millisecond, max: 5 * time.Second},
				{min: 65 * time.Second, max: 66 * time.Second}, {min: 65 * time.Second, max: 66 * time.Second},
			},
		},
		{
			name: "no wait is shorter than the minimum",
			cfg:  manifestreader.Config{PollInterval: minWait, JitterFraction: 0.9}, source: 0,
			want: []span{exact(minWait), exact(minWait)},
		},
		{
			name: "no jitter: exactly the interval and no start phase, whatever the source says",
			cfg:  manifestreader.Config{PollInterval: 10 * time.Second, DisableJitter: true}, source: 0.5,
			want: []span{exact(10 * time.Second), exact(10 * time.Second)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			publish(t, store, 1, threeObjects()...)

			rdr := launchRig(t, store, &tt.cfg, manifestreader.WithJitter(func() float64 { return tt.source }))
			rdr.settle()

			for range len(tt.want) - 1 {
				rdr.round() // the requests are not under test here: the waits are
			}

			waits := rdr.sched.waited()
			require.Len(t, waits, len(tt.want))

			for i, got := range waits {
				assert.GreaterOrEqual(t, got, tt.want[i].min, "wait %d", i)
				assert.LessOrEqual(t, got, tt.want[i].max, "wait %d", i)
			}
		})
	}
}

func TestReader_StartPhaseDelaysTheFirstRound(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	publish(t, store, 1, threeObjects()...)

	rdr := launchRig(t, store, &manifestreader.Config{}, manifestreader.WithJitter(func() float64 { return 0.5 }))
	rdr.settle()

	assert.Empty(t, store.takeCalls(), "nothing was requested during the start phase")
	assert.Nil(t, rdr.snapshot())

	assert.Equal(t, sortCalls([]call{
		manifestCall(""), objCall(t, threeObjects()[0]), objCall(t, threeObjects()[1]), objCall(t, threeObjects()[2]),
	}), rdr.round())

	assert.NotNil(t, rdr.snapshot(), "the first round ran after the phase")
}

func TestReader_NoStartPhaseWhenRestoredStateIsServed(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	manifest := publish(t, store, 1, threeObjects()...)
	raw := mustMarshal(t, manifest)

	hooks := manifestreader.Hooks{Restore: func() (*manifestreader.Restored, error) {
		return &manifestreader.Restored{
			ETag:     etagOf(raw),
			Manifest: raw,
			Objects: [][]byte{
				[]byte(threeObjects()[0].content),
				[]byte(threeObjects()[1].content),
				[]byte(threeObjects()[2].content),
			},
		}, nil
	}}

	rdr := launchRig(t, store, &manifestreader.Config{},
		manifestreader.WithJitter(func() float64 { return 0.5 }), manifestreader.WithHooks(hooks))
	rdr.settle()

	assert.Equal(t, []call{manifestCall(etagOf(raw))}, store.takeCalls(), "the first round ran at once")
	assert.Equal(t, []time.Duration{60 * time.Second}, rdr.sched.waited(), "the only wait is the one after it")
	assert.NotNil(t, rdr.snapshot())
}
