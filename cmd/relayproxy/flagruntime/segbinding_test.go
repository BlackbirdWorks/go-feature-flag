package flagruntime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

const (
	reportError = "error"
	reportDown  = "audiences down=true"
	reportUp    = "audiences down=false"
	connOther   = "other"
)

const bindingDoc = lookupDoc + "    version: {key: v}\n"

// manyContent is n flag sets, each with a key of its own, that use one segments object.
func manyContent(tb testing.TB, gen uint64, n int, flags, doc string) *flagruntime.Content {
	tb.Helper()

	sets := make([]controlplane.FlagSet, n)
	for i := range sets {
		sets[i] = controlplane.FlagSet{
			Name: fmt.Sprintf("s%d", i), Sources: src("f1"), SegmentSources: []string{segObject},
			APIKeys: []string{fmt.Sprintf("key-%d", i)},
		}
	}

	return content(gen, objs{
		"f1": obj(flagruntime.KindFlags, []byte(flags)), segObject: segObj(doc), setsName: setsObj(tb, sets...),
	})
}

// One binding serves every flag set and every generation that uses a segments object: one resolver, one cache, one
// poll of the version key, however many flag sets there are; a reload that changes only the flags keeps the cache warm;
// a change of the object gives a new binding and the old one is no longer used.
func TestSharedBinding(t *testing.T) {
	t.Parallel()

	moreFlags := viewFlagsYAML + "extra-flag:\n  variations: {a: 1}\n  defaultRule: {name: d, variation: a}\n"

	tests := []struct {
		name             string
		reload           string
		sets             int
		wantBindings     int
		wantBinds        int32
		wantVersionReads int
		wantContains     int
	}{
		{name: "shared", sets: 1000, wantBindings: 1, wantBinds: 1, wantVersionReads: 1, wantContains: 1},
		{
			name: "flags only", sets: 50, reload: moreFlags,
			wantBindings: 1, wantBinds: 1, wantVersionReads: 1, wantContains: 1,
		},
		{
			name: "object change", sets: 50, reload: "object",
			wantBindings: 1, wantBinds: 2, wantVersionReads: 2, wantContains: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
			binder := &memBinder{source: source}
			builder := viewBuilder(t, binder)
			eval := flagruntime.NewEvaluator(newService(t), nil)
			now := time.Unix(1_000, 0)

			step := func(built *flagruntime.Built) {
				current := built.Runtime

				for _, set := range current.FlagSets {
					require.NoError(t, set.Segments.View.RefreshVersions(t.Context(), now))
				}

				for _, name := range []string{"s0", "s1", fmt.Sprintf("s%d", tt.sets-1)} {
					got, err := eval.BoolVariationDetails(
						current.FlagSets[name], "audience-flag", ffcontext.NewEvaluationContext(memberAlice), false)
					require.NoError(t, err)
					assert.True(t, got.Value)
				}
			}

			last := buildAndCommit(t, builder, manyContent(t, 1, tt.sets, viewFlagsYAML, bindingDoc))
			step(last)

			switch tt.reload {
			case "":
			case "object":
				moved := bindingDoc + "    key: moved\n"
				last = buildAndCommit(t, builder, manyContent(t, 2, tt.sets, viewFlagsYAML, moved))
				step(last)
			default:
				last = buildAndCommit(t, builder, manyContent(t, 2, tt.sets, tt.reload, bindingDoc))
				step(last)
			}

			assert.Equal(t, tt.wantBindings, last.Runtime.SegmentBindings().Live)
			assert.Equal(t, tt.wantBinds, binder.binds.Load())
			assert.Equal(t, tt.wantVersionReads, source.Calls(segment.OpVersion))
			assert.Equal(t, tt.wantContains, source.Calls(segment.OpContains))
		})
	}
}

