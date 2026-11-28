package redissegment_test

import (
	"bytes"
	"context"
	"fmt"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
)

const (
	// boundTimeout bounds every wait of these tests. It is a failure bound, not a measured time.
	boundTimeout = 30 * time.Second
	// panicText stands for a secret that a panic value could carry.
	panicText = "secret-panic-text"
	// msgUnavailable1of2 is the text of a failure of one lookup in two.
	msgUnavailable1of2 = "redis segment: unavailable: 1 of 2 lookups failed"
	msgUnavailable2of2 = "redis segment: unavailable: 2 of 2 lookups failed"
	segOther           = "other"
)

// boundedCtx returns a context that ends by itself, so a hung call or shutdown fails the test and does not block it.
func boundedCtx(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), boundTimeout)
	t.Cleanup(cancel)

	return ctx
}

// gate is a store hook that holds every pipeline until release, and says when one has entered. Its release is
// registered in the cleanup of the test, so it runs before anything registered earlier.
type gate struct {
	entered chan struct{}
	open    chan struct{}
	passed  atomic.Int32
	once    sync.Once
}

func newGate(t *testing.T) *gate {
	t.Helper()

	g := &gate{entered: make(chan struct{}, 8), open: make(chan struct{})}
	t.Cleanup(g.release)

	return g
}

func (g *gate) release() { g.once.Do(func() { close(g.open) }) }

// hold waits for the release and ignores the context (a client that does not honour it).
func (g *gate) hold(context.Context) error {
	g.entered <- struct{}{}

	<-g.open
	g.passed.Add(1)

	return nil
}

// holdCtx waits for the release or the end of the context.
func (g *gate) holdCtx(ctx context.Context) error {
	g.entered <- struct{}{}

	select {
	case <-g.open:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *gate) waitEntered(t *testing.T) {
	t.Helper()

	select {
	case <-g.entered:
	case <-boundedCtx(t).Done():
		require.FailNow(t, "no pipeline reached the store")
	}
}

func TestClusterRedirectsAndCrossSlotAreServerFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
	}{
		{name: "moved", text: "MOVED 3999 10.9.8.7:6381"},
		{name: "ask", text: "ASK 3999 10.9.8.7:6381"},
		{name: "cross slot", text: "CROSSSLOT Keys in request don't hash to the same slot"},
		{name: "cluster down", text: "CLUSTERDOWN The cluster is down"},
	}

	cases := make([]failureCase, len(tests))

	for i, tt := range tests {
		cases[i] = failureCase{
			name: tt.name,
			store: &fakeStore{
				sets:  failureSetBeta(),
				reply: func([]any) *fakeReply { return &fakeReply{err: serverError{msg: tt.text}} },
			},
			lookups:     []segment.Lookup{lk(segBeta, idAlice)},
			wantStates:  []segment.State{segment.Unknown},
			wantClass:   redissegment.ClassServer,
			wantSentry:  redissegment.ErrServer,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: "redis segment: server: 1 of 1 lookups failed",
		}
	}

	runFailureCases(t, cases)
}

