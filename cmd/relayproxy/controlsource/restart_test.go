package controlsource_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/controlsource"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

const (
	restartConn     = "control"
	restartChannel  = "stable"
	restartManifest = "manifests/stable.yaml"
	restartFlag     = "f"
)

var errStoreUnreachable = errors.New("the store is unreachable")

// storeCall is one request that reached the store.
type storeCall struct {
	Key  string
	ETag string
}

// memStore is the object store of the reader: bytes by key, with ETags, a call log and failures to inject.
type memStore struct {
	objects map[string][]byte
	fails   map[string][]error
	calls   []storeCall
	mu      sync.Mutex
}

func newMemStore() *memStore {
	return &memStore{objects: map[string][]byte{}, fails: map[string][]error{}}
}

func etagOf(data []byte) string {
	sum := sha256.Sum256(data)

	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func (m *memStore) Get(_ context.Context, req manifestreader.GetRequest) (manifestreader.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, storeCall{Key: req.Key, ETag: req.ETag})

	if queue := m.fails[req.Key]; len(queue) > 0 {
		m.fails[req.Key] = queue[1:]

		return manifestreader.Object{}, queue[0]
	}

	data, ok := m.objects[req.Key]
	if !ok {
		return manifestreader.Object{}, manifestreader.ErrNotFound
	}

	if req.ETag != "" && req.ETag == etagOf(data) {
		return manifestreader.Object{}, manifestreader.ErrNotModified
	}

	return manifestreader.Object{Bytes: data, ETag: etagOf(data)}, nil
}

func (m *memStore) put(key string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.objects[key] = data
}

func (m *memStore) remove(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.objects, key)
}

func (m *memStore) failNext(key string, errs ...error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fails[key] = append(m.fails[key], errs...)
}

func (m *memStore) take() []storeCall {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := m.calls
	m.calls = nil

	return out
}

func (m *memStore) etag(key string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	return etagOf(m.objects[key])
}

// listing is what a publisher lists in a manifest: flags objects by name, and the flag sets.
type listing struct {
	flags map[string][]byte
	sets  []controlplane.FlagSet
	gen   uint64
}

// publish writes the objects of the listing, then its manifest, and returns the key of each flags object.
func (l *listing) publish(t *testing.T, store *memStore) map[string]string {
	t.Helper()

	manifest := &controlplane.Manifest{
		APIVersion: controlplane.APIVersion, Kind: controlplane.DocumentKindManifest, Channel: restartChannel,
		Generation: l.gen,
	}
	keys := map[string]string{}
	add := func(name, kind string, data []byte) {
		sum := controlplane.ChecksumOf(data)
		key, err := controlplane.ObjectKey(sum, controlplane.FormatYAML)
		require.NoError(t, err)

		keys[name] = key

		store.put(key, data)

		manifest.Objects = append(manifest.Objects, controlplane.ManifestObject{
			Name: name, Kind: kind, Connection: restartConn, Key: key, Format: controlplane.FormatYAML,
			Checksum: sum, Size: int64(len(data)),
		})
	}

	doc, err := controlplane.MarshalFlagSet(&controlplane.FlagSetDocument{FlagSets: l.sets})
	require.NoError(t, err)

	add("sets", controlplane.KindFlagSets, doc)

	for name, data := range l.flags {
		add(name, controlplane.KindFlags, data)
	}

	data, err := controlplane.MarshalManifest(manifest)
	require.NoError(t, err)

	store.put(restartManifest, data)

	return keys
}

