package flagruntime_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

var (
	errStoreDown    = errors.New("the store is down")
	errNoConnection = errors.New("no such connection")
)

// memBinder binds every definition of the connection "audiences" to one in-memory source.
type memBinder struct {
	source  *segment.MemorySource
	missing []segment.MissingKey
	mu      sync.Mutex
	binds   atomic.Int32
	failed  bool
}

func (b *memBinder) Bind(
	connection string, missing segment.MissingKey, _ []flagruntime.BoundDefinition,
) (*flagruntime.Binding, error) {
	b.binds.Add(1)

	b.mu.Lock()
	b.missing = append(b.missing, missing)
	b.mu.Unlock()

	if b.failed || connection != connAudiences {
		return nil, errNoConnection
	}

	return &flagruntime.Binding{Provider: b.source, Versioner: b.source, Source: b.source}, nil
}

const (
	viewFlagsYAML = `
audience-flag:
  variations: {on: true, off: false}
  targeting:
    - {name: audience, query: 'targetingKey in segment("audience")', variation: on}
  defaultRule: {name: default, variation: off}
twin-flag:
  variations: {on: true, off: false}
  targeting:
    - {name: audience, query: 'targetingKey in segment("audience")', variation: on}
  defaultRule: {name: default, variation: off}
typed-flag:
  variations: {on: true, off: false}
  targeting:
    - {name: audience, query: 'user.id in segment("audience")', variation: on}
  defaultRule: {name: default, variation: off}
either-flag:
  variations: {on: true, off: false}
  targeting:
    - name: audience
      query: 'targetingKey in segment("audience") or
        targetingKey in segment("strict")'
      variation: on
  defaultRule: {name: default, variation: off}
tenant-flag:
  variations: {on: true, off: false}
  targeting:
    - {name: tenant, query: 'tenant in segment("audience")', variation: on}
  defaultRule: {name: default, variation: off}
tenant-twin:
  variations: {on: true, off: false}
  targeting:
    - {name: tenant, query: 'tenant in segment("audience")', variation: on}
  defaultRule: {name: default, variation: off}
ghost-flag:
  variations: {on: true, off: false}
  targeting:
    - {name: ghost, query: 'targetingKey in segment("ghost")', variation: on}
  defaultRule: {name: default, variation: off}
plain-flag:
  variations: {on: true, off: false}
  defaultRule: {name: default, variation: on}
`
	lookupDoc = segHeader + "  - name: audience\n    connection: audiences\n    mode: lookup\n"
)

func viewBuilder(tb testing.TB, binder flagruntime.SegmentBinder) *flagruntime.Builder {
	tb.Helper()

	return viewBuilderWith(tb, &flagruntime.SegmentEnv{Binder: binder})
}

func viewBuilderWith(tb testing.TB, env *flagruntime.SegmentEnv) *flagruntime.Builder {
	tb.Helper()

	stores := flagruntime.StoreTable{connAudiences: {Kind: kindRedis, Store: bothStore{}}}

	return flagruntime.NewBuilder(newService(tb), flagruntime.BuilderOptions{SegmentStores: stores, Segments: env})
}

func viewContent(tb testing.TB, doc string) *flagruntime.Content {
	tb.Helper()

	return content(1, objs{
		"f1":      obj(flagruntime.KindFlags, []byte(viewFlagsYAML)),
		segObject: segObj(doc),
		setsName: setsObj(tb, controlplane.FlagSet{
			Name: "a", Sources: src("f1"), SegmentSources: []string{segObject}, APIKeys: []string{keyA},
		}),
	})
}

func viewSet(t *testing.T, binder flagruntime.SegmentBinder, doc string) *flagruntime.FlagSet {
	t.Helper()

	return buildAndCommit(t, viewBuilder(t, binder), viewContent(t, doc)).Runtime.FlagSets["a"]
}

