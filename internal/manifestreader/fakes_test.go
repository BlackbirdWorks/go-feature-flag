package manifestreader_test

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// wait is the bound of every wait of a test: a missing event fails the test instead of hanging it.
const wait = 10 * time.Second

// call is one request the fake store received.
type call struct {
	Key       string
	ETag      string
	VersionID string
}

// fakeStore is an in-memory ObjectStore that records every call. The ETag of an object is derived from its
// content, so an unchanged object answers a conditional call with ErrNotModified.
type fakeStore struct {
	objects map[string][]byte
	etags   map[string]string
	fails   map[string][]error
	// gate, when set, runs inside Get for every key other than the manifest, after the call is counted in flight.
	gate func(ctx context.Context, key string)
	// hang holds the keys whose Get blocks until its context ends; hung is told when one blocks.
	hang        map[string]bool
	hung        chan string
	calls       []call
	inFlight    int
	maxInFlight int
	mu          sync.Mutex
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		objects: map[string][]byte{}, etags: map[string]string{}, fails: map[string][]error{},
		hang: map[string]bool{}, hung: make(chan string, 64),
	}
}

func etagOf(content []byte) string {
	return `"` + string(hexSum(content)) + `"`
}

func (f *fakeStore) Get(ctx context.Context, req manifestreader.GetRequest) (manifestreader.Object, error) {
	key, etag, limit := req.Key, req.ETag, req.Limit

	f.mu.Lock()
	f.calls = append(f.calls, call{Key: key, ETag: etag, VersionID: req.VersionID})
	f.inFlight++
	f.maxInFlight = max(f.maxInFlight, f.inFlight)
	gate := f.gate
	f.mu.Unlock()

	defer func() {
		f.mu.Lock()
		f.inFlight--
		f.mu.Unlock()
	}()

	if gate != nil && key != manifestKey {
		gate(ctx, key)
	}

	f.mu.Lock()
	hang := f.hang[key]
	f.mu.Unlock()

	if hang {
		f.hung <- key

		<-ctx.Done()
	}

	if ctx.Err() != nil {
		return manifestreader.Object{}, ctx.Err()
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if queue := f.fails[key]; len(queue) > 0 {
		f.fails[key] = queue[1:]

		return manifestreader.Object{}, queue[0]
	}

	content, ok := f.objects[key]
	if !ok {
		return manifestreader.Object{}, manifestreader.ErrNotFound
	}

	if int64(len(content)) > limit {
		return manifestreader.Object{}, manifestreader.ErrTooLarge
	}

	if etag != "" && etag == f.etags[key] {
		return manifestreader.Object{}, manifestreader.ErrNotModified
	}

	return manifestreader.Object{Bytes: content, ETag: f.etags[key]}, nil
}

func (f *fakeStore) put(key string, content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.objects[key] = content
	f.etags[key] = etagOf(content)
}

func (f *fakeStore) remove(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.objects, key)
	delete(f.etags, key)
}

// failNext makes the next calls of key fail with errs, one each.
func (f *fakeStore) failNext(key string, errs ...error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.fails[key] = append(f.fails[key], errs...)
}

// takeCalls returns the calls since the last take, in a stable order: the manifest first, then the objects
// by key (concurrent downloads have no order of their own).
func (f *fakeStore) takeCalls() []call {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := f.calls
	f.calls = nil

	return sortCalls(out)
}

// sortCalls orders calls the way takeCalls does.
func sortCalls(calls []call) []call {
	slices.SortStableFunc(calls, func(left, right call) int {
		am, bm := left.Key == manifestKey, right.Key == manifestKey
		if am != bm {
			if am {
				return -1
			}

			return 1
		}

		return cmp.Compare(left.Key, right.Key)
	})

	return calls
}

func (f *fakeStore) maxConcurrent() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.maxInFlight
}

func (f *fakeStore) active() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.inFlight
}