// ticker drives RunSegmentVersions one tick at a time. The loop never waits for the passes of the connections, so the
// ticker follows them through the OnPass hook: tick joins the passes that the ticks started, step does not.
type ticker struct {
	ticks   chan struct{}
	entered chan struct{}
	ends    chan string
	starts  map[string]int
	started atomic.Int32
	mu      sync.Mutex
}

func newTicker() *ticker {
	return &ticker{
		ticks: make(chan struct{}), entered: make(chan struct{}, 1), ends: make(chan string, 1024),
		starts: map[string]int{},
	}
}

func (k *ticker) onPass(connection string, running bool) {
	if running {
		k.started.Add(1)

		k.mu.Lock()
		k.starts[connection]++
		k.mu.Unlock()

		return
	}

	k.ends <- connection
}

func (k *ticker) wait(ctx context.Context, _ time.Duration) error {
	select {
	case k.entered <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("wait: %w", ctx.Err())
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("wait: %w", ctx.Err())
	case <-k.ticks:
		return nil
	}
}

// step gives the loop n ticks and returns when it waits for the next one.
func (k *ticker) step(t *testing.T, n int) {
	t.Helper()

	for range n {
		select {
		case <-k.entered:
		case <-time.After(5 * time.Second):
			require.Fail(t, "the loop did not wait")
		}

		select {
		case k.ticks <- struct{}{}:
		case <-time.After(5 * time.Second):
			require.Fail(t, "the loop did not take the tick")
		}
	}

	select {
	case <-k.entered:
	case <-time.After(5 * time.Second):
		require.Fail(t, "the loop did not end the tick")
	}

	// the loop waits for the next tick: leave the signal for the next call
	k.entered <- struct{}{}
}

// tick is step, then waits for the end of every pass that the ticks started.
func (k *ticker) tick(t *testing.T, n int) {
	t.Helper()

	k.step(t, n)

	for range k.started.Swap(0) {
		select {
		case <-k.ends:
		case <-time.After(5 * time.Second):
			require.Fail(t, "a pass did not end")
		}
	}
}

// passes is the number of passes that the loop started for the connection.
func (k *ticker) passes(connection string) int {
	k.mu.Lock()
	defer k.mu.Unlock()

	return k.starts[connection]
}

// awaitEnd waits for the end of a pass of the connection.
func (k *ticker) awaitEnd(t *testing.T, connection string) {
	t.Helper()

	for {
		select {
		case got := <-k.ends:
			if got == connection {
				return
			}
		case <-time.After(5 * time.Second):
			require.Fail(t, "the pass did not end", connection)
		}
	}
}

// logLines is a buffer of the JSON lines that the loop logs.
type logLines struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (l *logLines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.Write(p)
}

// all returns the messages of the lines, in order.
func (l *logLines) all(t *testing.T) []string {
	t.Helper()

	l.mu.Lock()
	defer l.mu.Unlock()

	var out []string

	for line := range strings.SplitSeq(strings.TrimSpace(l.buf.String()), "\n") {
		var rec struct {
			Msg string `json:"msg"`
		}

		require.NoError(t, json.Unmarshal([]byte(line), &rec))

		out = append(out, rec.Msg)
	}

	return out
}

type snapshotRig struct {
	logs    *logLines
	source  *segment.MemorySource
	holder  *flagruntime.Holder
	tick    *ticker
	eval    *flagruntime.Evaluator
	reports chan string
}

