package flagruntime_test

import (
	"bytes"
	"cmp"
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
					got, err := eval.BoolVariationDetails(t.Context(),
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
	if !running {
		return
	}

	k.started.Add(1)

	k.mu.Lock()
	k.starts[connection]++
	k.mu.Unlock()
}

// onIdle is told after the connection is free: the passes that a tick started have then ended for good.
func (k *ticker) onIdle(connection string) { k.ends <- connection }

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

// tick is step, then waits for the end of every pass that the ticks started. The loop commits the state of a connection
// only on a tick, from the current bindings, so tick gives it one more tick to see the result of the passes.
func (k *ticker) tick(t *testing.T, n int) {
	t.Helper()

	k.joined(t, n)
	k.joined(t, 1)
}

// joined is step, then waits for the passes that it started.
func (k *ticker) joined(t *testing.T, n int) {
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

// awaitEnd waits for the end of a pass of the connection of the audiences.
func (k *ticker) awaitEnd(t *testing.T) {
	t.Helper()

	for {
		select {
		case got := <-k.ends:
			if got == connAudiences {
				k.started.Add(-1) // this pass is joined

				return
			}
		case <-time.After(5 * time.Second):
			require.Fail(t, "the pass did not end")
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

// all0 returns the messages without a test helper, for an assertion that nothing was logged.
func (l *logLines) all0() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return strings.Fields(strings.TrimSpace(l.buf.String()))
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
			Wait: rig.tick.wait, OnPass: rig.tick.onPass, OnIdle: rig.tick.onIdle, Tick: time.Second,
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

	got, err := r.eval.BoolVariationDetails(t.Context(),
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

	gates      map[string]chan struct{}
	deadlines  map[string][]time.Time
	asks       map[string]int
	versionErr map[string]error
	membersErr map[string]error
	hang       chan struct{}
	entered    chan string
	blocked    chan string
	mu         sync.Mutex
}

func newGateSource(source *segment.MemorySource, gated ...string) *gateSource {
	out := &gateSource{
		MemorySource: source, gates: map[string]chan struct{}{}, deadlines: map[string][]time.Time{},
		asks: map[string]int{}, entered: make(chan string, 256), blocked: make(chan string, 64),
		versionErr: map[string]error{}, membersErr: map[string]error{},
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

	select {
	case g.entered <- name:
	default:
	}

	g.mu.Lock()
	hang, failure := g.hang, g.versionErr[name]
	g.mu.Unlock()

	if hang != nil {
		<-hang // a store call that ignores its context
	}

	if failure != nil {
		return "", failure
	}

	version, err := g.MemorySource.Version(ctx, name)
	if err != nil {
		return "", fmt.Errorf("version: %w", err)
	}

	return version, nil
}

func (g *gateSource) Members(ctx context.Context, name string) iter.Seq2[string, error] {
	gate := g.gates[name]

	g.mu.Lock()
	failure := g.membersErr[name]
	g.mu.Unlock()

	return func(yield func(string, error) bool) {
		if failure != nil {
			yield("", failure)

			return
		}

		if gate != nil {
			select {
			case g.blocked <- name:
			default:
			}

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

// failVersion makes the version read of a set fail, nil makes it work again.
func (g *gateSource) failVersion(name string, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.versionErr[name] = err
}

// failMembers makes the pull of a set fail, nil makes it work again.
func (g *gateSource) failMembers(name string, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.membersErr[name] = err
}

// hangVersions makes every version read wait for release, whatever its context says; the returned function releases.
func (g *gateSource) hangVersions() func() {
	release := make(chan struct{})

	g.mu.Lock()
	g.hang = release
	g.mu.Unlock()

	return func() { close(release) }
}

// drainAsks forgets the asks that were signalled so far.
func (g *gateSource) drainAsks() {
	for {
		select {
		case <-g.entered:
		default:
			return
		}
	}
}

// unhang makes the version reads work again; the ones that wait are released by the function that hangVersions gave.
func (g *gateSource) unhang() {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.hang = nil
}

// awaitBlocked waits until the pull of the set entered its gate.
func (g *gateSource) awaitBlocked(t *testing.T, name string) {
	t.Helper()

	for {
		select {
		case got := <-g.blocked:
			if got == name {
				return
			}
		case <-time.After(5 * time.Second):
			require.Fail(t, "the pull did not reach its gate", name)
		}
	}
}

// awaitAsk waits until the source was asked for the version of the set.
func (g *gateSource) awaitAsk(t *testing.T) {
	t.Helper()

	for {
		select {
		case got := <-g.entered:
			if got == audienceSegmentName {
				return
			}
		case <-time.After(5 * time.Second):
			require.Fail(t, "the source was not asked")
		}
	}
}

func (g *gateSource) open(name string) { close(g.gates[name]) }

func (g *gateSource) firstDeadline(name string) time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.deadlines[name][0]
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

// pollRig is a runtime with the loop of the segments running on it.
type pollRig struct {
	holder *flagruntime.Holder
	eng    *flagruntime.Engine
	stop   func()
	rev    uint64
}

// submit publishes the content as the next revision and waits until the engine applied it.
func (r *pollRig) submit(t *testing.T, content *flagruntime.Content) {
	t.Helper()

	r.rev++
	r.eng.Submit(revision(content, r.rev))

	_, err := r.eng.Await(t.Context(), r.rev)
	require.NoError(t, err)
}

// startPoll builds a runtime on the doc and runs the loop on it; the cleanup stops both and joins them. A pull timeout
// of zero is the default.
func startPoll(
	t *testing.T, binder flagruntime.SegmentBinder, doc string, poll *flagruntime.SegmentPoll, pull time.Duration,
) *pollRig {
	t.Helper()

	stores := flagruntime.StoreTable{
		connAudiences: {Kind: kindRedis, Store: bothStore{}}, connOther: {Kind: kindRedis, Store: bothStore{}},
	}
	holder := flagruntime.NewHolder()
	eng := flagruntime.NewEngine(nil, newService(t), holder, flagruntime.WithSegmentStores(stores),
		flagruntime.WithSegments(&flagruntime.SegmentEnv{Binder: binder, PullTimeout: pull}))

	ctx, cancel := context.WithCancel(t.Context())

	var loops sync.WaitGroup

	rig := &pollRig{holder: holder, eng: eng, stop: func() {
		cancel()
		loops.Wait()
	}}

	t.Cleanup(rig.stop)

	loops.Go(func() { eng.Run(ctx) })
	rig.submit(t, viewContent(t, doc))

	loops.Go(func() { flagruntime.RunSegmentVersions(ctx, holder, poll) })

	return rig
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

	rig := startPoll(t, binder, doc, &flagruntime.SegmentPoll{
		Wait: tick.wait, OnPass: tick.onPass, OnIdle: tick.onIdle, Tick: time.Second,
		Jitter: func(limit time.Duration) time.Duration {
			spreads.Add(1)

			return limit / 2
		},
	}, 0)

	// the first tick arms both bindings with half a second of spread: none is due yet
	tick.step(t, 1)
	assert.EqualValues(t, 2, spreads.Load())
	assert.Zero(t, good.Calls(segment.OpVersion))

	// the second tick starts both passes: the hung store holds its own, the other connection is read
	tick.step(t, 1)
	tick.awaitEnd(t)
	assert.Equal(t, 1, good.Calls(segment.OpVersion))

	// the interval of the good connection passes (five seconds) while the other pass still runs: it is read again, the
	// hung one is not started twice
	tick.step(t, 5)
	tick.awaitEnd(t)
	assert.Equal(t, 2, good.Calls(segment.OpVersion))
	assert.Equal(t, 1, tick.passes(connOther), "the pass that still runs is not started twice")
	assert.Equal(t, 2, tick.passes(connAudiences))

	// the loop ends with its context and joins the pass that still runs: nothing is left behind
	rig.stop()

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
		audienceSegmentName: {memberAlice}, segTwo: {memberAlice},
	}), audienceSegmentName)
	binder := &twoBinder{sources: map[string]segment.SnapshotSource{connAudiences: source}}
	doc := audienceDoc(connAudiences, "") + "  - name: second\n    connection: audiences\n"
	tick := newTicker()

	rig := startPoll(
		t,
		binder,
		doc,
		&flagruntime.SegmentPoll{Wait: tick.wait, OnPass: tick.onPass, OnIdle: tick.onIdle, Tick: time.Second},
		0,
	)

	// the pull of the first set is held for twelve ticks, more than twice the interval: no second pass starts
	tick.step(t, 1)
	source.awaitBlocked(t, audienceSegmentName)
	tick.step(t, 11)
	assert.Equal(t, 1, source.asked(audienceSegmentName))
	assert.Zero(t, source.asked(segTwo))

	source.open(audienceSegmentName)
	tick.awaitEnd(t)

	assert.Equal(t, flagruntime.SegmentBindingsStatus{
		Live: 1, Connections: []flagruntime.ConnectionSnapshots{{Connection: connAudiences, Loaded: 2, Members: 2}},
	}, rig.holder.Load().SegmentBindings())

	assert.True(t, source.firstDeadline(segTwo).After(source.firstDeadline(audienceSegmentName)),
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
	thirdName  = "third"
	freshName  = "fresh"
	interval10 = "10s"
	manyNames  = 50
	ttl30      = "30s"
)

// manyDoc is one segments object: a static segment, a lookup segment and fifty snapshot segments.
type manyDoc struct {
	beta      string
	lookupTTL string
	// interval is the interval of the version of seg3; empty means no version.
	interval   string
	changedKey int
	extra      bool
	layout     bool // write layout: set on seg5
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

		if i == 5 && d.layout {
			doc.WriteString("    layout: set\n")
		}

		if i == 3 && d.interval != "" {
			doc.WriteString("    version: {key: v3, interval: " + d.interval + "}\n")
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

	base := manyDoc{beta: memberAlice, lookupTTL: ttl30, changedKey: -1, interval: interval10}

	tests := []struct {
		wantCold     []string
		name         string
		next         manyDoc
		wantMembers  int
		wantContains int
	}{
		{
			name:         "static segment edited",
			next:         manyDoc{beta: memberAlice + ", bob", lookupTTL: ttl30, changedKey: -1, interval: interval10},
			wantMembers:  manyNames,
			wantContains: 1,
		},
		{
			name:        "snapshot version interval changed",
			next:        manyDoc{beta: memberAlice, lookupTTL: ttl30, changedKey: -1, interval: "20s"},
			wantMembers: manyNames, wantContains: 1,
		},
		{
			name: "a layout written that is the default",
			next: manyDoc{
				beta: memberAlice, lookupTTL: ttl30, changedKey: -1, interval: interval10, layout: true,
			},
			wantMembers: manyNames, wantContains: 1,
		},
		{
			name:         "one snapshot definition changed",
			next:         manyDoc{beta: memberAlice, lookupTTL: ttl30, changedKey: 7, interval: interval10},
			wantCold:     []string{"seg7"},
			wantMembers:  manyNames + 1,
			wantContains: 1,
		},
		{
			name:         "lookup definition changed",
			next:         manyDoc{beta: memberAlice, lookupTTL: "1m", changedKey: -1, interval: interval10},
			wantMembers:  manyNames,
			wantContains: 2,
		},
		{
			name: "snapshot definition added",
			next: manyDoc{
				beta:       memberAlice,
				lookupTTL:  ttl30,
				changedKey: -1,
				extra:      true,
				interval:   interval10,
			},
			wantCold:     []string{freshName},
			wantMembers:  manyNames + 1,
			wantContains: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sets := map[string][]string{audienceSegmentName: {memberAlice}, freshName: {memberAlice}}
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

			got, err := eval.BoolVariationDetails(t.Context(), first.Runtime.FlagSets["a"], audienceFlag, ctx, false)
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

			names = append(names, freshName)

			for _, name := range names {
				state, status, ok := set.Segments.Catalog.Answer(name, memberAlice)
				if name == freshName && !tt.next.extra {
					assert.False(t, ok, name)

					continue
				}

				require.True(t, ok, name) // an unchanged definition still answers for its segment

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

			got, err = eval.BoolVariationDetails(t.Context(), set, audienceFlag, ctx, false)
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

const threeDoc = "  - name: second\n    connection: audiences\n  - name: third\n    connection: audiences\n"

func threeSets() map[string][]string {
	return map[string][]string{
		audienceSegmentName: {memberAlice}, segTwo: {memberAlice}, thirdName: {memberAlice},
	}
}

// A set whose refresh always runs into its deadline does not keep the sets after it from being refreshed: the pass goes
// on to the next holder, at every poll.
func TestSegmentPassContinuesPastATimedOutHolder(t *testing.T) {
	t.Parallel()

	source := newGateSource(segment.NewMemorySource(threeSets()), audienceSegmentName) // its gate is never opened
	binder := &twoBinder{sources: map[string]segment.SnapshotSource{connAudiences: source}}
	tick := newTicker()
	rig := startPoll(
		t,
		binder,
		audienceDoc(connAudiences, "")+threeDoc,
		&flagruntime.SegmentPoll{
			Wait:   tick.wait,
			OnPass: tick.onPass,
			OnIdle: tick.onIdle,
			Tick:   time.Second,
		},
		50*time.Millisecond,
	)

	tick.joined(t, 1)

	want := flagruntime.SegmentBindingsStatus{
		Live: 1, Connections: []flagruntime.ConnectionSnapshots{
			{Connection: connAudiences, Loaded: 2, NeverLoaded: 1, Members: 2},
		},
	}
	assert.Equal(t, want, rig.holder.Load().SegmentBindings())

	// the next poll (a second later, as one set is not loaded) tries the first one again and reaches the others again
	tick.joined(t, 2)
	assert.Equal(t, 2, source.asked(audienceSegmentName))
	assert.Equal(t, 2, source.asked(thirdName))
	assert.Equal(t, want, rig.holder.Load().SegmentBindings())
}

// A pass that hangs (a store call that ignores its context) is logged once, the connection counts as down, the sets
// that nothing refreshes answer STALE after three intervals, and the loop still ends with its context.
func TestSegmentPassThatHangsIsReported(t *testing.T) {
	t.Parallel()

	source := newGateSource(segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}}))
	binder := &twoBinder{sources: map[string]segment.SnapshotSource{connAudiences: source}}
	logs := &logLines{}
	reports := make(chan string, 16)
	tick := newTicker()
	rig := startPoll(t, binder, audienceDoc(connAudiences, ""), &flagruntime.SegmentPoll{
		Wait:         tick.wait,
		OnPass:       tick.onPass,
		OnIdle:       tick.onIdle,
		Tick:         time.Second,
		JoinTimeout:  50 * time.Millisecond,
		Logger:       slog.New(slog.NewJSONHandler(logs, nil)),
		OnConnection: func(name string, down bool) { reports <- fmt.Sprintf("%s down=%t", name, down) },
	}, 0)

	catalog := func() *segment.Catalog { return rig.holder.Load().FlagSets["a"].Segments.Catalog }

	tick.tick(t, 1)
	source.awaitAsk(t)

	_, status, _ := catalog().Answer(audienceSegmentName, memberAlice)
	assert.Equal(t, segment.StatusHealthy, status)

	var once sync.Once

	release := source.hangVersions()
	releaseOnce := func() { once.Do(release) }

	t.Cleanup(releaseOnce)

	// the poll that is due five seconds later hangs; more than twice the deadline of a refresh (one minute) later the
	// pass is hung
	tick.step(t, 5)
	source.awaitAsk(t)
	tick.step(t, 125)

	_, status, _ = catalog().Answer(audienceSegmentName, memberAlice)
	assert.Equal(t, segment.StatusStale, status, "a set that nothing refreshes goes stale")
	assert.Equal(t, flagruntime.SegmentBindingsStatus{
		Live: 1, Connections: []flagruntime.ConnectionSnapshots{{Connection: connAudiences, Stale: 1, Members: 1}},
	}, rig.holder.Load().SegmentBindings(), "/info and the metric count what the requests are told")
	assert.Equal(t, []string{connAudiences + " down=true"}, drainLines(reports))

	rig.stop() // returns after the join timeout, with the pass still stuck

	assert.Equal(t, []string{
		"a segment pass is hung: a store call does not return, its sets go stale",
		"a segment store fails: its segments are STORE_ERROR or STALE",
		"a segment pass did not end with the relay: a store call ignores its context",
	}, logs.all(t))

	releaseOnce()
}

func drainLines(ch chan string) []string {
	var out []string

	for {
		select {
		case line := <-ch:
			out = append(out, line)
		default:
			return out
		}
	}
}

// One set that the store cannot give is not a store that fails: it has a line of its own, once, and is counted in its
// state, and the connection stays up. A store that fails for every set is down, with the one line of the connection.
func TestSingleBrokenSetDoesNotFlipTheConnection(t *testing.T) {
	t.Parallel()

	const (
		failed = "a segment could not be refreshed"
		down   = "a segment store fails: its segments are STORE_ERROR or STALE"
	)

	wrongType := fmt.Errorf("members: %w", answeredError("a key of the wrong type"))

	tests := []struct {
		setup          func(*gateSource)
		name           string
		doc            string
		wantLines      []string
		wantConn       []string
		wantConnStates flagruntime.ConnectionSnapshots
	}{
		{
			name:      "a key of the wrong type",
			setup:     func(s *gateSource) { s.failMembers(segTwo, wrongType) },
			wantLines: []string{failed},
			wantConnStates: flagruntime.ConnectionSnapshots{
				Connection:  connAudiences,
				Loaded:      2,
				NeverLoaded: 1,
				Members:     2,
			},
		},
		{
			name: "the only set has a key of the wrong type", doc: audienceDoc(connAudiences, ""),
			setup:          func(s *gateSource) { s.failMembers(audienceSegmentName, wrongType) },
			wantLines:      []string{failed},
			wantConnStates: flagruntime.ConnectionSnapshots{Connection: connAudiences, NeverLoaded: 1},
		},
		{
			name:      "one set cannot be read for a transport failure",
			setup:     func(s *gateSource) { s.failVersion(segTwo, errStoreDown) },
			wantLines: []string{failed},
			wantConnStates: flagruntime.ConnectionSnapshots{
				Connection:  connAudiences,
				Loaded:      2,
				NeverLoaded: 1,
				Members:     2,
			},
		},
		{
			name: "every set fails for a transport failure",
			setup: func(s *gateSource) {
				for name := range threeSets() {
					s.failVersion(name, errStoreDown)
				}
			},
			wantLines:      []string{down},
			wantConn:       []string{connAudiences + " down=true"},
			wantConnStates: flagruntime.ConnectionSnapshots{Connection: connAudiences, NeverLoaded: 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := newGateSource(segment.NewMemorySource(threeSets()))
			tt.setup(source)

			binder := &twoBinder{sources: map[string]segment.SnapshotSource{connAudiences: source}}
			logs := &logLines{}
			reports := make(chan string, 16)
			tick := newTicker()
			doc := cmp.Or(tt.doc, audienceDoc(connAudiences, "")+threeDoc)
			rig := startPoll(t, binder, doc, &flagruntime.SegmentPoll{
				Wait: tick.wait, OnPass: tick.onPass, OnIdle: tick.onIdle, Tick: time.Second,
				Logger:       slog.New(slog.NewJSONHandler(logs, nil)),
				OnConnection: func(name string, down bool) { reports <- fmt.Sprintf("%s down=%t", name, down) },
			}, 0)

			tick.tick(t, 8) // the failing set is read again every second, and reported once

			assert.Equal(t, tt.wantLines, logs.all(t))
			assert.Equal(t, tt.wantConn, drainLines(reports))
			assert.Equal(t, flagruntime.SegmentBindingsStatus{
				Live: 1, Connections: []flagruntime.ConnectionSnapshots{tt.wantConnStates},
			}, rig.holder.Load().SegmentBindings())
		})
	}
}

// A new generation does not bring back a held set that its predecessor did not keep: a definition that went through
// another identity, another mode or no flag set starts empty, never with a set that may be hours old.
func TestHeldSetsAreNotRevived(t *testing.T) {
	t.Parallel()

	snap := audienceDoc(connAudiences, "")
	moved := snap + "    key: moved\n"

	tests := []struct {
		name       string
		docs       []string
		wantState  segment.State
		wantStatus segment.Status
	}{
		{
			name: "same identity again", docs: []string{snap, snap + "\n"},
			wantState: segment.Member, wantStatus: segment.StatusHealthy,
		},
		{
			name: "other identity and back", docs: []string{snap, moved, snap},
			wantState: segment.Unknown, wantStatus: segment.StatusStoreError,
		},
		{
			name: "lookup and back", docs: []string{snap, lookupDoc, snap},
			wantState: segment.Unknown, wantStatus: segment.StatusStoreError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
			builder := viewBuilder(t, &memBinder{source: source})
			now := time.Unix(1_000, 0)

			var last *flagruntime.Built

			for gen, doc := range tt.docs {
				last = buildAndCommit(t, builder, revisionContent(t, uint64(gen+1), doc))

				// every generation but the last is served long enough for the loop to refresh it
				if gen < len(tt.docs)-1 {
					require.NoError(t, last.Runtime.FlagSets["a"].Segments.View.RefreshVersions(t.Context(), now))
				}
			}

			// what a request sees first in the last generation, before any refresh of it
			state, status, ok := last.Runtime.FlagSets["a"].Segments.Catalog.Answer(audienceSegmentName, memberAlice)

			assert.True(t, ok)
			assert.Equal(t, tt.wantState, state)
			assert.Equal(t, tt.wantStatus, status)
		})
	}
}

// A binding that no flag set of the served runtime uses is not refreshed: when a flag set uses it again its sets answer
// STALE until a refresh worked, never healthy with what they held.
func TestUnreferencedBindingGoesStale(t *testing.T) {
	t.Parallel()

	source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
	tick := newTicker()
	slow := audienceDoc(connAudiences, "") + "    version: {key: v, interval: 30s}\n"
	rig := startPoll(t, &memBinder{source: source}, slow,
		&flagruntime.SegmentPoll{Wait: tick.wait, OnPass: tick.onPass, OnIdle: tick.onIdle, Tick: time.Second}, 0)

	answer := func() segment.Status {
		_, status, _ := rig.holder.Load().FlagSets["a"].Segments.Catalog.Answer(audienceSegmentName, memberAlice)

		return status
	}

	tick.tick(t, 2)
	assert.Equal(t, segment.StatusHealthy, answer())

	rig.submit(t, plainContent(t, 2, slow))
	tick.tick(t, 1) // the loop sees that nothing uses the binding any more
	rig.submit(t, revisionContent(t, 3, slow))

	assert.Equal(t, segment.StatusStale, answer(), "used again before any refresh")

	tick.tick(t, 1)
	assert.Equal(t, segment.StatusHealthy, answer(), "a refresh worked")
}

// plainContent is viewContent with a flag set that names no segments object.
func plainContent(tb testing.TB, gen uint64, doc string) *flagruntime.Content {
	tb.Helper()

	return content(gen, objs{
		"f1":      obj(flagruntime.KindFlags, []byte(viewFlagsYAML)),
		segObject: segObj(doc),
		setsName:  setsObj(tb, controlplane.FlagSet{Name: "a", Sources: src("f1"), APIKeys: []string{keyA}}),
	})
}

// The cache of the lookups of a connection that went away is not kept for a later generation that brings it back.
func TestRemovedConnectionLookupCacheIsNotKept(t *testing.T) {
	t.Parallel()

	source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
	builder := viewBuilder(t, &memBinder{source: source})
	eval := flagruntime.NewEvaluator(newService(t), nil)
	ctx := ffcontext.NewEvaluationContext(memberAlice)

	for gen, doc := range []string{lookupDoc, segHeader + "  - name: beta\n    members: [alice]\n", lookupDoc} {
		built := buildAndCommit(t, builder, revisionContent(t, uint64(gen+1), doc))

		_, err := eval.BoolVariationDetails(t.Context(), built.Runtime.FlagSets["a"], audienceFlag, ctx, false)
		require.NoError(t, err)
	}

	assert.Equal(t, 2, source.Calls(segment.OpContains), "the third generation starts with a cold cache")
}

// answeredError is a failure of a store that answered about one set.
type answeredError string

func (e answeredError) Error() string { return string(e) }

func (answeredError) StoreAnswered() bool { return true }

// lookupOnlyBinder binds a connection that has a provider and a versioner but cannot list members.
type lookupOnlyBinder struct{ source *segment.MemorySource }

func (b *lookupOnlyBinder) Bind(
	string, segment.MissingKey, []flagruntime.BoundDefinition,
) (*flagruntime.Binding, error) {
	return &flagruntime.Binding{Provider: b.source, Versioner: b.source}, nil
}

// A snapshot definition that cannot be served is refused alone: the lookups of its connection keep working.
func TestRefusedSnapshotDoesNotTakeTheLookupsDown(t *testing.T) {
	t.Parallel()

	source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
	doc := lookupDoc + "  - name: snap\n    connection: audiences\n"

	built := buildAndCommit(t, viewBuilder(t, &lookupOnlyBinder{source: source}), viewContent(t, doc))
	set := built.Runtime.FlagSets["a"]
	eval := flagruntime.NewEvaluator(newService(t), nil)

	require.NotNil(t, set.Segments.View)
	assert.True(t, set.Segments.View.Serves(audienceSegmentName))

	got, err := eval.BoolVariationDetails(
		t.Context(),
		set,
		audienceFlag,
		ffcontext.NewEvaluationContext(memberAlice),
		false,
	)
	require.NoError(t, err)
	assert.True(t, got.Value)
	assert.Equal(t, segment.StatusHealthy, got.SegmentStatus)

	_, _, served := set.Segments.Catalog.Answer("snap", memberAlice)
	assert.False(t, served, "the refused snapshot is answered by no one")
}

// The version read of a lookup has the budget of its own interval, not the shortest interval of the binding: a
// snapshot that is read every second does not cut the two seconds of a lookup.
func TestLookupVersionBudgetIsItsOwnInterval(t *testing.T) {
	t.Parallel()

	source := newGateSource(
		segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}, "snap": {memberAlice}}),
	)
	doc := segHeader + "  - name: audience\n    connection: audiences\n    mode: lookup\n" +
		"    version: {key: v, interval: 2s}\n" +
		"  - name: snap\n    connection: audiences\n    version: {key: w, interval: 1s}\n"
	binder := &gateBinder{source: source}
	tick := newTicker()

	startPoll(
		t,
		binder,
		doc,
		&flagruntime.SegmentPoll{Wait: tick.wait, OnPass: tick.onPass, OnIdle: tick.onIdle, Tick: time.Second},
		3*time.Second,
	)

	tick.joined(t, 1)

	// both reads happen in one pass, a moment apart: the deadlines differ by the difference of the budgets
	gap := source.firstDeadline("snap").Sub(source.firstDeadline(audienceSegmentName))

	assert.Greater(t, gap, 500*time.Millisecond)
	assert.Less(t, gap, 1500*time.Millisecond)
}

// gateBinder binds one gate source as provider, versioner and source of the members.
type gateBinder struct{ source *gateSource }

func (b *gateBinder) Bind(
	string, segment.MissingKey, []flagruntime.BoundDefinition,
) (*flagruntime.Binding, error) {
	return &flagruntime.Binding{Provider: b.source, Versioner: b.source, Source: b.source}, nil
}

// The end of a pass is told before the connection is free to start the next one: while the hook of the end of the first
// pass is held, the loop starts no second pass for the connection.
func TestPassEndIsToldBeforeTheConnectionIsFree(t *testing.T) {
	t.Parallel()

	source := segment.NewMemorySource(nil)
	source.Fail(segment.OpVersion, errStoreDown) // never loaded: a pass on every tick

	var (
		held    sync.Once
		freed   sync.Once
		reached = make(chan struct{})
		release = make(chan struct{})
		tick    = newTicker()
	)

	// idempotent, and deferred before the ticker advances: a failure cannot leave the hook waiting
	open := func() { freed.Do(func() { close(release) }) }

	defer open()

	t.Cleanup(open)

	record := func(connection string, running bool) {
		tick.onPass(connection, running)

		if !running {
			held.Do(func() {
				close(reached)
				<-release
			})
		}
	}

	startPoll(t, &memBinder{source: source}, audienceDoc(connAudiences, ""),
		&flagruntime.SegmentPoll{Wait: tick.wait, OnPass: record, OnIdle: tick.onIdle, Tick: time.Second}, 0)

	tick.step(t, 1)

	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		require.Fail(t, "the pass did not end")
	}

	tick.step(t, 3)
	assert.Equal(t, 1, tick.passes(connAudiences), "the connection is busy until its end was told")

	open()
}

// A reload that changes only a static segment hands the holders of the unchanged definitions to the new binding: the
// old binding retires, and the sets stay healthy, the first evaluation after the swap and after several ticks.
func TestCarriedHoldersStayHealthyAfterAStaticReload(t *testing.T) {
	t.Parallel()

	source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
	tick := newTicker()
	first := audienceDoc(connAudiences, "") + "    version: {key: v, interval: 30s}\n"
	rig := startPoll(t, &memBinder{source: source}, first,
		&flagruntime.SegmentPoll{Wait: tick.wait, OnPass: tick.onPass, OnIdle: tick.onIdle, Tick: time.Second}, 0)

	answer := func() (segment.State, segment.Status) {
		state, status, _ := rig.holder.Load().FlagSets["a"].Segments.Catalog.Answer(audienceSegmentName, memberAlice)

		return state, status
	}

	tick.tick(t, 2)

	changed := strings.Replace(first, "members: [alice]", "members: [alice, bob]", 1)
	require.NotEqual(t, first, changed)

	rig.submit(t, revisionContent(t, 5, changed))

	state, status := answer()
	assert.Equal(t, segment.Member, state)
	assert.Equal(t, segment.StatusHealthy, status, "the first evaluation after the swap")

	tick.tick(t, 4)

	state, status = answer()
	assert.Equal(t, segment.Member, state)
	assert.Equal(t, segment.StatusHealthy, status, "after several ticks: the old binding retired, the holder did not")
}

// The binding wakes at the earliest pending deadline of its lookups and its snapshot sets: a two second lookup and a
// three second snapshot are read at ticks 1, 3, 5 and 1, 4, 7.
func TestBindingWakesAtTheEarliestDeadline(t *testing.T) {
	t.Parallel()

	source := newGateSource(
		segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}, "snap": {memberAlice}}),
	)
	doc := segHeader + "  - name: audience\n    connection: audiences\n    mode: lookup\n" +
		"    version: {key: v, interval: 2s}\n" +
		"  - name: snap\n    connection: audiences\n    version: {key: w, interval: 3s}\n"
	tick := newTicker()

	startPoll(t, &gateBinder{source: source}, doc,
		&flagruntime.SegmentPoll{Wait: tick.wait, OnPass: tick.onPass, OnIdle: tick.onIdle, Tick: time.Second}, 0)

	snapReads := make([]int, 0, 7)

	for range 7 {
		tick.joined(t, 1)

		snapReads = append(snapReads, source.asked("snap"))
	}

	assert.Equal(t, []int{1, 1, 1, 2, 2, 2, 3}, snapReads)
}

// The freshness of a set comes from its own refresh, not from the start of the pass: the sets that load after a slow
// first one are not stale at the next tick.
func TestFreshnessIsMeasuredFromTheHoldersOwnRefresh(t *testing.T) {
	t.Parallel()

	source := newGateSource(segment.NewMemorySource(threeSets()), audienceSegmentName)
	binder := &twoBinder{sources: map[string]segment.SnapshotSource{connAudiences: source}}
	tick := newTicker()
	rig := startPoll(t, binder, audienceDoc(connAudiences, "")+threeDoc,
		&flagruntime.SegmentPoll{Wait: tick.wait, OnPass: tick.onPass, OnIdle: tick.onIdle, Tick: time.Second}, 0)

	tick.step(t, 1)
	source.awaitBlocked(t, audienceSegmentName)
	tick.step(t, 20) // the first set takes twenty seconds, longer than three intervals
	source.open(audienceSegmentName)
	tick.awaitEnd(t)

	// the next pass is held in its first call, so that nothing refreshes the sets again before they are asked
	release := source.hangVersions()
	t.Cleanup(release)

	source.drainAsks()
	tick.step(t, 1)
	source.awaitAsk(t)

	catalog := rig.holder.Load().FlagSets["a"].Segments.Catalog

	for _, name := range []string{audienceSegmentName, segTwo, thirdName} {
		state, status, ok := catalog.Answer(name, memberAlice)

		assert.True(t, ok, name)
		assert.Equal(t, segment.Member, state, name)
		assert.Equal(t, segment.StatusHealthy, status, name)
	}
}

// A pass of serial operations is hung only when one operation stops making progress: three pulls of forty five seconds,
// each within its own deadline, are not a hung connection.
func TestSerialPullsWithinTheirDeadlinesAreNotHung(t *testing.T) {
	t.Parallel()

	source := newGateSource(segment.NewMemorySource(threeSets()), audienceSegmentName, segTwo, thirdName)
	binder := &twoBinder{sources: map[string]segment.SnapshotSource{connAudiences: source}}
	logs := &logLines{}
	reports := make(chan string, 16)
	tick := newTicker()

	startPoll(t, binder, audienceDoc(connAudiences, "")+threeDoc, &flagruntime.SegmentPoll{
		Wait: tick.wait, OnPass: tick.onPass, OnIdle: tick.onIdle, Tick: time.Second,
		Logger:       slog.New(slog.NewJSONHandler(logs, nil)),
		OnConnection: func(name string, down bool) { reports <- fmt.Sprintf("%s down=%t", name, down) },
	}, 0)

	tick.step(t, 1)

	for _, name := range []string{audienceSegmentName, segTwo, thirdName} {
		source.awaitBlocked(t, name)
		tick.step(t, 45)
		source.open(name)
	}

	tick.awaitEnd(t)
	tick.joined(t, 1)

	assert.Empty(t, logs.all0(), "no hung pass and no store down")
	assert.Empty(t, drainLines(reports))
}

// A second hang is reported too, even when the pass that replaces the first starts as soon as the first ended.
func TestEachHungPassIsReported(t *testing.T) {
	t.Parallel()

	source := newGateSource(segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}}))
	binder := &twoBinder{sources: map[string]segment.SnapshotSource{connAudiences: source}}
	logs := &logLines{}
	tick := newTicker()

	startPoll(t, binder, audienceDoc(connAudiences, ""), &flagruntime.SegmentPoll{
		Wait: tick.wait, OnPass: tick.onPass, OnIdle: tick.onIdle, Tick: time.Second,
		Logger: slog.New(slog.NewJSONHandler(logs, nil)),
	}, 0)

	tick.joined(t, 1)
	source.awaitAsk(t)

	for range 2 {
		release := source.hangVersions()

		tick.step(t, 6)
		source.awaitAsk(t)
		tick.step(t, 125)

		source.unhang()
		release()
		tick.awaitEnd(t)
	}

	var hung int

	for _, line := range logs.all(t) {
		if strings.HasPrefix(line, "a segment pass is hung") {
			hung++
		}
	}

	assert.Equal(t, 2, hung)
}