// A dynamic segment in the lookup mode is served: the members of the store target, the answer of a healthy store
// carries no status, a store that fails answers not a member with STORE_ERROR, and the flag is never cacheable.
func TestDynamicSegmentIsServed(t *testing.T) {
	t.Parallel()

	eval := flagruntime.NewEvaluator(newService(t), nil)

	tests := []struct {
		fail            error
		name            string
		target          string
		wantStatus      segment.Status
		wantValue       bool
		wantCacheable   bool
		wantStoreCalled int
	}{
		{name: "member", target: memberAlice, wantValue: true, wantStatus: segment.StatusHealthy, wantStoreCalled: 1},
		{name: "not a member", target: memberBob, wantStatus: segment.StatusHealthy, wantStoreCalled: 1},
		{
			name: "store error", target: memberAlice, fail: errStoreDown, wantStatus: segment.StatusStoreError,
			wantStoreCalled: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
			source.Fail(segment.OpContains, tt.fail)

			set := viewSet(t, &memBinder{source: source}, lookupDoc)
			got, err := eval.BoolVariationDetails(t.Context(),
				set,
				"audience-flag",
				ffcontext.NewEvaluationContext(tt.target),
				false,
			)

			require.NoError(t, err)
			assert.Equal(t, tt.wantValue, got.Value)
			assert.Equal(t, tt.wantStatus, got.SegmentStatus)
			assert.Equal(t, tt.wantCacheable, got.Cacheable)
			assert.Equal(t, tt.wantStoreCalled, source.Calls(segment.OpContains))
		})
	}
}

// The memberships of an evaluation are fetched in one batch before the rules run: one call to the store for one
// flag, and one for all the flags of a bulk evaluation. A flag without dynamic segments costs no call.
func TestDynamicSegmentsAreFetchedInOneBatch(t *testing.T) {
	t.Parallel()

	eval := flagruntime.NewEvaluator(newService(t), nil)

	tests := []struct {
		run       func(set *flagruntime.FlagSet) any
		name      string
		wantCalls int
	}{
		{
			name: "one flag", wantCalls: 1,
			run: func(set *flagruntime.FlagSet) any {
				got, _ := eval.BoolVariationDetails(t.Context(),
					set,
					"audience-flag",
					ffcontext.NewEvaluationContext(memberAlice),
					false,
				)

				return got.Value
			},
		},
		{
			name: "plain flag", wantCalls: 0,
			run: func(set *flagruntime.FlagSet) any {
				got, _ := eval.BoolVariationDetails(t.Context(),
					set,
					"plain-flag",
					ffcontext.NewEvaluationContext(memberAlice),
					false,
				)

				return got.Value
			},
		},
		{
			name: "all flags", wantCalls: 1,
			run: func(set *flagruntime.FlagSet) any {
				states := eval.AllFlagsState(t.Context(), set, ffcontext.NewEvaluationContext(memberAlice))

				return states.GetFlags()["twin-flag"].Value
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})

			// no cache: a second call to the store would show, where a cached answer would hide it
			noCache := true
			env := &flagruntime.SegmentEnv{
				Binder: &memBinder{source: source}, Resolver: &bootstrap.SegmentResolver{
					Default: bootstrap.SegmentTuning{NoCache: &noCache},
				},
			}
			set := buildAndCommit(t, viewBuilderWith(t, env), viewContent(t, lookupDoc)).Runtime.FlagSets["a"]

			assert.Equal(t, true, tt.run(set))
			assert.Equal(t, tt.wantCalls, source.Calls(segment.OpContains))
		})
	}
}

// The view is part of the generation: a build that leaves the flag set unchanged carries the same view, a definition
// that changes gets a new one, and a binder that fails leaves the segment unserved with a problem on the flag set.
func TestSegmentViewFollowsTheFlagSet(t *testing.T) {
	t.Parallel()

	source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
	binder := &memBinder{source: source}
	builder := viewBuilder(t, binder)

	first := buildAndCommit(t, builder, viewContent(t, lookupDoc))
	next := viewContent(t, lookupDoc)
	next.Generation = 2
	same := buildAndCommit(t, builder, next)

	require.NotNil(t, first.Runtime.FlagSets["a"].Segments.View)
	assert.Same(t, first.Runtime.FlagSets["a"].Segments.View, same.Runtime.FlagSets["a"].Segments.View)
	assert.Equal(t, int32(1), binder.binds.Load(), "an unchanged flag set binds nothing")

	changed := viewContent(t, lookupDoc+"    cache: {ttl: 1m}\n")
	changed.Generation = 3
	other := buildAndCommit(t, builder, changed)

	assert.NotSame(t, first.Runtime.FlagSets["a"].Segments.View, other.Runtime.FlagSets["a"].Segments.View)
	assert.Equal(t, int32(2), binder.binds.Load())

	broken := viewSet(t, &memBinder{source: source, failed: true}, lookupDoc)
	assert.Nil(t, broken.Segments.View)
	assert.Equal(t, flagruntime.StateDegraded, broken.State)
}