func startSnapshotRig(t *testing.T, doc string) *snapshotRig {
	t.Helper()

	rig := &snapshotRig{
		source: segment.NewMemorySource(nil), holder: flagruntime.NewHolder(), tick: newTicker(),
		reports: make(chan string, 64), logs: &logLines{},
	}

	stores := flagruntime.StoreTable{connAudiences: {Kind: kindRedis, Store: bothStore{}}}
	eng := flagruntime.NewEngine(nil, newService(t), rig.holder, flagruntime.WithSegmentStores(stores),
		flagruntime.WithSegments(&flagruntime.SegmentEnv{Binder: &memBinder{source: rig.source}}))

	ctx, cancel := context.WithCancel(t.Context())

	var loops sync.WaitGroup

	t.Cleanup(func() {
		cancel()
		loops.Wait()
	})

	loops.Go(func() { eng.Run(ctx) })

	eng.Submit(revision(viewContent(t, doc), 1))

	_, err := eng.Await(ctx, 1)
	require.NoError(t, err)

	loops.Go(func() {
		flagruntime.RunSegmentVersions(ctx, rig.holder, &flagruntime.SegmentPoll{
			Wait: rig.tick.wait, OnPass: rig.tick.onPass, Tick: time.Second,
			Logger:       slog.New(slog.NewJSONHandler(rig.logs, nil)),
			OnError:      func(error) { rig.reports <- reportError },
			OnConnection: func(name string, down bool) { rig.reports <- fmt.Sprintf("%s down=%t", name, down) },
		})
	})

	rig.eval = flagruntime.NewEvaluator(newService(t), nil)

	return rig
}

func (r *snapshotRig) answer(t *testing.T) (bool, segment.Status) {
	t.Helper()

	got, err := r.eval.BoolVariationDetails(
		r.holder.Load().FlagSets["a"], "audience-flag", ffcontext.NewEvaluationContext(memberAlice), false)
	require.NoError(t, err)

	return got.Value, got.SegmentStatus
}

func (r *snapshotRig) drained() []string {
	var out []string

	for {
		select {
		case line := <-r.reports:
			out = append(out, line)
		default:
			return out
		}
	}
}

// A snapshot segment is refreshed off the request path and answered from the held set: nothing is held before the
// first load (not a member, STORE_ERROR), a load makes it healthy, a failed refresh keeps the set and says STALE, a
// store that is back says healthy again, and a deleted set is not a member and healthy. The loop is the only reader.
func TestRunSegmentVersionsSnapshot(t *testing.T) {
	t.Parallel()

	rig := startSnapshotRig(t, audienceDoc(connAudiences, ""))

	steps := []struct {
		act         func()
		name        string
		wantReports []string
		ticks       int
		wantStatus  segment.Status
		wantMember  bool
	}{
		{
			name:        "before the first load",
			ticks:       1,
			act:         func() { rig.source.Fail(segment.OpVersion, errStoreDown) },
			wantStatus:  segment.StatusStoreError,
			wantMember:  false,
			wantReports: []string{reportError, reportDown},
		},
		{
			name: "first load", ticks: 12, act: func() {
				rig.source.Fail(segment.OpVersion, nil)
				rig.source.Set(audienceSegmentName, []string{memberAlice})
			},
			wantStatus: segment.StatusHealthy, wantMember: true, wantReports: []string{reportUp},
		},
		{
			name:        "store down",
			ticks:       12,
			act:         func() { rig.source.Fail(segment.OpVersion, errStoreDown) },
			wantStatus:  segment.StatusStale,
			wantMember:  true,
			wantReports: []string{reportError, reportDown},
		},
		{
			name: "store back", ticks: 12, act: func() { rig.source.Fail(segment.OpVersion, nil) },
			wantStatus: segment.StatusHealthy, wantMember: true, wantReports: []string{reportUp},
		},
		{
			name: "set deleted", ticks: 12, act: func() { rig.source.Delete(audienceSegmentName) },
			wantStatus: segment.StatusHealthy, wantMember: false,
		},
	}

	for _, step := range steps {
		step.act()
		rig.tick.tick(t, step.ticks)

		member, status := rig.answer(t)
		assert.Equal(t, step.wantMember, member, step.name)
		assert.Equal(t, step.wantStatus, status, step.name)
		assert.ElementsMatch(t, step.wantReports, rig.drained(), step.name)
		assert.Zero(t, rig.source.Calls(segment.OpContains), step.name)
	}

	// one line for each change of the state of the connection, never one for each failed poll (that is debug detail)
	const (
		down = "a segment store fails: its segments are STORE_ERROR or STALE"
		up   = "a segment store works again"
	)

	assert.Equal(t, []string{down, up, down, up}, rig.logs.all(t))
}

