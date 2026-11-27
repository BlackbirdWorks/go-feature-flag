package resolver_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

// deafStore returns a store that never returns until the test ends: it ignores its context.
func deafStore(t *testing.T) *fakeStore {
	t.Helper()

	store := betaStore()
	store.gate = gateFor(t)
	store.entered = make(chan struct{}, 16)
	store.deaf = true

	return store
}

func TestStuckCallsAreBreakerFailuresWhenTheyBecomeOverdue(t *testing.T) {
	t.Parallel()

	store := deafStore(t)
	res := newResolver(t, store, &resolver.Options{
		CallTimeout: time.Nanosecond,
		MaxOverdue:  100,
		Segments:    map[string]resolver.SegmentOptions{segBeta: {BreakerFailures: 2, NoCache: true}},
	})

	for round, id := range []string{"a", "b"} {
		lookups := []segment.Lookup{lk(segBeta, id)}

		err := res.Contains(context.Background(), lookups)

		require.ErrorIs(t, err, resolver.ErrWaiting, "round %d", round)
		assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))

		<-store.entered // the call is in the store, and it does not return

		want := round + 1

		require.Eventually(t, func() bool { return res.Status().Overdue == want }, eventuallyWait, eventuallyTick)
	}

	assert.Equal(
		t,
		resolver.BreakerOpen,
		res.Status().Segments[segBeta].Breaker,
		"two overdue calls opened the breaker",
	)

	third := []segment.Lookup{lk(segBeta, "c")}
	err := res.Contains(context.Background(), third)

	require.ErrorIs(t, err, resolver.ErrCircuitOpen)
	assert.Equal(t, []segment.State{segment.Unknown}, states(third))
	assert.Equal(
		t,
		[][]string{{"beta:a"}, {"beta:b"}},
		store.callList(),
		"no call for the third id while the others are stuck",
	)
}

func TestTooManyOverdueCallsStopNewOnes(t *testing.T) {
	t.Parallel()

	store := deafStore(t)
	rec := &segRecorder{}
	res := newResolver(t, store, &resolver.Options{
		CallTimeout: time.Nanosecond,
		MaxOverdue:  2,
		Observer:    rec,
		Default:     resolver.SegmentOptions{NoBreaker: true, NoCache: true},
	})

	for _, id := range []string{"a", "b"} {
		require.ErrorIs(t, res.Contains(context.Background(), []segment.Lookup{lk(segBeta, id)}), resolver.ErrWaiting)

		<-store.entered
	}

	require.Eventually(t, func() bool { return res.Status().Overdue == 2 }, eventuallyWait, eventuallyTick)

	third := []segment.Lookup{lk(segBeta, "c")}
	err := res.Contains(context.Background(), third)

	require.ErrorIs(t, err, resolver.ErrOverdue)
	assert.Equal(t, []segment.State{segment.Unknown}, states(third))
	assert.Equal(t, [][]string{{"beta:a"}, {"beta:b"}}, store.callList())

	_, segs := rec.observed()
	assert.Equal(t, resolver.ReasonOverdue, segs[len(segs)-1].Failure)
}

// gatedLifecycle is a store whose Init and Exists wait on a gate, and that records what reaches it.
type gatedLifecycle struct {
	*fakeStore

	existsEntered chan struct{}
	existsGate    chan struct{}
	events        []string
	mu            sync.Mutex
}

func (g *gatedLifecycle) Init(context.Context) error { //nolint:unparam // the interface returns an error
	g.record("init")

	return nil
}

func (g *gatedLifecycle) Exists(
	_ context.Context,
	names []string,
) ([]segment.State, error) { //nolint:unparam // the interface returns an error
	g.record("exists")

	if g.existsEntered != nil {
		g.existsEntered <- struct{}{}

		<-g.existsGate
	}

	return make([]segment.State, len(names)), nil
}

func (g *gatedLifecycle) record(event string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.events = append(g.events, event)
}

func (g *gatedLifecycle) seen() []string {
	g.mu.Lock()
	defer g.mu.Unlock()

	return append([]string{}, g.events...)
}

func TestInitAndExistsGoThroughTheShutdownAdmission(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		call       func(*resolver.Resolver) error
		wantEvents []string
	}{
		{
			name:       "Init",
			call:       func(res *resolver.Resolver) error { return res.Init(context.Background()) },
			wantEvents: []string{"init"},
		},
		{
			name: "Exists",
			call: func(res *resolver.Resolver) error {
				_, err := res.Exists(context.Background(), []string{segBeta})

				return err
			},
			wantEvents: []string{"exists"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" reaches the store before Shutdown and not after", func(t *testing.T) {
			t.Parallel()

			store := &gatedLifecycle{fakeStore: betaStore()}
			res := newResolver(t, store, &resolver.Options{})

			require.NoError(t, tt.call(res))
			require.NoError(t, res.Shutdown(context.Background()))

			err := tt.call(res)

			require.ErrorIs(t, err, resolver.ErrClosed)
			assert.Equal(t, tt.wantEvents, store.seen(), "nothing reached the store after Shutdown")
		})
	}
}

func TestShutdownWaitsForAnExistsInFlight(t *testing.T) {
	t.Parallel()

	store := &gatedLifecycle{fakeStore: betaStore(), existsEntered: make(chan struct{}, 1), existsGate: gateFor(t)}
	res := newResolver(t, store, &resolver.Options{})

	var wg sync.WaitGroup

	var existsErr error

	wg.Go(func() {
		_, existsErr = res.Exists(context.Background(), []string{segBeta})
	})

	<-store.existsEntered

	ended, cancel := context.WithCancel(context.Background())
	cancel()

	err := res.Shutdown(ended)

	require.ErrorIs(t, err, resolver.ErrShutdown)
	assert.Equal(
		t,
		"resolver: shutdown ended before the calls in flight: 1 calls still running: context canceled",
		err.Error(),
	)

	close(store.existsGate)
	wg.Wait()

	require.NoError(t, existsErr)
	require.NoError(t, res.Shutdown(context.Background()))
}