// RefreshVersions polls the version key of a segment when its interval is due: a change of the version is a cache
// miss, so the next lookup asks the store again; a poll that is not due reads nothing.
func TestSegmentViewRefreshVersions(t *testing.T) {
	t.Parallel()

	const doc = lookupDoc + "    version: {key: audience-version}\n"

	start := time.Unix(1_000, 0)
	eval := flagruntime.NewEvaluator(newService(t), nil)
	source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
	set := viewSet(t, &memBinder{source: source}, doc)
	view := set.Segments.View
	ctx := ffcontext.NewEvaluationContext(memberBob)

	tests := []struct {
		name         string
		at           time.Duration
		change       bool
		wantValue    bool
		wantVersions int
	}{
		{name: "first poll", at: 0, wantVersions: 1},
		{name: "not due", at: time.Second, change: true, wantVersions: 1},
		{name: "due, new members", at: 6 * time.Second, wantVersions: 2, wantValue: true},
	}

	// the rows are the steps of one history: they run in order, on one store
	for _, tt := range tests {
		if tt.change {
			source.Set(audienceSegmentName, []string{memberAlice, memberBob})
		}

		require.NoError(t, view.RefreshVersions(t.Context(), start.Add(tt.at)), tt.name)

		got, err := eval.BoolVariationDetails(t.Context(), set, "audience-flag", ctx, false)
		require.NoError(t, err, tt.name)
		assert.Equal(t, tt.wantValue, got.Value, tt.name)
		assert.Equal(t, tt.wantVersions, source.Calls(segment.OpVersion), tt.name)
	}
}

// The watcher polls a view when its interval is due on the time it has waited, and stops when its context ends. The
// test drives the waits one by one: a tick of one second, a default interval of five.
func TestRunSegmentVersions(t *testing.T) {
	t.Parallel()

	source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
	holder := flagruntime.NewHolder()
	eng := flagruntime.NewEngine(nil, newService(t), holder,
		flagruntime.WithSegmentStores(flagruntime.StoreTable{connAudiences: {Kind: kindRedis, Store: bothStore{}}}),
		flagruntime.WithSegments(&flagruntime.SegmentEnv{Binder: &memBinder{source: source}}))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	engineDone := make(chan struct{})

	go func() {
		defer close(engineDone)

		eng.Run(ctx)
	}()

	eng.Submit(revision(viewContent(t, lookupDoc+"    version: {key: v}\n"), 1))

	_, err := eng.Await(ctx, 1)
	require.NoError(t, err)

	var (
		failures atomic.Int32
		tick     = newTicker()
		done     = make(chan struct{})
	)

	go func() {
		defer close(done)

		flagruntime.RunSegmentVersions(ctx, holder, &flagruntime.SegmentPoll{
			Wait:    tick.wait,
			OnPass:  tick.onPass,
			OnIdle:  tick.onIdle,
			Tick:    time.Second,
			OnError: func(error) { failures.Add(1) },
		})
	}()

	bounded := func(what string, ch <-chan struct{}) {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			assert.Fail(t, what)
		}
	}

	source.Fail(segment.OpVersion, errStoreDown)

	// the ticks of one second: the first poll is due at once, the next one five seconds later
	steps := []struct {
		name         string
		wantFailures int32
	}{
		{name: "1s", wantFailures: 1},
		{name: "2s", wantFailures: 1},
		{name: "3s", wantFailures: 1},
		{name: "4s", wantFailures: 1},
		{name: "5s", wantFailures: 1},
		{name: "6s", wantFailures: 2},
	}

	for _, step := range steps {
		tick.joined(t, 1)
		assert.Equal(t, step.wantFailures, failures.Load(), step.name)
	}

	cancel()

	for _, stopped := range []chan struct{}{done, engineDone} {
		bounded("a loop did not stop when its context ended", stopped)
	}
}