// gateSource is a source whose members of a set are held until the test opens its gate, and that remembers the deadline
// of the context of each version read and each pull.
type gateSource struct {
	*segment.MemorySource

	gates     map[string]chan struct{}
	deadlines map[string][]time.Time
	asks      map[string]int
	mu        sync.Mutex
}

func newGateSource(source *segment.MemorySource, gated ...string) *gateSource {
	out := &gateSource{
		MemorySource: source, gates: map[string]chan struct{}{}, deadlines: map[string][]time.Time{},
		asks: map[string]int{},
	}

	for _, name := range gated {
		out.gates[name] = make(chan struct{})
	}

	return out
}

func (g *gateSource) Version(ctx context.Context, name string) (string, error) {
	deadline, _ := ctx.Deadline()

	g.mu.Lock()
	g.deadlines[name] = append(g.deadlines[name], deadline)
	g.asks[name]++
	g.mu.Unlock()

	version, err := g.MemorySource.Version(ctx, name)
	if err != nil {
		return "", fmt.Errorf("version: %w", err)
	}

	return version, nil
}

func (g *gateSource) Members(ctx context.Context, name string) iter.Seq2[string, error] {
	gate := g.gates[name]

	return func(yield func(string, error) bool) {
		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				yield("", fmt.Errorf("members: %w", ctx.Err()))

				return
			}
		}

		for id, err := range g.MemorySource.Members(ctx, name) {
			if !yield(id, err) {
				return
			}
		}
	}
}

func (g *gateSource) open(name string) { close(g.gates[name]) }

func (g *gateSource) deadline(name string, n int) time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.deadlines[name][n]
}

func (g *gateSource) asked(name string) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.asks[name]
}

type twoBinder struct {
	sources map[string]segment.SnapshotSource
}

func (b *twoBinder) Bind(
	connection string, _ segment.MissingKey, _ []flagruntime.BoundDefinition,
) (*flagruntime.Binding, error) {
	source, ok := b.sources[connection]
	if !ok {
		return nil, errNoConnection
	}

	return &flagruntime.Binding{Source: source}, nil
}

// startPoll builds a runtime on the doc and runs the loop on it; the cleanup stops both and joins them.
func startPoll(
	t *testing.T, binder flagruntime.SegmentBinder, doc string, poll *flagruntime.SegmentPoll,
) (*flagruntime.Holder, func()) {
	t.Helper()

	stores := flagruntime.StoreTable{
		connAudiences: {Kind: kindRedis, Store: bothStore{}}, connOther: {Kind: kindRedis, Store: bothStore{}},
	}
	holder := flagruntime.NewHolder()
	eng := flagruntime.NewEngine(nil, newService(t), holder, flagruntime.WithSegmentStores(stores),
		flagruntime.WithSegments(&flagruntime.SegmentEnv{Binder: binder}))

	ctx, cancel := context.WithCancel(t.Context())

	var loops sync.WaitGroup

	stop := func() {
		cancel()
		loops.Wait()
	}

	t.Cleanup(stop)

	loops.Go(func() { eng.Run(ctx) })
	eng.Submit(revision(viewContent(t, doc), 1))

	_, err := eng.Await(ctx, 1)
	require.NoError(t, err)

	loops.Go(func() { flagruntime.RunSegmentVersions(ctx, holder, poll) })

	return holder, stop
}

