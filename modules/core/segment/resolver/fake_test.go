package resolver_test

import (
	"context"
	"errors"
	"maps"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

var (
	errStore = errors.New("store failure")
	errBoom  = errors.New("boom")
)

// fakeStore is a scripted segment.Provider: it answers from its data, records every call as "segment:id" strings
// and can fail, hang on a gate or panic.
type fakeStore struct {
	fail         error
	panicValue   any
	data         map[string]map[string]bool
	gate         chan struct{}
	entered      chan struct{}
	failSegments map[string]bool
	calls        [][]string
	ctxErrAt     []error
	mu           sync.Mutex
	deaf         bool
}

func (f *fakeStore) Contains(ctx context.Context, lookups []segment.Lookup) error {
	rendered := make([]string, len(lookups))
	for i := range lookups {
		rendered[i] = lookups[i].Segment + ":" + lookups[i].ID
	}

	f.mu.Lock()
	f.calls = append(f.calls, rendered)
	gate, entered, failAll, failSegments := f.gate, f.entered, f.fail, maps.Clone(f.failSegments)
	data, deaf := f.data, f.deaf // the answers of a call are the data when it started
	f.mu.Unlock()

	if entered != nil {
		entered <- struct{}{}
	}

	waitErr := waitOn(ctx, gate, deaf)
	if waitErr != nil {
		return waitErr
	}

	f.mu.Lock()
	f.ctxErrAt = append(f.ctxErrAt, ctx.Err())
	f.mu.Unlock()

	if f.panicValue != nil {
		panic(f.panicValue)
	}

	if failAll != nil {
		return failAll
	}

	return answerFrom(data, failSegments, lookups)
}

// waitOn waits on the gate; a store that is deaf ignores its context.
func waitOn(ctx context.Context, gate chan struct{}, deaf bool) error {
	switch {
	case gate == nil:
		return nil
	case deaf:
		<-gate

		return nil
	}

	select {
	case <-gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// answerFrom sets the state of the lookups from the data; the segments in failSegments are left Unknown.
func answerFrom(data map[string]map[string]bool, failSegments map[string]bool, lookups []segment.Lookup) error {
	var err error

	for i := range lookups {
		set, ok := data[lookups[i].Segment]
		if !ok {
			continue
		}

		if failSegments[lookups[i].Segment] {
			err = errStore

			continue
		}

		if set[lookups[i].ID] {
			lookups[i].State = segment.Member
		} else {
			lookups[i].State = segment.NotMember
		}
	}

	return err
}

func members(sets map[string][]string) map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(sets))

	for name, ids := range sets {
		out[name] = make(map[string]bool, len(ids))
		for _, id := range ids {
			out[name][id] = true
		}
	}

	return out
}

// Fail, FailSegment, Hang, Release and Heal make the fake the backend of the conformance suite.
func (f *fakeStore) Fail() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.fail = errStore
}

func (f *fakeStore) FailSegment(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failSegments == nil {
		f.failSegments = map[string]bool{}
	}

	f.failSegments[name] = true
}

func (f *fakeStore) Hang() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.gate = make(chan struct{})
	f.entered = make(chan struct{}, 64)

	return f.entered
}

func (f *fakeStore) Release() {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.gate != nil {
		select {
		case <-f.gate: // the test opened it already
		default:
			close(f.gate)
		}

		f.gate = nil
		f.entered = nil // a call that comes after the release does not signal
	}
}

func (f *fakeStore) Heal() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.fail = nil
	f.failSegments = nil
}

func (f *fakeStore) callList() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([][]string{}, f.calls...)
}

func (f *fakeStore) ctxErrors() []error {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]error{}, f.ctxErrAt...)
}

// setData replaces the members: the calls that started before keep the data they started with.
func (f *fakeStore) setData(data map[string]map[string]bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.data = data
}

// gateFor returns a gate that is opened when the test ends, whatever the test did: a store that waits on it never
// outlives the test, even when an assertion stopped the test before it opened the gate.
func gateFor(t *testing.T) chan struct{} {
	t.Helper()

	gate := make(chan struct{})

	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})

	return gate
}

// storeUnderTest is a store with the call list of the fake.
type storeUnderTest interface {
	segment.Provider
	callList() [][]string
}

// existsStore is a fake store that also answers Exists, the way a store that can tell does: a segment that its data
// do not hold is not loaded (NotMember).
type existsStore struct {
	*fakeStore
}

func (e existsStore) Exists(
	_ context.Context,
	names []string,
) ([]segment.State, error) {
	e.mu.Lock()
	data := e.data
	e.mu.Unlock()

	out := make([]segment.State, len(names))

	for i, name := range names {
		out[i] = segment.NotMember

		if _, held := data[name]; held {
			out[i] = segment.Member
		}
	}

	return out, nil
}

// waitIdle waits until the shared calls of the resolver, and the checks that follow them, are over.
func waitIdle(t *testing.T, res *resolver.Resolver) {
	t.Helper()

	require.Eventually(t, func() bool { return res.Status().Running == 0 }, eventuallyWait, eventuallyTick)
}