// A view reports what it serves.
func TestSegmentViewReports(t *testing.T) {
	t.Parallel()

	source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
	view := viewSet(t, &memBinder{source: source}, lookupDoc).Segments.View

	var none *flagruntime.SegmentView

	healthy, failing := view.Health()
	assert.Equal(t, 1, healthy)
	assert.Equal(t, 0, failing)
	assert.Equal(t, []string{connAudiences}, view.Connections())
	assert.True(t, view.Serves(audienceSegmentName))
	assert.False(t, view.Serves("other"))
	assert.False(t, none.Serves(audienceSegmentName))
	assert.Nil(t, none.Connections())
}

// The definitions of a flag set that read a missing key differently are bound apart, one resolver for each, and a
// request that reaches both asks each once; the cache settings of the file and of the definition apply, and the
// observer counts what the resolvers do.
func TestSegmentViewGroupsAndSettings(t *testing.T) {
	t.Parallel()

	const doc = lookupDoc + "    cache: {ttl: 1m, maxEntries: 10}\n" +
		"  - name: strict\n    connection: audiences\n    mode: lookup\n    missingKey: unknown\n"

	var (
		counters resolver.Counters
		calls    = time.Second
		entries  = 100
	)

	source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}, "strict": {memberBob}})
	binder := &memBinder{source: source}
	stores := flagruntime.StoreTable{connAudiences: {Kind: kindRedis, Store: bothStore{}}}
	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{
		SegmentStores: stores, Segments: &flagruntime.SegmentEnv{
			Binder: binder, Observer: &counters,
			Resolver: &bootstrap.SegmentResolver{
				CallTimeout: durationOf(calls), Default: bootstrap.SegmentTuning{MaxEntries: &entries},
			},
		},
	})
	set := buildAndCommit(t, builder, viewContent(t, doc)).Runtime.FlagSets["a"]
	eval := flagruntime.NewEvaluator(newService(t), nil)

	assert.ElementsMatch(t, []segment.MissingKey{segment.MissingNotMember, segment.MissingUnknown}, binder.missing)

	tests := []struct {
		name      string
		target    string
		wantValue bool
	}{
		{name: "first group", target: memberAlice, wantValue: true},
		{name: "second group", target: memberBob, wantValue: true},
		{name: "neither", target: "carol"},
	}

	for _, tt := range tests {
		got, err := eval.BoolVariationDetails(
			t.Context(),
			set,
			"either-flag",
			ffcontext.NewEvaluationContext(tt.target),
			false,
		)
		require.NoError(t, err, tt.name)
		assert.Equal(t, tt.wantValue, got.Value, tt.name)
	}

	assert.Equal(t, 6, source.Calls(segment.OpContains), "one call for each group of each evaluation")

	_, err := eval.BoolVariationDetails(
		t.Context(),
		set,
		"either-flag",
		ffcontext.NewEvaluationContext(memberAlice),
		false,
	)
	require.NoError(t, err)
	assert.Positive(t, counters.Snapshot().Hits, "the second evaluation of alice is answered by the cache")
}

// A lookup that the batch could not read (an attribute inside a typed object) is asked when the rule reaches it.
func TestSegmentViewAsksWhatTheBatchCouldNotRead(t *testing.T) {
	t.Parallel()

	type user struct {
		ID string `json:"id"`
	}

	source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
	set := viewSet(t, &memBinder{source: source}, lookupDoc)
	eval := flagruntime.NewEvaluator(newService(t), nil)

	evalCtx := ffcontext.NewEvaluationContextBuilder("k").AddCustom("user", user{ID: memberAlice}).Build()
	got, err := eval.BoolVariationDetails(t.Context(), set, "typed-flag", evalCtx, false)

	require.NoError(t, err)
	assert.True(t, got.Value)
	assert.Equal(t, 1, source.Calls(segment.OpContains))
}

func durationOf(d time.Duration) *bootstrap.Duration {
	v := bootstrap.Duration(d)

	return &v
}

// A segment without a version key is never polled.
func TestSegmentViewWithoutVersionKeyIsNotPolled(t *testing.T) {
	t.Parallel()

	source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
	view := viewSet(t, &memBinder{source: source}, lookupDoc+"    version: {value: v1}\n").Segments.View

	require.NoError(t, view.RefreshVersions(t.Context(), time.Unix(1_000, 0)))
	assert.Equal(t, 0, source.Calls(segment.OpVersion))
}