// The first poll of a binding is spread by the injected random source, and a store that hangs delays the poll of no
// other connection: the pass of each connection is its own, the loop never waits for it and starts no second one.
func TestSegmentPollPasses(t *testing.T) {
	t.Parallel()

	good := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
	hung := newGateSource(segment.NewMemorySource(map[string][]string{"stuck": {memberAlice}}), "stuck")
	doc := audienceDoc(connAudiences, "") + "  - name: stuck\n    connection: other\n"
	binder := &twoBinder{sources: map[string]segment.SnapshotSource{connAudiences: good, connOther: hung}}

	var (
		tick    = newTicker()
		spreads atomic.Int32
	)

	_, stop := startPoll(t, binder, doc, &flagruntime.SegmentPoll{
		Wait: tick.wait, OnPass: tick.onPass, Tick: time.Second,
		Jitter: func(limit time.Duration) time.Duration {
			spreads.Add(1)

			return limit / 2
		},
	})

	// the first tick arms both bindings with half a second of spread: none is due yet
	tick.tick(t, 1)
	assert.EqualValues(t, 2, spreads.Load())
	assert.Zero(t, good.Calls(segment.OpVersion))

	// the second tick starts both passes: the hung store holds its own, the other connection is read
	tick.step(t, 1)
	tick.awaitEnd(t, connAudiences)
	assert.Equal(t, 1, good.Calls(segment.OpVersion))

	// the interval of the good connection passes (five seconds) while the other pass still runs: it is read again, the
	// hung one is not started twice
	tick.step(t, 5)
	tick.awaitEnd(t, connAudiences)
	assert.Equal(t, 2, good.Calls(segment.OpVersion))
	assert.Equal(t, 1, tick.passes(connOther), "the pass that still runs is not started twice")
	assert.Equal(t, 2, tick.passes(connAudiences))

	// the loop ends with its context and joins the pass that still runs: nothing is left behind
	stop()

	var ended []string

	for len(tick.ends) > 0 {
		ended = append(ended, <-tick.ends)
	}

	assert.Contains(t, ended, connOther)
}

// Each holder has a deadline of its own: a set that takes longer than the poll interval to pull loads, and the next set
// of the connection does not share its deadline.
func TestSegmentPullDeadlines(t *testing.T) {
	t.Parallel()

	source := newGateSource(segment.NewMemorySource(map[string][]string{
		audienceSegmentName: {memberAlice}, "second": {memberAlice},
	}), audienceSegmentName)
	binder := &twoBinder{sources: map[string]segment.SnapshotSource{connAudiences: source}}
	doc := audienceDoc(connAudiences, "") + "  - name: second\n    connection: audiences\n"
	tick := newTicker()

	holder, _ := startPoll(
		t,
		binder,
		doc,
		&flagruntime.SegmentPoll{Wait: tick.wait, OnPass: tick.onPass, Tick: time.Second},
	)

	// the pull of the first set is held for twelve ticks, more than twice the interval: no second pass starts
	tick.step(t, 12)
	assert.Equal(t, 1, source.asked(audienceSegmentName))
	assert.Zero(t, source.asked("second"))

	source.open(audienceSegmentName)
	tick.awaitEnd(t, connAudiences)

	assert.Equal(t, flagruntime.SegmentBindingsStatus{
		Live: 1, Connections: []flagruntime.ConnectionSnapshots{{Connection: connAudiences, Loaded: 2, Members: 2}},
	}, holder.Load().SegmentBindings())

	assert.True(t, source.deadline("second", 0).After(source.deadline(audienceSegmentName, 0)),
		"the second set has a deadline of its own, started after the first")
}

func TestRandomSpread(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		limit     time.Duration
		wantZero  bool
		wantBelow time.Duration
	}{
		{name: "no limit", limit: 0, wantZero: true},
		{name: "negative", limit: -time.Second, wantZero: true},
		{name: "a second", limit: time.Second, wantBelow: time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			distinct := map[int64]struct{}{}

			for range 50 {
				got := flagruntime.RandomSpread(tt.limit)
				distinct[got.Nanoseconds()] = struct{}{}

				if tt.wantZero {
					assert.Zero(t, got)
				} else {
					assert.GreaterOrEqual(t, got, time.Duration(0))
					assert.Less(t, got, tt.wantBelow)
				}
			}

			assert.Equal(t, tt.wantZero, len(distinct) == 1)
		})
	}
}

