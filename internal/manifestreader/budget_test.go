package manifestreader_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// This file is the request budget suite of #8: the numbers a reader costs the store, asserted exactly on an
// in-memory store that records every request, with a scheduler that the test drives (no timer, no wall clock).

func manifestKeyOf(index int) string { return fmt.Sprintf("manifests/control-%d.yaml", index) }

// budgetObjects returns n distinct objects.
func budgetObjects(n int) []obj {
	objs := make([]obj, n)
	for i := range objs {
		objs[i] = obj{name: fmt.Sprintf("set-%03d", i), content: fmt.Sprintf(`{"flag-%d":{"state":"ENABLED"}}`, i)}
	}

	return objs
}

// publishAt writes the objects and then the manifest of generation gen at key.
func publishAt(tb testing.TB, store *fakeStore, key string, gen uint64, objs ...obj) {
	tb.Helper()

	for _, o := range objs {
		store.put(keyOf(tb, o), []byte(o.content))
	}

	store.put(key, mustMarshal(tb, manifestOf(tb, gen, objs...)))
}

// controlSpecs is C readers, each on its own manifest of the same store.
func controlSpecs(connections int, opts ...manifestreader.Option) []readerSpec {
	specs := make([]readerSpec, connections)
	for i := range specs {
		specs[i] = readerSpec{cfg: manifestreader.Config{ManifestKey: manifestKeyOf(i)}, opts: opts}
	}

	return specs
}

func etagAt(store *fakeStore, key string) string {
	store.mu.Lock()
	defer store.mu.Unlock()

	return store.etags[key]
}

func TestBudget_SteadyStateIsOneConditionalGetPerRound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		connections int
		rounds      int
		objects     int
	}{
		{name: "one connection, 1 round", connections: 1, rounds: 1, objects: 5},
		{name: "one connection, 100 rounds", connections: 1, rounds: 100, objects: 5},
		{name: "one connection, the number of objects does not matter", connections: 1, rounds: 10, objects: 500},
		{name: "three connections, 20 rounds", connections: 3, rounds: 20, objects: 5},
		{name: "five connections, 7 rounds, no object", connections: 5, rounds: 7, objects: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			objs := budgetObjects(tt.objects)

			for i := range tt.connections {
				publishAt(t, store, manifestKeyOf(i), 1, objs...)
			}

			flt := startFleet(t, store, controlSpecs(tt.connections))

			// startup: C manifests and the distinct objects once per connection (nothing is shared between readers)
			assert.Equal(t, coldFleetCalls(t, tt.connections, objs), store.takeCalls())

			flt.runTo(uint64(1 + tt.rounds))

			want := make([]call, 0, tt.connections*tt.rounds)

			for i := range tt.connections {
				for range tt.rounds {
					want = append(want, manifestCall2(manifestKeyOf(i), etagAt(store, manifestKeyOf(i))))
				}
			}

			assert.Equal(t, sortCalls(want), store.takeCalls(), "exactly C x N conditional GETs and nothing else")

			for _, rep := range flt.replicas {
				status := rep.reader.Status()
				assert.Equal(t, uint64(1+tt.rounds), status.Polls)
				assert.Equal(t, uint64(tt.rounds), status.NotModified)
			}
		})
	}
}

// repeated is n copies of c.
func repeated(c call, n int) []call {
	out := make([]call, n)
	for i := range out {
		out[i] = c
	}

	return out
}

// coldFleetCalls is what connections readers that hold nothing request: each reads its manifest unconditionally and
// fetches each object once.
func coldFleetCalls(tb testing.TB, connections int, objs []obj) []call {
	tb.Helper()

	out := make([]call, 0, (1+len(objs))*connections)

	for i := range connections {
		out = append(out, manifestCall2(manifestKeyOf(i), ""))

		for _, item := range objs {
			out = append(out, objCall(tb, item))
		}
	}

	return sortCalls(out)
}

func manifestCall2(key, etag string) call { return call{Key: key, ETag: etag} }