func TestAPanickingClientIsRecovered(t *testing.T) {
	t.Parallel()

	var armed atomic.Bool

	armed.Store(true)

	store := &fakeStore{
		sets: map[string]map[string]bool{keyBeta: {idAlice: true}},
		exec: func(context.Context) error {
			if armed.Load() {
				panic(panicText)
			}

			return nil
		},
	}
	prov := newProvider(
		t,
		store,
		&redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
	)
	ctx := boundedCtx(t)

	// Contains: the state the caller left is not kept, and the error has a fixed text
	lookups := []segment.Lookup{{Segment: segBeta, ID: idAlice, State: segment.Member}, lk(segBeta, "zed")}
	err := prov.Contains(ctx, lookups)

	require.ErrorIs(t, err, redissegment.ErrUnavailable)
	assert.Equal(t, "redis segment: unavailable: 2 of 2 lookups failed", err.Error())
	assert.Equal(t, []segment.State{segment.Unknown, segment.Unknown}, states(lookups))
	assert.Equal(t, redissegment.ClassUnavailable, prov.Status().LastFailure)

	// Init
	err = prov.Init(ctx)

	require.ErrorIs(t, err, redissegment.ErrInit)
	require.ErrorIs(t, err, redissegment.ErrUnavailable)
	assert.NotContains(t, err.Error(), panicText)

	// Exists
	got, err := prov.Exists(ctx, []string{segBeta})

	require.ErrorIs(t, err, redissegment.ErrUnavailable)
	assert.NotContains(t, err.Error(), panicText)
	assert.Equal(t, []segment.State{segment.Unknown}, got)

	// the provider still works once the client does
	armed.Store(false)

	lookups = []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")}

	require.NoError(t, prov.Contains(ctx, lookups))
	assert.Equal(t, []segment.State{segment.Member, segment.NotMember}, states(lookups))
}

func TestACallAfterShutdownNeverReachesTheStore(t *testing.T) {
	t.Parallel()

	store := &fakeStore{sets: map[string]map[string]bool{keyBeta: {idAlice: true}}}
	prov := newProvider(
		t,
		store,
		&redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
	)

	require.NoError(t, prov.Shutdown(boundedCtx(t)))

	lookups := []segment.Lookup{{Segment: segBeta, ID: idAlice, State: segment.Member}}
	states2, existsErr := prov.Exists(boundedCtx(t), []string{segBeta})

	require.ErrorIs(t, prov.Contains(boundedCtx(t), lookups), redissegment.ErrClosed)
	assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))
	require.ErrorIs(t, prov.Init(boundedCtx(t)), redissegment.ErrClosed)
	require.ErrorIs(t, existsErr, redissegment.ErrClosed)
	assert.Equal(t, []segment.State{segment.Unknown}, states2)
	assert.Empty(t, store.commands())
}

func TestAnEndedContextMakesNoCall(t *testing.T) {
	t.Parallel()

	store := &fakeStore{sets: map[string]map[string]bool{keyBeta: {idAlice: true}}}
	prov := newProvider(
		t,
		store,
		&redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
	)
	ctx, cancel := context.WithCancel(context.Background())

	cancel()

	initErr := prov.Init(ctx)
	got, existsErr := prov.Exists(ctx, []string{segBeta})

	require.ErrorIs(t, initErr, redissegment.ErrInit)
	require.ErrorIs(t, initErr, redissegment.ErrCanceled)
	require.ErrorIs(t, existsErr, redissegment.ErrCanceled)
	assert.Equal(t, []segment.State{segment.Unknown}, got)
	assert.Empty(t, store.commands())
}

func TestShutdownWaitsForTheCallsInFlight(t *testing.T) {
	t.Parallel()

	hold := newGate(t)
	store := &fakeStore{sets: map[string]map[string]bool{keyBeta: {idAlice: true}}, exec: hold.hold}
	prov := newProvider(
		t,
		store,
		&redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
	)
	lookups := []segment.Lookup{lk(segBeta, idAlice)}
	containsDone := make(chan error, 1)
	shutdownDone := make(chan error, 1)
	passedAtReturn := make(chan int32, 1)

	go func() { containsDone <- prov.Contains(boundedCtx(t), lookups) }()

	hold.waitEntered(t)

	go func() {
		err := prov.Shutdown(boundedCtx(t))

		passedAtReturn <- hold.passed.Load()

		shutdownDone <- err
	}()

	// Shutdown marks the provider closed first, and then waits for the call
	require.Eventually(t, func() bool { return prov.Health().Closed }, boundTimeout, time.Millisecond)

	hold.release()

	require.NoError(t, <-containsDone)
	require.NoError(t, <-shutdownDone)
	assert.Equal(t, int32(1), <-passedAtReturn, "Shutdown returned before the call left the store")
	assert.Equal(t, []segment.State{segment.Member}, states(lookups))
}