// noSourceBinder binds a connection that can look members up but not list them.
type noSourceBinder struct{ source *segment.MemorySource }

func (b *noSourceBinder) Bind(
	string, segment.MissingKey, []flagruntime.BoundDefinition,
) (*flagruntime.Binding, error) {
	return &flagruntime.Binding{Provider: b.source}, nil
}

// SegmentBindings counts the held sets of each connection by state and the members of all, the view counts them
// healthy or failing, and a snapshot definition on a store that cannot list members is not served.
func TestSegmentBindings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setup          func(*segment.MemorySource)
		name           string
		doc            string
		wantStates     flagruntime.ConnectionSnapshots
		wantHealthy    int
		wantStoreError int
		poll2          bool
	}{
		{
			name: "loaded", doc: audienceDoc(connAudiences, ""), wantHealthy: 1,
			setup:      func(s *segment.MemorySource) { s.Set(audienceSegmentName, []string{memberAlice, memberBob}) },
			wantStates: flagruntime.ConnectionSnapshots{Connection: connAudiences, Loaded: 1, Members: 2},
		},
		{
			name: "never loaded", doc: audienceDoc(connAudiences, ""), wantStoreError: 1,
			setup:      func(s *segment.MemorySource) { s.Fail(segment.OpVersion, errStoreDown) },
			wantStates: flagruntime.ConnectionSnapshots{Connection: connAudiences, NeverLoaded: 1},
		},
		{
			name: "missing", doc: audienceDoc(connAudiences, ""), wantHealthy: 1,
			setup:      func(*segment.MemorySource) {},
			wantStates: flagruntime.ConnectionSnapshots{Connection: connAudiences, Missing: 1},
		},
		{
			name: "stale", doc: audienceDoc(connAudiences, ""), wantStoreError: 1, poll2: true,
			setup:      func(s *segment.MemorySource) { s.Set(audienceSegmentName, []string{memberAlice}) },
			wantStates: flagruntime.ConnectionSnapshots{Connection: connAudiences, Stale: 1, Members: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := segment.NewMemorySource(nil)
			tt.setup(source)

			built := buildAndCommit(t, viewBuilder(t, &memBinder{source: source}), viewContent(t, tt.doc))
			set := built.Runtime.FlagSets["a"]
			start := time.Unix(1_000, 0)

			_ = set.Segments.View.RefreshVersions(t.Context(), start)

			if tt.poll2 {
				source.Fail(segment.OpVersion, errStoreDown)

				_ = set.Segments.View.RefreshVersions(t.Context(), start.Add(time.Minute))
			}

			healthy, failing := set.Segments.View.Health()
			assert.Equal(t, tt.wantHealthy, healthy)
			assert.Equal(t, tt.wantStoreError, failing)
			assert.Equal(t, []string{connAudiences}, set.Segments.View.Connections())
			assert.Equal(t, flagruntime.SegmentBindingsStatus{
				Live: 1, Connections: []flagruntime.ConnectionSnapshots{tt.wantStates},
			}, built.Runtime.SegmentBindings())
		})
	}
}

// sourceOnlyBinder binds a connection that can list members but not look them up.
type sourceOnlyBinder struct{ source *segment.MemorySource }

func (b *sourceOnlyBinder) Bind(
	string, segment.MissingKey, []flagruntime.BoundDefinition,
) (*flagruntime.Binding, error) {
	return &flagruntime.Binding{Source: b.source}, nil
}

// A definition is served only when its store gives what its mode needs: a snapshot a source of the members, a lookup a
// provider. Without it the flag set has no view.
func TestBindingNeedsWhatTheModeNeeds(t *testing.T) {
	t.Parallel()

	source := segment.NewMemorySource(nil)

	tests := []struct {
		binder flagruntime.SegmentBinder
		name   string
		doc    string
	}{
		{
			name: "snapshot without a source", binder: &noSourceBinder{source: source},
			doc: audienceDoc(connAudiences, ""),
		},
		{name: "lookup without a provider", binder: &sourceOnlyBinder{source: source}, doc: lookupDoc},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			set := viewSet(t, tt.binder, tt.doc)

			assert.Nil(t, set.Segments.View)
		})
	}
}

