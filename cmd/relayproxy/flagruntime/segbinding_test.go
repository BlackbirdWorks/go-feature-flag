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

// ticker drives RunSegmentVersions one tick at a time: tick returns when the loop is waiting again, so the pass of
// the tick is over.
type ticker struct {
	ticks   chan struct{}
	entered chan struct{}
}

func newTicker() *ticker { return &ticker{ticks: make(chan struct{}), entered: make(chan struct{}, 1)} }

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

func (k *ticker) tick(t *testing.T, n int) {
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
		require.Fail(t, "the pass did not end")
	}

	// the loop waits for the next tick: leave the signal for the next call
	k.entered <- struct{}{}
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
			Wait: rig.tick.wait, Tick: time.Second, Logger: slog.New(slog.NewJSONHandler(rig.logs, nil)),
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
		assert.Equal(t, step.wantReports, rig.drained(), step.name)
		assert.Zero(t, rig.source.Calls(segment.OpContains), step.name)
	}

	// one line for each new failure and for each change of the state of the connection, never one for each poll
	const (
		failed = "a segment could not be refreshed"
		down   = "a segment store fails: its segments are STORE_ERROR or STALE"
		up     = "a segment store works again"
	)

	assert.Equal(t, []string{failed, down, up, failed, down, up}, rig.logs.all(t))
}

// hungSource never answers a version until its context ends.
type hungSource struct {
	*segment.MemorySource

	asked chan struct{}
	once  sync.Once
}

func (h *hungSource) Version(ctx context.Context, _ string) (string, error) {
	h.once.Do(func() { close(h.asked) })
	<-ctx.Done()

	return "", fmt.Errorf("hung: %w", ctx.Err())
}

func (h *hungSource) Members(ctx context.Context, name string) iter.Seq2[string, error] {
	return h.MemorySource.Members(ctx, name)
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

// The first poll of a binding is spread by the injected random source, and a store that hangs delays the poll of no
// other connection: the pass of each connection is its own.
func TestSegmentPollPasses(t *testing.T) {
	t.Parallel()

	good := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
	hung := &hungSource{MemorySource: segment.NewMemorySource(nil), asked: make(chan struct{})}
	stores := flagruntime.StoreTable{
		connAudiences: {Kind: kindRedis, Store: bothStore{}}, connOther: {Kind: kindRedis, Store: bothStore{}},
	}
	doc := audienceDoc(
		connAudiences,
		"",
	) + "  - name: stuck\n    connection: other\n    version: {key: v, interval: 100ms}\n"
	binder := &twoBinder{sources: map[string]segment.SnapshotSource{connAudiences: good, connOther: hung}}

	holder := flagruntime.NewHolder()
	eng := flagruntime.NewEngine(nil, newService(t), holder, flagruntime.WithSegmentStores(stores),
		flagruntime.WithSegments(&flagruntime.SegmentEnv{Binder: binder}))

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

	var (
		tick    = newTicker()
		spreads atomic.Int32
	)

	loops.Go(func() {
		flagruntime.RunSegmentVersions(ctx, holder, &flagruntime.SegmentPoll{
			Wait: tick.wait, Tick: time.Second,
			Jitter: func(limit time.Duration) time.Duration {
				spreads.Add(1)

				return limit / 2
			},
		})
	})

	// the first tick arms both bindings with half of their spread (half a second at most): none is due yet
	tick.tick(t, 1)
	assert.EqualValues(t, 2, spreads.Load())
	assert.Zero(t, good.Calls(segment.OpVersion))

	// the second tick reads both: the hung store holds its own pass (bounded by its interval) and the other is read
	tick.tick(t, 1)
	assert.Equal(t, 1, good.Calls(segment.OpVersion))

	select {
	case <-hung.asked:
	case <-time.After(5 * time.Second):
		assert.Fail(t, "the hung store was not read")
	}
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