func TestBudget_ChangedObjectsCostOneGetEach(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		changed []int
		extra   int
	}{
		{name: "nothing but a new generation", changed: nil},
		{name: "one object", changed: []int{3}},
		{name: "three objects", changed: []int{0, 7, 19}},
		{name: "an object that is added", changed: []int{3}, extra: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			objs := budgetObjects(20)
			publishAt(t, store, manifestKey, 1, objs...)

			flt := startFleet(t, store, []readerSpec{{}})
			store.takeCalls()

			held := manifestETag(store)

			next := append([]obj(nil), objs...)

			var want []call

			for _, index := range tt.changed {
				next[index] = obj{name: objs[index].name, content: fmt.Sprintf(`{"changed-%d":true}`, index)}
				want = append(want, objCall(t, next[index]))
			}

			for i := range tt.extra {
				added := obj{name: fmt.Sprintf("added-%d", i), content: fmt.Sprintf(`{"added-%d":true}`, i)}
				next = append(next, added)
				want = append(want, objCall(t, added))
			}

			publishAt(t, store, manifestKey, 2, next...)

			flt.runTo(2)

			assert.Equal(t, sortCalls(append([]call{manifestCall(held)}, want...)), store.takeCalls())

			// and quiet again
			held = manifestETag(store)

			flt.runTo(5)
			assert.Equal(
				t,
				sortCalls([]call{manifestCall(held), manifestCall(held), manifestCall(held)}),
				store.takeCalls(),
			)
		})
	}
}

func TestBudget_StartupCostsAtMostConnectionsPlusDistinctObjects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		connections  int
		distinct     int
		duplicateOf  int
		wantRequests int
	}{
		{name: "all distinct", connections: 1, distinct: 10, wantRequests: 1 + 10},
		{
			name:         "ten names, three share one content: one download",
			connections:  1,
			distinct:     8,
			duplicateOf:  2,
			wantRequests: 1 + 8,
		},
		{name: "two connections", connections: 2, distinct: 4, wantRequests: 2 * (1 + 4)},
		{name: "an empty manifest", connections: 1, distinct: 0, wantRequests: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			objs := budgetObjects(tt.distinct)

			if tt.duplicateOf > 0 {
				for i := range tt.duplicateOf {
					objs = append(objs, obj{name: fmt.Sprintf("twin-%d", i), content: objs[0].content})
				}
			}

			for i := range tt.connections {
				publishAt(t, store, manifestKeyOf(i), 1, objs...)
			}

			flt := startFleet(t, store, controlSpecs(tt.connections))

			// the manifest of each connection, and each distinct content once per connection (a twin is not fetched)
			assert.Equal(t, coldFleetCalls(t, tt.connections, objs[:tt.distinct]), store.takeCalls())
			assert.Len(t, coldFleetCalls(t, tt.connections, objs[:tt.distinct]), tt.wantRequests)

			for _, rep := range flt.replicas {
				assert.Equal(t, tt.distinct+tt.duplicateOf, rep.reader.Status().Counts.OK)
			}
		})
	}
}

// stratified puts a stratified jitter source on a replica: replica i of n draws (i + 0.5) / n, the values of
// evenly spread random draws, so the test asserts how a draw maps to a delay without depending on luck.
func stratified(index, total int) manifestreader.Option {
	value := (float64(index) + 0.5) / float64(total)

	return manifestreader.WithJitter(func() float64 { return value })
}

