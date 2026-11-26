package manifestreader_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
			assert.Len(t, store.takeCalls(), tt.connections*(1+tt.objects))

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

			calls := store.takeCalls()
			assert.Len(t, calls, tt.wantRequests)

			for _, c := range calls {
				if c.Key != manifestKeyOf(0) && c.Key != manifestKeyOf(1) {
					assert.Empty(t, c.ETag, "object requests are unconditional and never repeated")
				}
			}

			byKey := map[string]int{}
			for _, c := range calls {
				byKey[c.Key]++
			}

			for key, count := range byKey {
				if key == manifestKeyOf(0) || key == manifestKeyOf(1) {
					assert.Equal(t, 1, count, "%s is read once", key)

					continue
				}

				assert.Equal(t, tt.connections, count, "%s is fetched once per connection", key)
			}

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
	assert.Len(t, store.takeCalls(), replicas*(1+objects))

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

	// the budget: 200 x rounds conditional manifest GETs and 200 x changed object GETs, nothing else
	wantManifest := replicas * (steady + after)
	assert.Len(t, calls, wantManifest+replicas*changed)

	byCall := map[call]int{}
	for _, c := range calls {
		byCall[c]++
	}

	// the polls before the publish and the one that reads the new manifest send the held ETag; the next one the new
	newETag := manifestETag(store)

	assert.Equal(t, replicas*(steady+1), byCall[manifestCall(held)])
	assert.Equal(t, replicas*(after-1), byCall[manifestCall(newETag)])
	assert.Equal(t, replicas, byCall[objCall(t, next[1])])
	assert.Equal(t, replicas, byCall[objCall(t, next[8])])
	assert.Len(t, byCall, 4, "no other request")
}

func TestBudget_SpreadDelayIsJitterTimesTheWindow(t *testing.T) {
	t.Parallel()

	const (
		replicas = 200
		window   = manifestreader.DefaultFetchSpread
	)

	tests := []struct {
		name       string
		cfg        manifestreader.Config
		wantWindow time.Duration
		wantWaits  bool
	}{
		{name: "default window", cfg: manifestreader.Config{}, wantWindow: window, wantWaits: true},
		{
			name:       "a configured window",
			cfg:        manifestreader.Config{FetchSpread: 2 * time.Second},
			wantWindow: 2 * time.Second,
			wantWaits:  true,
		},
		{
			name:       "the window never passes the poll interval",
			cfg:        manifestreader.Config{PollInterval: time.Second, FetchSpread: time.Minute},
			wantWindow: time.Second, wantWaits: true,
		},
		{name: "disabled: everybody fetches at once", cfg: manifestreader.Config{DisableFetchSpread: true}},
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

			publishAt(t, store, manifestKey, 2, next...)
			store.takeCalls()

			flt.runTo(2)

			bound := cmpDuration(tt.cfg.PollInterval, manifestreader.DefaultPollInterval)

			var delays []time.Duration

			for _, rep := range flt.replicas {
				got := rep.sched.shorterThan(bound)

				if !tt.wantWaits {
					assert.Empty(t, got)

					continue
				}

				require.Len(t, got, 1, "one delay before the fetches of the generation")

				delays = append(delays, got[0])
			}

			assert.Len(
				t,
				store.takeCalls(),
				replicas*2,
				"spreading costs no request: the manifest and the one changed object",
			)

			if !tt.wantWaits {
				return
			}

			// the delays fill the window evenly: 5 equal slots hold the same number of replicas
			slots := make([]int, 5)

			for _, delay := range delays {
				assert.GreaterOrEqual(t, delay, time.Duration(0))
				assert.Less(t, delay, tt.wantWindow)

				slots[int(math.Floor(float64(delay)/float64(tt.wantWindow)*float64(len(slots))))]++
			}

			assert.Equal(t, []int{40, 40, 40, 40, 40}, slots)
		})
	}
}

func cmpDuration(value, fallback time.Duration) time.Duration {
	if value == 0 {
		return fallback
	}

	return value
}