// A store that fails the lookups of a segment is reported down once. The breaker cools down on the clock of the
// process, which the tests freeze, so the way back is proven by the snapshot test.
func TestRunSegmentVersionsLookupConnection(t *testing.T) {
	t.Parallel()

	rig := startSnapshotRig(t, lookupDoc+"    version: {key: v, interval: 100ms}\n")

	rig.source.Set(audienceSegmentName, []string{memberAlice})
	rig.source.Fail(segment.OpContains, errStoreDown)

	// consecutive failed calls open the breaker of the segment
	var (
		member bool
		status segment.Status
	)

	for range 10 {
		member, status = rig.answer(t)
	}

	assert.False(t, member)
	assert.Equal(t, segment.StatusStoreError, status)

	rig.tick.tick(t, 1)
	assert.Equal(t, []string{reportDown}, rig.drained())
}

// A segment that is missing in the store under missingKey: unknown is an answer, not a failure: it counts as loaded,
// answers healthy (the failure policy makes it not a member), is read at its interval and writes no line.
func TestMissingSegmentWithUnknownPolicyIsHealthy(t *testing.T) {
	t.Parallel()

	rig := startSnapshotRig(t, audienceDoc(connAudiences, "")+"    missingKey: unknown\n")

	rig.tick.tick(t, 4)

	member, status := rig.answer(t)
	assert.False(t, member)
	assert.Equal(t, segment.StatusHealthy, status)
	assert.Equal(t, flagruntime.SegmentBindingsStatus{
		Live: 1, Connections: []flagruntime.ConnectionSnapshots{{Connection: connAudiences, Missing: 1}},
	}, rig.holder.Load().SegmentBindings())
	assert.Equal(t, 1, rig.source.Calls(segment.OpVersion), "polled at its interval, not every second")
	assert.Empty(t, rig.drained())
	assert.Empty(t, strings.TrimSpace(rig.logs.buf.String()), "a store that works writes no line")
}

const (
	manyNames = 50
	ttl30     = "30s"
)

// manyDoc is one segments object: a static segment, a lookup segment and fifty snapshot segments.
type manyDoc struct {
	beta       string
	lookupTTL  string
	changedKey int
	extra      bool
}

func (d manyDoc) String() string {
	var doc strings.Builder

	doc.WriteString(segHeader + "  - name: beta\n    members: [" + d.beta + "]\n")
	doc.WriteString("  - name: audience\n    connection: audiences\n    mode: lookup\n")
	doc.WriteString("    cache: {ttl: " + d.lookupTTL + "}\n")

	for i := range manyNames {
		fmt.Fprintf(&doc, "  - name: seg%d\n    connection: audiences\n", i)

		if i == d.changedKey {
			doc.WriteString("    key: moved\n")
		}
	}

	if d.extra {
		doc.WriteString("  - name: fresh\n    connection: audiences\n")
	}

	return doc.String()
}