func TestBudget_TwoHundredReplicasOnePublish(t *testing.T) {
	t.Parallel()

	const (
		replicas = 200
		objects  = 10
		steady   = 3 // rounds before the publish
		after    = 2 // rounds from the publish on
		changed  = 2
	)

	store := newFakeStore()
	objs := budgetObjects(objects)
	publishAt(t, store, manifestKey, 1, objs...)

	specs := make([]readerSpec, replicas)
	for i := range specs {
		specs[i] = readerSpec{opts: []manifestreader.Option{stratified(i, replicas)}}
	}

	flt := startFleet(t, store, specs)

	// startup: every replica reads the manifest and the objects once, with no spreading delay (it has no content)
	cold := make([]call, 0, replicas*(1+len(objs)))

	for range replicas {
		cold = append(cold, coldCalls(t, objs...)...)
	}

	assert.Equal(t, sortCalls(cold), store.takeCalls())

	for _, rep := range flt.replicas {
		assert.Empty(t, rep.sched.shorterThan(manifestreader.DefaultPollInterval), "a cold start does not wait")
	}

	flt.runTo(1 + steady)

	// the publisher changes two objects
	next := append([]obj(nil), objs...)
	next[1].content = `{"changed":1}`
	next[8].content = `{"changed":8}`

	held := manifestETag(store)
	publishAt(t, store, manifestKey, 2, next...)

	flt.runTo(1 + steady + after)

	calls := store.takeCalls()

	// the budget, as the exact list of requests: every poll before the publish and the one that reads the new
	// manifest send the held ETag, the next poll sends the new one, and each changed object is fetched once per
	// replica. Nothing else.
	newETag := manifestETag(store)

	want := make([]call, 0, replicas*(steady+after+changed))
	want = append(want, repeated(manifestCall(held), replicas*(steady+1))...)
	want = append(want, repeated(manifestCall(newETag), replicas*(after-1))...)
	want = append(want, repeated(objCall(t, next[1]), replicas)...)
	want = append(want, repeated(objCall(t, next[8]), replicas)...)

	assert.ElementsMatch(t, want, calls, "the same requests, in whatever order the replicas sent them")
	assert.Len(t, want, replicas*(steady+after)+replicas*changed, "200 x rounds + 200 x changed objects")
}

func TestBudget_SpreadDelayIsJitterTimesTheWindow(t *testing.T) {
	t.Parallel()

	const (
		replicas = 200
		window   = manifestreader.DefaultFetchSpread
	)

	evenly := []int{40, 40, 40, 40, 40}

	tests := []struct {
		name       string
		wantSlots  []int
		cfg        manifestreader.Config
		wantWindow time.Duration
		// wantDelays is the number of spreading delays each replica asks the scheduler for
		wantDelays int
	}{
		{name: "default window", cfg: manifestreader.Config{}, wantWindow: window, wantDelays: 1, wantSlots: evenly},
		{
			name: "a configured window", cfg: manifestreader.Config{FetchSpread: 2 * time.Second},
			wantWindow: 2 * time.Second, wantDelays: 1, wantSlots: evenly,
		},
		{
			name:       "the window never passes the poll interval",
			cfg:        manifestreader.Config{PollInterval: time.Second, FetchSpread: time.Minute},
			wantWindow: time.Second, wantDelays: 1, wantSlots: evenly,
		},
		{
			name: "disabled: everybody fetches at once", cfg: manifestreader.Config{DisableFetchSpread: true},
			wantWindow: window, wantSlots: []int{0, 0, 0, 0, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			objs := budgetObjects(4)
			publishAt(t, store, manifestKey, 1, objs...)

			specs := make([]readerSpec, replicas)
			for i := range specs {
				specs[i] = readerSpec{cfg: tt.cfg, opts: []manifestreader.Option{stratified(i, replicas)}}
			}

			flt := startFleet(t, store, specs)

			next := append([]obj(nil), objs...)
			next[0].content = `{"changed":true}`

			held := manifestETag(store)
			publishAt(t, store, manifestKey, 2, next...)
			store.takeCalls()

			flt.runTo(2)

			bound := cmpDuration(tt.cfg.PollInterval, manifestreader.DefaultPollInterval)

			var delays []time.Duration

			for _, rep := range flt.replicas {
				got := rep.sched.shorterThan(bound)

				assert.Len(t, got, tt.wantDelays, "the delays before the fetches of the generation")

				delays = append(delays, got...)
			}

			// spreading costs no request: the manifest read and the one changed object, for every replica
			want := append(repeated(manifestCall(held), replicas), repeated(objCall(t, next[0]), replicas)...)
			assert.ElementsMatch(t, want, store.takeCalls())

			// the delays fill the window evenly: 5 equal slots hold the same number of replicas
			slots := make([]int, 5)

			for _, delay := range delays {
				assert.GreaterOrEqual(t, delay, time.Duration(0))
				assert.Less(t, delay, tt.wantWindow)

				slots[int(math.Floor(float64(delay)/float64(tt.wantWindow)*float64(len(slots))))]++
			}

			assert.Equal(t, tt.wantSlots, slots)
		})
	}
}

func cmpDuration(value, fallback time.Duration) time.Duration {
	if value == 0 {
		return fallback
	}

	return value
}