// The old binding of a reload that changed a static segment retires while the new binding has not refreshed yet: the
// sets that the new binding took over answer healthy in that time, because a live binding still serves them.
func TestRetiringBindingLeavesTheTakenOverHoldersAlone(t *testing.T) {
	t.Parallel()

	source := newGateSource(segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}}))
	binder := &twoBinder{sources: map[string]segment.SnapshotSource{connAudiences: source}}
	first := audienceDoc(connAudiences, "") + "    version: {key: v, interval: 30s}\n"
	tick := newTicker()
	rig := startPoll(t, binder, first,
		&flagruntime.SegmentPoll{Wait: tick.wait, OnPass: tick.onPass, OnIdle: tick.onIdle, Tick: time.Second}, 0)

	tick.tick(t, 2)

	release := source.hangVersions() // the first pass of the new binding is held
	t.Cleanup(release)

	source.drainAsks()
	rig.submit(t, revisionContent(t, 5, strings.Replace(first, "members: [alice]", "members: [alice, bob]", 1)))
	tick.step(t, 1) // the old binding retires, the new one starts its pass
	source.awaitAsk(t)

	state, status, ok := rig.holder.Load().FlagSets["a"].Segments.Catalog.Answer(audienceSegmentName, memberAlice)

	assert.True(t, ok)
	assert.Equal(t, segment.Member, state)
	assert.Equal(t, segment.StatusHealthy, status)
}