// fakeSched is a Scheduler driven by the test: Wait blocks until the test ticks it.
type fakeSched struct {
	entered chan struct{}
	release chan struct{}
	waits   []time.Duration
	mu      sync.Mutex
}

func newFakeSched() *fakeSched {
	return &fakeSched{entered: make(chan struct{}, 1024), release: make(chan struct{})}
}

func (s *fakeSched) Wait(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.waits = append(s.waits, d)
	s.mu.Unlock()

	s.entered <- struct{}{}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.release:
		return nil
	}
}

func (s *fakeSched) waited() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]time.Duration(nil), s.waits...)
}

// rig runs a reader on the fake store and scheduler.
type rig struct {
	t      testing.TB
	store  *fakeStore
	sched  *fakeSched
	reader *manifestreader.Reader
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
}

// startRig runs the reader with the jitter off and waits for its first round to end.
func startRig(tb testing.TB, store *fakeStore, cfg *manifestreader.Config, opts ...manifestreader.Option) *rig {
	tb.Helper()

	own := *cfg
	own.DisableJitter = true

	rdr := launchRig(tb, store, &own, opts...)
	rdr.settle()

	return rdr
}

// launchRig runs the reader and returns at once; the caller waits for the rounds.
func launchRig(
	tb testing.TB,
	store manifestreader.ObjectStore,
	cfg *manifestreader.Config,
	opts ...manifestreader.Option,
) *rig {
	tb.Helper()

	own := *cfg
	if own.Connection == "" {
		own.Connection = connControl
	}

	if own.ManifestKey == "" {
		own.ManifestKey = manifestKey
	}

	switch own.Channel {
	case "":
		own.Channel = testChannel
	case anyChannel:
		own.Channel = ""
	}

	sched := newFakeSched()
	opts = append([]manifestreader.Option{manifestreader.WithScheduler(sched)}, opts...)

	reader, err := manifestreader.New(store, &own, opts...)
	require.NoError(tb, err)

	ctx, cancel := context.WithCancel(tb.Context())
	fake, _ := store.(*fakeStore)
	rdr := &rig{t: tb, store: fake, sched: sched, reader: reader, cancel: cancel, done: make(chan error, 1)}

	go func() { rdr.done <- reader.Run(ctx) }()

	tb.Cleanup(rdr.stop)

	return rdr
}

// settle waits until the reader finished a round and waits for the next tick.
func (r *rig) settle() {
	r.t.Helper()

	select {
	case <-r.sched.entered:
	case <-time.After(wait):
		require.FailNow(r.t, "the reader did not end its round")
	}
}

// round ticks the scheduler once and returns the calls of the round (nil when the store is not the fake).
func (r *rig) round() []call {
	r.t.Helper()

	if r.store != nil {
		r.store.takeCalls()
	}

	select {
	case r.sched.release <- struct{}{}:
	case <-time.After(wait):
		require.FailNow(r.t, "the reader is not waiting for a tick")
	}

	r.settle()

	if r.store == nil {
		return nil
	}

	return r.store.takeCalls()
}

// snapshot returns the snapshot waiting in the channel, or nil. It is called after a round: the reader
// published before it started to wait, so a snapshot of that round is already in the channel.
func (r *rig) snapshot() *manifestreader.Snapshot {
	r.t.Helper()

	select {
	case snap := <-r.reader.Snapshots():
		return snap
	default:
		return nil
	}
}

// stop ends the reader and waits for Run to return; it may be called more than once.
func (r *rig) stop() {
	r.once.Do(func() {
		r.cancel()

		select {
		case err := <-r.done:
			require.NoError(r.t, err)
		case <-time.After(wait):
			assert.Fail(r.t, "Run did not return after the context ended")
		}
	})
}

func (f *fakeStore) setHang(key string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.hang[key] = on
}

// callCount is the number of requests received and not taken yet.
func (f *fakeStore) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.calls)
}
