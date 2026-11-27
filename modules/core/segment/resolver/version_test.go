package resolver_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

const segAlphaName = "alpha"

func versioned(t *testing.T, src segment.Provider, names ...string) *resolver.Resolver {
	t.Helper()

	opts := &resolver.Options{Segments: map[string]resolver.SegmentOptions{}}
	for _, name := range names {
		opts.Segments[name] = resolver.SegmentOptions{Versioned: true}
	}

	res, err := resolver.New(src, opts)
	require.NoError(t, err)

	return res
}

func ask(t *testing.T, res *resolver.Resolver) segment.State {
	t.Helper()

	lookups := []segment.Lookup{{Segment: segAlphaName, ID: "a"}}
	require.NoError(t, res.Contains(context.Background(), lookups))

	return lookups[0].State
}

func TestWithoutAVersionOnlyTheTTLApplies(t *testing.T) {
	t.Parallel()

	src := segment.NewMemorySource(map[string][]string{segAlphaName: {"a"}})
	res := versioned(t, src)

	assert.Equal(t, segment.Member, ask(t, res))
	assert.Equal(t, segment.Member, ask(t, res))
	assert.Equal(t, 1, src.Calls(segment.OpContains), "the second answer came from the cache")
	assert.Empty(t, res.Status().Segments[segAlphaName].Version)
}

func TestSetVersionMakesOldEntriesMisses(t *testing.T) {
	t.Parallel()

	src := segment.NewMemorySource(map[string][]string{segAlphaName: {"a"}})
	res := versioned(t, src, segAlphaName)

	assert.Equal(t, segment.Member, ask(t, res))
	assert.False(t, res.SetVersion(segAlphaName, ""), "the same version changes nothing")
	assert.Equal(t, segment.Member, ask(t, res))
	assert.Equal(t, 1, src.Calls(segment.OpContains), "same version: a hit")

	assert.True(t, res.SetVersion(segAlphaName, "v2"))
	assert.Equal(t, 1, res.Status().Segments[segAlphaName].Entries, "no flush: the old entry is still there")

	src.Set(segAlphaName, []string{"b"})

	assert.Equal(t, segment.NotMember, ask(t, res), "another version: the store is asked again")
	assert.Equal(t, 2, src.Calls(segment.OpContains))
	assert.Equal(t, segment.NotMember, ask(t, res), "the new answer is cached under v2")
	assert.Equal(t, 2, src.Calls(segment.OpContains))
	assert.Equal(t, 1, res.Status().Segments[segAlphaName].Entries, "the entry was replaced, not added")
	assert.Equal(t, "v2", res.Status().Segments[segAlphaName].Version)

	assert.True(t, res.SetVersion(segAlphaName, ""), "back to the first token is a change too")
	assert.Equal(t, segment.NotMember, ask(t, res))
	assert.Equal(t, 3, src.Calls(segment.OpContains), "an entry of v2 is a miss under the old token")
}

func TestSetVersionIgnoresWhatItDoesNotKnow(t *testing.T) {
	t.Parallel()

	res := versioned(t, segment.NewMemorySource(nil), segAlphaName)
	assert.False(t, res.SetVersion("ghost", "v1"))

	uncached, err := resolver.New(segment.NewMemorySource(nil), &resolver.Options{
		Segments: map[string]resolver.SegmentOptions{segAlphaName: {NoCache: true, Versioned: true}},
	})
	require.NoError(t, err)
	assert.False(t, uncached.SetVersion(segAlphaName, "v1"), "no cache, nothing to stamp")
}

func TestRefreshVersionsAsksOnlyVersionedSegments(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	src := segment.NewMemorySource(map[string][]string{segAlphaName: {"a"}, segBeta: {"b"}, "gamma": {"c"}})
	opts := &resolver.Options{Segments: map[string]resolver.SegmentOptions{
		segAlphaName: {Versioned: true}, segBeta: {}, "gamma": {Versioned: true},
	}}

	res, err := resolver.New(src, opts)
	require.NoError(t, err)

	src.PinVersion(segAlphaName, "a7")
	src.PinVersion(segBeta, "b7")

	require.NoError(t, res.RefreshVersions(ctx, src))

	segs := res.Status().Segments
	assert.Equal(t, "a7", segs[segAlphaName].Version)
	assert.Empty(t, segs[segBeta].Version, "beta is not versioned")
	assert.Equal(t, 2, src.Calls(segment.OpVersion), "alpha and gamma only")
}

func TestRefreshVersionsKeepsTheVersionOnFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	src := segment.NewMemorySource(map[string][]string{segAlphaName: {"a"}, segBeta: {"b"}})
	res := versioned(t, src, segAlphaName, segBeta)

	src.PinVersion(segAlphaName, "a1")
	require.NoError(t, res.RefreshVersions(ctx, src))
	assert.Equal(t, segment.Member, ask(t, res))

	src.Fail(segment.OpVersion, assert.AnError)

	err := res.RefreshVersions(ctx, src)
	require.ErrorIs(t, err, assert.AnError)
	require.ErrorContains(t, err, `segment "alpha" version`)
	require.ErrorContains(t, err, `segment "beta" version`, "one failure does not stop the step")
	assert.Equal(t, "a1", res.Status().Segments[segAlphaName].Version, "the version stays: the TTL is the fallback")
	assert.Equal(t, segment.Member, ask(t, res))
	assert.Equal(t, 1, src.Calls(segment.OpContains), "still a hit")

	src.Fail(segment.OpVersion, nil)

	ended, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, res.RefreshVersions(ended, src), context.Canceled)
}

// gated is a provider whose call waits for the test.
type gated struct {
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (g *gated) Contains(_ context.Context, lookups []segment.Lookup) error {
	g.mu.Lock()
	g.calls++
	first := g.calls == 1
	g.mu.Unlock()

	if first {
		close(g.started)
		<-g.release
	}

	for i := range lookups {
		lookups[i].State = segment.Member
	}

	return nil
}

func TestAnAnswerFetchedUnderAnOldVersionIsNotCached(t *testing.T) {
	t.Parallel()

	prov := &gated{started: make(chan struct{}), release: make(chan struct{})}
	res := versioned(t, prov, segAlphaName)

	var (
		wait sync.WaitGroup
		got  segment.State
	)

	wait.Go(func() {
		lookups := []segment.Lookup{{Segment: segAlphaName, ID: "a"}}
		assert.NoError(t, res.Contains(context.Background(), lookups))

		got = lookups[0].State
	})

	<-prov.started
	assert.True(t, res.SetVersion(segAlphaName, "v2"), "the version moves while the call is in flight")
	close(prov.release)
	wait.Wait()

	assert.Equal(t, segment.Member, got, "the caller of the call still gets its answer")
	assert.Equal(t, 0, res.Status().Segments[segAlphaName].Entries, "but it is not cached under the new version")
	assert.Equal(t, segment.Member, ask(t, res))
	assert.Equal(t, 2, prov.calls)
}

//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestVersionedCacheHitAllocatesNothing(t *testing.T) {
	src := segment.NewMemorySource(map[string][]string{segAlphaName: {"a"}})
	res := versioned(t, src, segAlphaName)
	lookups := []segment.Lookup{{Segment: segAlphaName, ID: "a"}}
	ctx := context.Background()

	res.SetVersion(segAlphaName, "v1")
	require.NoError(t, res.Contains(ctx, lookups))

	assert.Zero(t, cheapestAllocs(func() {
		lookups[0].State = segment.Unknown
		assert.NoError(t, res.Contains(ctx, lookups))
	}), "a hit under a version")
	assert.Equal(t, segment.Member, lookups[0].State)
}
