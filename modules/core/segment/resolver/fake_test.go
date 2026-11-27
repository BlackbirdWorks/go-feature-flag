package resolver_test

import (
	"context"
	"errors"
	"maps"
	"sync"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
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
}

func (f *fakeStore) Contains(ctx context.Context, lookups []segment.Lookup) error {
	rendered := make([]string, len(lookups))
	for i := range lookups {
		rendered[i] = lookups[i].Segment + ":" + lookups[i].ID
	}

	f.mu.Lock()
	f.calls = append(f.calls, rendered)
	gate, entered, failAll, failSegments := f.gate, f.entered, f.fail, maps.Clone(f.failSegments)
	f.mu.Unlock()

	if entered != nil {
		entered <- struct{}{}
	}

	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
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

	var err error

	for i := range lookups {
		set, ok := f.data[lookups[i].Segment]
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
		close(f.gate)

		f.gate = nil
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