// process is one run of the relay's control side: a reader with its state directory, the source, the engine.
type process struct {
	t      *testing.T
	store  *memStore
	sched  *tickScheduler
	reader *manifestreader.Reader
	source *controlsource.Source
	holder *flagruntime.Holder
	eval   *flagruntime.Evaluator
	rounds chan flagruntime.RoundResult
	state  *statedir.Store
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func startProcess(t *testing.T, store *memStore, dir string) *process {
	t.Helper()

	state, err := statedir.New(statedir.Config{Dir: dir})
	require.NoError(t, err)

	sched := &tickScheduler{entered: make(chan struct{}, 16), release: make(chan struct{})}
	reader, err := manifestreader.New(store,
		&manifestreader.Config{
			ManifestKey: restartManifest, Connection: restartConn, Channel: restartChannel, DisableJitter: true,
			FetchConcurrency: 1, DisableFetchSpread: true,
		},
		manifestreader.WithScheduler(sched), manifestreader.WithJitter(func() float64 { return 0 }),
		manifestreader.WithHooks(state.Hooks()))
	require.NoError(t, err)

	svc := flagdef.NewService()
	t.Cleanup(svc.Close)

	proc := &process{
		t: t, store: store, sched: sched, reader: reader, holder: flagruntime.NewHolder(), state: state,
		rounds: make(chan flagruntime.RoundResult, 64), eval: flagruntime.NewEvaluator(svc, nil),
	}
	engine := flagruntime.NewEngine(nil, svc, proc.holder, flagruntime.WithHooks(flagruntime.Hooks{
		AfterRound: func(res flagruntime.RoundResult) { proc.rounds <- res },
	}))
	proc.source = controlsource.New(reader, engine, proc.holder, nil)

	ctx, cancel := context.WithCancel(t.Context())
	proc.cancel = cancel

	proc.wg.Go(func() { state.Run(ctx) })
	proc.wg.Go(func() { assert.NoError(t, reader.Run(ctx)) })
	proc.wg.Go(func() { engine.Run(ctx) })
	proc.wg.Go(func() { proc.source.Run(ctx) })

	t.Cleanup(proc.stop)

	return proc
}

// stop ends the process and releases its state directory; it may be called twice.
func (p *process) stop() {
	p.cancel()
	p.wg.Wait()
	assert.NoError(p.t, p.state.Close())
}

func (p *process) settle() {
	p.t.Helper()

	timer := time.NewTimer(waitBound)
	defer timer.Stop()

	select {
	case <-p.sched.entered:
	case <-timer.C:
		require.FailNow(p.t, "the reader did not end its round")
	}
}

func (p *process) poll() {
	p.t.Helper()

	timer := time.NewTimer(waitBound)
	defer timer.Stop()

	select {
	case p.sched.release <- struct{}{}:
	case <-timer.C:
		require.FailNow(p.t, "the reader is not waiting for a tick")
	}

	p.settle()
}

func (p *process) nextRound() flagruntime.RoundResult {
	p.t.Helper()

	timer := time.NewTimer(waitBound)
	defer timer.Stop()

	select {
	case res := <-p.rounds:
		return res
	case <-timer.C:
		require.FailNow(p.t, "the engine ended no round")
	}

	return flagruntime.RoundResult{}
}

func (p *process) flush() {
	p.t.Helper()

	ctx, cancel := context.WithTimeout(p.t.Context(), waitBound)
	defer cancel()

	require.NoError(p.t, p.state.Flush(ctx))
}

func (p *process) value(key string) (any, flagruntime.Route) {
	set, route := p.holder.Load().Route(key)
	if set == nil {
		return nil, route
	}

	res, _ := p.eval.RawVariation(context.Background(), set, restartFlag, ffcontext.NewEvaluationContext("user"), nil)

	return res.Value, route
}

// A relay that restarts while the store is down serves what it served before: the manifest reader restores its saved
// state, in which a name that was stale carries its last good bytes; the source hands them to a fresh engine, which
// reads them as the last good content of a name it never loaded. The key evaluates the old value, the flag set is
// degraded, and the restart cost exactly one request to the store: the conditional poll of the manifest, which fails.
func TestRestart_StateRestoredWithTheStoreDownServesTheLastKnownGood(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	dir := filepath.Join(t.TempDir(), "state")
	plan := &listing{
		flags: map[string][]byte{"flags-a": flagsYAML(1, restartFlag), "flags-b": flagsYAML(2, restartFlag)},
		sets: []controlplane.FlagSet{
			{Name: "one", Sources: []string{"flags-a"}, APIKeys: []string{"key-one"}},
			{Name: "two", Sources: []string{"flags-b"}, APIKeys: []string{"key-two"}},
		},
		gen: 1,
	}
	plan.publish(t, store)

	// the first run: generation 1 is served
	first := startProcess(t, store, dir)
	first.settle()
	assert.Equal(t, flagruntime.OutcomeApplied, first.nextRound().Outcome)

	got, route := first.value("key-one")
	assert.Equal(t, flagruntime.RouteFlagSet, route)
	assert.EqualValues(t, 1, got)

	// generation 2 lists a new flags-a whose object is not there: flags-a is stale with its generation 1 bytes
	plan.flags["flags-a"] = flagsYAML(10, restartFlag)
	plan.gen = 2
	keys := plan.publish(t, store)
	store.remove(keys["flags-a"])

	first.poll()
	assert.Equal(t, flagruntime.OutcomeApplied, first.nextRound().Outcome)

	state := first.holder.Load().Status
	assert.Equal(t, flagruntime.ObjectStale, state.Objects["flags-a"].State)

	first.flush()
	first.stop()
	store.take()

	// the restart, with the store down
	etag := store.etag(restartManifest)
	store.failNext(restartManifest, errStoreUnreachable)

	second := startProcess(t, store, dir)
	second.settle()
	assert.Equal(t, flagruntime.OutcomeApplied, second.nextRound().Outcome)

	got, route = second.value("key-one")
	assert.Equal(t, flagruntime.RouteFlagSet, route)
	assert.EqualValues(t, 1, got, "the last known good content of the stale object, adopted by a fresh engine")

	got, _ = second.value("key-two")
	assert.EqualValues(t, 2, got)

	run := second.holder.Load()
	assert.Equal(t, flagruntime.StateDegraded, run.FlagSets["one"].State)
	assert.Equal(t, flagruntime.StateServing, run.FlagSets["two"].State)
	assert.Equal(t, flagruntime.ObjectStale, run.Status.Objects["flags-a"].State)
	assert.EqualValues(t, 2, run.Generation)

	assert.Equal(t, []storeCall{{Key: restartManifest, ETag: etag}}, store.take(),
		"the whole cost of the restart: one conditional poll of the manifest, which failed")
	assert.Equal(t, manifestreader.PollError, second.source.Status().Reader.Poll.Result)
	assert.Equal(t, manifestreader.RestoreRestored, second.source.Status().Reader.State.Restore)
}