func TestShutdownThatRunsOutOfTimeEndsTheCallsInFlight(t *testing.T) {
	t.Parallel()

	hold := newGate(t)
	store := &fakeStore{sets: map[string]map[string]bool{keyBeta: {idAlice: true}}, exec: hold.holdCtx}
	prov := newProvider(
		t,
		store,
		&redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
	)
	lookups := []segment.Lookup{lk(segBeta, idAlice)}
	containsDone := make(chan error, 1)

	go func() { containsDone <- prov.Contains(boundedCtx(t), lookups) }()

	hold.waitEntered(t)

	expired, cancel := context.WithCancel(context.Background())

	cancel()

	err := prov.Shutdown(expired)

	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, <-containsDone, redissegment.ErrCanceled)
	assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))
	assert.Equal(t, [][]string{{cmdBeta + " " + idAlice, existsBeta}}, store.commands())
}

func TestNoOpCallsAfterShutdownAreClosed(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	prov := newProvider(
		t,
		store,
		&redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
	)

	require.NoError(t, prov.Shutdown(boundedCtx(t)))

	tests := []struct {
		call func() error
		name string
	}{
		{name: "an empty batch", call: func() error { return prov.Contains(boundedCtx(t), []segment.Lookup{}) }},
		{name: "no lookup at all", call: func() error { return prov.Contains(boundedCtx(t), nil) }},
		{
			name: "no name",
			call: func() error {
				_, err := prov.Exists(boundedCtx(t), nil)

				return err
			},
		},
		{
			name: "names of no set segment",
			call: func() error {
				_, err := prov.Exists(boundedCtx(t), []string{segOther})

				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.ErrorIs(t, tt.call(), redissegment.ErrClosed)
		})
	}

	assert.Empty(t, store.commands())
}

// goroutinesLabeled counts the goroutines that carry the pprof label value and run the function, from the text
// form of the profile.
func goroutinesLabeled(t *testing.T, value, function string) int {
	t.Helper()

	var dump bytes.Buffer

	require.NoError(t, pprof.Lookup("goroutine").WriteTo(&dump, 1))

	total := 0

	for group := range strings.SplitSeq(dump.String(), "\n\n") {
		if !strings.Contains(group, `# labels: {"`+labelKey+`":"`+value+`"}`) || !strings.Contains(group, function) {
			continue
		}

		var count int

		_, err := fmt.Sscanf(group, "%d @", &count)
		require.NoError(t, err)

		total += count
	}

	return total
}

const labelKey = "redissegment-test"

func TestShutdownCallsShareOneWaiter(t *testing.T) {
	t.Parallel()

	hold := newGate(t)
	store := &fakeStore{sets: map[string]map[string]bool{keyBeta: {idAlice: true}}, exec: hold.hold}
	prov := newProvider(
		t,
		store,
		&redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
	)
	containsDone := make(chan error, 1)

	go func() { containsDone <- prov.Contains(boundedCtx(t), []segment.Lookup{lk(segBeta, idAlice)}) }()

	hold.waitEntered(t)

	expired, cancel := context.WithCancel(context.Background())

	cancel()

	// the goroutines that Shutdown starts inherit the label of the caller
	const attempts = 3

	pprof.Do(context.Background(), pprof.Labels(labelKey, t.Name()), func(context.Context) {
		for range attempts {
			require.ErrorIs(t, prov.Shutdown(expired), context.Canceled)
		}
	})

	assert.Equal(t, 1, goroutinesLabeled(t, t.Name(), "(*Provider).Shutdown"), "one waiter, not one per Shutdown call")

	hold.release()

	require.NoError(t, <-containsDone)
	require.NoError(t, prov.Shutdown(boundedCtx(t)))
}