// A new generation of a segments object takes over, for each definition that did not change, the holder with its state
// and the cache of the lookups: only a changed or new definition starts empty, so a change of one definition (or of a
// static segment of the object) does not turn the others into not a member.
func TestBindingsCarryOverUnchangedDefinitions(t *testing.T) {
	t.Parallel()

	base := manyDoc{beta: memberAlice, lookupTTL: ttl30, changedKey: -1}

	tests := []struct {
		name         string
		wantCold     []string
		next         manyDoc
		wantMembers  int
		wantContains int
	}{
		{
			name: "static segment edited", next: manyDoc{beta: memberAlice + ", bob", lookupTTL: ttl30, changedKey: -1},
			wantMembers: manyNames, wantContains: 1,
		},
		{
			name: "one snapshot definition changed", next: manyDoc{beta: memberAlice, lookupTTL: ttl30, changedKey: 7},
			wantCold: []string{"seg7"}, wantMembers: manyNames + 1, wantContains: 1,
		},
		{
			name: "lookup definition changed", next: manyDoc{beta: memberAlice, lookupTTL: "1m", changedKey: -1},
			wantMembers: manyNames, wantContains: 2,
		},
		{
			name:         "snapshot definition added",
			next:         manyDoc{beta: memberAlice, lookupTTL: ttl30, changedKey: -1, extra: true},
			wantCold:     []string{"fresh"},
			wantMembers:  manyNames + 1,
			wantContains: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sets := map[string][]string{audienceSegmentName: {memberAlice}, "fresh": {memberAlice}}
			for i := range manyNames {
				sets[fmt.Sprintf("seg%d", i)] = []string{memberAlice}
			}

			source := segment.NewMemorySource(sets)
			builder := viewBuilder(t, &memBinder{source: source})
			eval := flagruntime.NewEvaluator(newService(t), nil)
			now := time.Unix(1_000, 0)
			ctx := ffcontext.NewEvaluationContext(memberAlice)

			first := buildAndCommit(t, builder, viewContent(t, base.String()))
			require.NoError(t, first.Runtime.FlagSets["a"].Segments.View.RefreshVersions(t.Context(), now))

			got, err := eval.BoolVariationDetails(first.Runtime.FlagSets["a"], audienceFlag, ctx, false)
			require.NoError(t, err)
			require.True(t, got.Value)
			require.Equal(t, manyNames, source.Calls(segment.OpMembers))

			// the swap: before any refresh of the new generation, what did not change answers from memory
			next := buildAndCommit(t, builder, revisionContent(t, 2, tt.next.String()))
			set := next.Runtime.FlagSets["a"]

			var cold []string

			names := make([]string, 0, manyNames+1)
			for i := range manyNames {
				names = append(names, fmt.Sprintf("seg%d", i))
			}

			names = append(names, "fresh")

			for _, name := range names {
				state, status, ok := set.Segments.Catalog.Answer(name, memberAlice)
				if !ok {
					continue
				}

				if status == segment.StatusStoreError {
					cold = append(cold, name)

					continue
				}

				assert.Equal(t, segment.Member, state, name)
				assert.Equal(t, segment.StatusHealthy, status, name)
			}

			assert.Equal(t, tt.wantCold, cold)
			assert.Equal(t, manyNames, source.Calls(segment.OpMembers), "no pull for the swap itself")

			require.NoError(t, set.Segments.View.RefreshVersions(t.Context(), now.Add(time.Minute)))

			got, err = eval.BoolVariationDetails(set, audienceFlag, ctx, false)
			require.NoError(t, err)
			assert.True(t, got.Value)
			assert.Equal(t, tt.wantMembers, source.Calls(segment.OpMembers))
			assert.Equal(t, tt.wantContains, source.Calls(segment.OpContains))
			assert.Equal(
				t,
				1,
				next.Runtime.SegmentBindings().Live,
				"one binding: the old holders are not kept beside the new",
			)
		})
	}
}

// revisionContent is viewContent for a generation.
func revisionContent(tb testing.TB, gen uint64, doc string) *flagruntime.Content {
	tb.Helper()

	out := viewContent(tb, doc)
	out.Generation = gen

	return out
}

// A store that fails every set of a connection writes one line when the connection goes down, not one for each set.
func TestStoreDownWritesOneLineForAllSets(t *testing.T) {
	t.Parallel()

	rig := startSnapshotRig(t, manyDoc{beta: memberAlice, lookupTTL: ttl30, changedKey: -1}.String())

	rig.source.Fail(segment.OpVersion, errStoreDown)
	rig.tick.tick(t, 3)

	assert.Equal(t, []string{"a segment store fails: its segments are STORE_ERROR or STALE"}, rig.logs.all(t))
	assert.Contains(t, rig.drained(), reportError, "the failures of the sets reach OnError, joined")
}
