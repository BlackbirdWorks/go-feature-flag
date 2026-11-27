package segmenttest

import (
	"context"
	"errors"
	"iter"
	"runtime"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/snapshot"
)

// Seed creates or replaces the members of a segment in the store behind a source, and must change the version that
// the source reports for it. It is how the suites write to a store.
type Seed func(name string, ids []string) error

// SnapshotFixture is a store under test: the source, and the way to write to the store behind it.
type SnapshotFixture struct {
	// Source is the implementation under test.
	Source segment.SnapshotSource
	// Seed writes members to the store.
	Seed Seed
	// Close (optional) releases the store after the case.
	Close func()
	// Remove (optional) deletes a segment from the store and moves its version.
	Remove func(name string) error
	// Missing is what a segment that does not exist means for the holders of the suite: the default (empty) is
	// segment.MissingNotMember, segment.MissingUnknown is the opt-in.
	Missing segment.MissingKey
	// NoEmptySet is true for a store that cannot hold an empty segment (a Redis set does not exist without a
	// member): seeding no member then removes the segment, and the suite expects it to be missing.
	NoEmptySet bool
}

func (f SnapshotFixture) options(opts snapshot.Options) snapshot.Options {
	opts.Missing = f.Missing

	return opts
}

// SnapshotSegments returns the names of the segments that the suite uses, for a factory whose store must be told its
// segments before it starts.
func SnapshotSegments() []string { return []string{"alpha", "beta", "empty", "ghost"} }

// SnapshotFactory builds a fixture of an empty store for a case.
type SnapshotFactory func() (SnapshotFixture, error)

// The names of the cases of [RunSnapshotSource].
const (
	CaseSnapEmptyVsMissing  = "empty-is-a-set-and-missing-is-unknown"
	CaseSnapStreamsMembers  = "members-are-streamed-exactly"
	CaseSnapVersionMoves    = "version-is-stable-and-moves-with-a-write"
	CaseSnapCtxEnded        = "an-ended-context-fails-both-calls"
	CaseSnapNoRepull        = "an-unchanged-version-is-not-pulled-again"
	CaseSnapLastKnownGood   = "a-failed-pull-keeps-the-last-known-good"
	CaseSnapVersionFails    = "a-failed-version-keeps-the-last-known-good"
	CaseSnapCancelMidStream = "a-cancel-during-the-stream-keeps-the-last-known-good"
	CaseSnapMemoryGuard     = "the-memory-guard-refuses-and-keeps-the-last-known-good"
	CaseSnapGone            = "a-segment-gone-from-the-store-is-unknown-not-last-known-good"
	CaseSnapNeverLoaded     = "a-holder-that-never-loaded-answers-unknown"
	CaseSnapLargeStreamed   = "a-large-set-is-built-without-a-second-copy"
)

var (
	errStreamBroke = errors.New("the stream broke")
	errStoreDown   = errors.New("store down")
)

const (
	snapLarge      = 200_000
	snapMid        = 5_000
	snapAllocSlack = 1 << 20
	snapSmall      = 10
	snapStream     = 300
	snapMedium     = 50
	snapOver       = snapSmall + 1
	snapByteLimit  = 20
	snapPulls      = 3
	snapTwoPulls   = 2
	snapListed     = 20
)

// probe wraps a source: it counts the calls, can fail the stream after n members and calls a hook inside it.
type probe struct {
	inner     segment.SnapshotSource
	hook      func(yielded int)
	failVer   error
	failAfter error
	after     int
	mu        sync.Mutex
	versions  int
	pulls     int
}

func (p *probe) Version(ctx context.Context, name string) (string, error) {
	p.mu.Lock()
	p.versions++
	failure := p.failVer
	p.mu.Unlock()

	if failure != nil {
		return "", failure
	}

	return p.inner.Version(ctx, name) //nolint:wrapcheck // a transparent wrapper
}

func (p *probe) Members(ctx context.Context, name string) iter.Seq2[string, error] {
	p.mu.Lock()
	p.pulls++
	failure, after, hook := p.failAfter, p.after, p.hook
	p.mu.Unlock()

	return func(yield func(string, error) bool) {
		yielded := 0

		for id, err := range p.inner.Members(ctx, name) {
			if err != nil {
				yield("", err)

				return
			}

			if hook != nil {
				hook(yielded)
			}

			if failure != nil && yielded == after {
				yield("", failure)

				return
			}

			if !yield(id, nil) {
				return
			}

			yielded++
		}
	}
}

func (p *probe) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.versions, p.pulls
}

func idsOf(prefix string, n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = prefix + strconv.Itoa(i)
	}

	return ids
}

// RunSnapshotSource runs the conformance suite of [segment.SnapshotSource] against the implementation that the
// factory builds, once per case, and drives a [snapshot.Holder] over it for the cases of the holder contract (exact
// call counts, last known good, cancellation, the memory guard, one copy of a large set). Call it from a test that
// is not parallel: one case measures allocations of the whole process.
func RunSnapshotSource(t *testing.T, factory SnapshotFactory) {
	t.Helper()

	for _, tcase := range snapshotCases() {
		t.Run(tcase.name, func(t *testing.T) {
			fixture, err := factory()
			require.NoError(t, err, "the factory builds a fixture")

			if fixture.Close != nil {
				t.Cleanup(fixture.Close)
			}

			tcase.run(t, fixture)
		})
	}
}

// CheckSnapshotSource runs the same cases without a [testing.T] and returns the failures of each case that failed, by
// case name. It is how the suite is tested against deliberately broken sources.
func CheckSnapshotSource(factory SnapshotFactory) map[string][]string {
	failed := map[string][]string{}

	for _, tcase := range snapshotCases() {
		rep := &reporter{}

		fixture, err := factory()
		if err != nil {
			failed[tcase.name] = []string{"the factory failed: " + err.Error()}

			continue
		}

		done := make(chan struct{})

		go func() {
			defer close(done)

			tcase.run(rep, fixture)
		}()

		<-done

		if fixture.Close != nil {
			fixture.Close()
		}

		if rep.failed {
			failed[tcase.name] = rep.failures
		}
	}

	return failed
}

// caseT is what a case needs of a test: assertions and Helper. A [*testing.T] and the reporter of Check satisfy it.
type caseT interface {
	require.TestingT
	Helper()
}

type snapshotCase struct {
	run  func(caseT, SnapshotFixture)
	name string
}

func snapshotCases() []snapshotCase {
	return []snapshotCase{
		{snapEmptyVsMissing, CaseSnapEmptyVsMissing},
		{snapStreamsMembers, CaseSnapStreamsMembers},
		{snapVersionMoves, CaseSnapVersionMoves},
		{snapCtxEnded, CaseSnapCtxEnded},
		{snapNoRepull, CaseSnapNoRepull},
		{snapLastKnownGood, CaseSnapLastKnownGood},
		{snapVersionFails, CaseSnapVersionFails},
		{snapCancelMidStream, CaseSnapCancelMidStream},
		{snapMemoryGuard, CaseSnapMemoryGuard},
		{snapGone, CaseSnapGone},
		{snapNeverLoaded, CaseSnapNeverLoaded},
		{snapLargeStreamed, CaseSnapLargeStreamed},
	}
}

func collect(src segment.SnapshotSource, name string) ([]string, error) {
	var ids []string

	for id, err := range src.Members(context.Background(), name) {
		if err != nil {
			return ids, err
		}

		ids = append(ids, id)
	}

	return ids, nil
}

func snapEmptyVsMissing(t caseT, fix SnapshotFixture) {
	t.Helper()

	require.NoError(t, fix.Seed("empty", nil))

	ids, err := collect(fix.Source, "empty")
	if fix.NoEmptySet {
		require.ErrorIs(t, err, segment.ErrSegmentMissing, "a store without empty sets reports it as missing")
		assert.Empty(t, ids)
	} else {
		require.NoError(t, err, "an existing empty segment is not an error")
		assert.Empty(t, ids, "an existing empty segment has no member")
	}

	ids, err = collect(fix.Source, "ghost")
	require.ErrorIs(t, err, segment.ErrSegmentMissing, "a missing segment is reported as missing")
	assert.Empty(t, ids, "a missing segment yields no member")

	items := 0

	for _, streamErr := range fix.Source.Members(context.Background(), "ghost") {
		items++

		require.ErrorIs(t, streamErr, segment.ErrSegmentMissing)
	}

	assert.Equal(t, 1, items, "the stream ends with its error: nothing is yielded after it")
}

func snapStreamsMembers(t caseT, fix SnapshotFixture) {
	t.Helper()

	want := append(idsOf("m", snapStream), "ünï")
	require.NoError(t, fix.Seed("alpha", want))

	got, err := collect(fix.Source, "alpha")
	require.NoError(t, err)
	assert.ElementsMatch(t, want, got, "every member exactly once, whatever the bytes")
}

func snapVersionMoves(t caseT, fix SnapshotFixture) {
	t.Helper()

	ctx := context.Background()

	require.NoError(t, fix.Seed("alpha", []string{"a"}))

	first, err := fix.Source.Version(ctx, "alpha")
	require.NoError(t, err)

	again, err := fix.Source.Version(ctx, "alpha")
	require.NoError(t, err)
	assert.Equal(t, first, again, "no write, same version")

	require.NoError(t, fix.Seed("alpha", []string{"a", "b"}))

	moved, err := fix.Source.Version(ctx, "alpha")
	require.NoError(t, err)
	assert.NotEqual(t, first, moved, "a write moves the version")
}

func snapCtxEnded(t caseT, fix SnapshotFixture) {
	t.Helper()

	require.NoError(t, fix.Seed("alpha", idsOf("m", snapSmall)))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := fix.Source.Version(ctx, "alpha")
	require.ErrorIs(t, err, context.Canceled, "Version honours an ended context")

	var streamErr error

	for _, err := range fix.Source.Members(ctx, "alpha") {
		if err != nil {
			streamErr = err

			break
		}
	}

	require.ErrorIs(t, streamErr, context.Canceled, "Members honours an ended context")
}

func newHolder(t caseT, src segment.SnapshotSource, opts snapshot.Options) *snapshot.Holder {
	t.Helper()

	holder, err := snapshot.New("alpha", src, opts)
	require.NoError(t, err)

	return holder
}

func snapNoRepull(t caseT, fix SnapshotFixture) {
	t.Helper()

	ctx := context.Background()
	prb := &probe{inner: fix.Source}
	holder := newHolder(t, prb, fix.options(snapshot.Options{}))

	require.NoError(t, fix.Seed("alpha", []string{"a", "b"}))

	for range snapPulls {
		require.NoError(t, holder.Refresh(ctx))
	}

	versions, pulls := prb.counts()
	assert.Equal(t, snapPulls, versions, "the version is read on every refresh")
	assert.Equal(t, 1, pulls, "the members are pulled once while the version stays")
	assert.Equal(t, uint64(1), holder.Status().Pulls)

	grown := []string{"a", "b", "c"}
	require.NoError(t, fix.Seed("alpha", grown))
	require.NoError(t, holder.Refresh(ctx))

	versions, pulls = prb.counts()
	assert.Equal(t, snapPulls+1, versions)
	assert.Equal(t, snapTwoPulls, pulls, "a new version is pulled once")
	assert.Equal(t, segment.Member, holder.Lookup("c"))
	assert.Equal(t, len(grown), holder.Status().Members)
}

func snapLastKnownGood(t caseT, fix SnapshotFixture) {
	t.Helper()

	ctx := context.Background()
	prb := &probe{inner: fix.Source}
	holder := newHolder(t, prb, fix.options(snapshot.Options{}))

	require.NoError(t, fix.Seed("alpha", idsOf("old", snapMedium)))
	require.NoError(t, holder.Refresh(ctx))

	oldVersion := holder.Status().Version

	require.NoError(t, fix.Seed("alpha", idsOf("new", snapMedium)))

	errMid := errStreamBroke

	prb.mu.Lock()
	prb.failAfter, prb.after = errMid, snapSmall
	prb.mu.Unlock()

	require.ErrorIs(t, holder.Refresh(ctx), errMid, "the failure of the stream is returned")

	status := holder.Status()
	assert.Equal(t, snapshot.ClassRead, status.LastError)
	assert.Equal(t, oldVersion, status.Version, "the held version did not move")
	assert.Equal(t, segment.Member, holder.Lookup("old7"), "the last good set still answers")
	assert.Equal(t, segment.NotMember, holder.Lookup("new7"), "nothing of the failed pull leaked in")

	prb.mu.Lock()
	prb.failAfter = nil
	prb.mu.Unlock()

	require.NoError(t, holder.Refresh(ctx), "the store is back")
	assert.Equal(t, snapshot.ClassNone, holder.Status().LastError)
	assert.Equal(t, segment.Member, holder.Lookup("new7"))
	assert.Equal(t, segment.NotMember, holder.Lookup("old7"))
}

func snapVersionFails(t caseT, fix SnapshotFixture) {
	t.Helper()

	ctx := context.Background()
	prb := &probe{inner: fix.Source}
	holder := newHolder(t, prb, fix.options(snapshot.Options{}))

	require.NoError(t, fix.Seed("alpha", idsOf("m", snapListed)))
	require.NoError(t, holder.Refresh(ctx))

	errDown := errStoreDown

	prb.mu.Lock()
	prb.failVer = errDown
	prb.mu.Unlock()

	require.ErrorIs(t, holder.Refresh(ctx), errDown)
	assert.Equal(t, snapshot.ClassVersion, holder.Status().LastError)
	assert.Equal(t, segment.Member, holder.Lookup("m3"), "the store is down, the set keeps answering")

	_, pulls := prb.counts()
	assert.Equal(t, 1, pulls, "no pull while the version cannot be read")
}

func snapCancelMidStream(t caseT, fix SnapshotFixture) {
	t.Helper()

	prb := &probe{inner: fix.Source}
	holder := newHolder(t, prb, fix.options(snapshot.Options{}))

	require.NoError(t, fix.Seed("alpha", idsOf("old", snapSmall)))
	require.NoError(t, holder.Refresh(context.Background()))
	require.NoError(t, fix.Seed("alpha", idsOf("new", snapMid)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const cancelAt = 2000

	last := 0

	prb.mu.Lock()
	prb.hook = func(yielded int) {
		last = yielded // the hook runs in the stream, on the goroutine of Refresh

		if yielded == cancelAt {
			cancel()
		}
	}
	prb.mu.Unlock()

	require.ErrorIs(t, holder.Refresh(ctx), context.Canceled)
	assert.Less(t, last, snapMid-1, "the pull stopped inside the stream, it did not read to the end")
	assert.Equal(t, snapshot.ClassCanceled, holder.Status().LastError)
	assert.Equal(t, segment.Member, holder.Lookup("old3"), "the last good set still answers")
	assert.Equal(t, segment.NotMember, holder.Lookup("new3"))
}

func snapMemoryGuard(t caseT, fix SnapshotFixture) {
	t.Helper()

	ctx := context.Background()

	require.NoError(t, fix.Seed("alpha", idsOf("m", snapSmall)))

	byCount := newHolder(t, fix.Source, fix.options(snapshot.Options{MaxMembers: snapSmall}))
	require.NoError(t, byCount.Refresh(ctx), "exactly at the limit is allowed")

	require.NoError(t, fix.Seed("alpha", idsOf("n", snapOver)))
	require.ErrorIs(t, byCount.Refresh(ctx), segment.ErrTooManyMembers)
	assert.Equal(t, snapshot.ClassTooLarge, byCount.Status().LastError)
	assert.Equal(t, segment.Member, byCount.Lookup("m1"), "the set under the limit is kept")
	assert.Equal(t, segment.NotMember, byCount.Lookup("n1"))

	byBytes := newHolder(t, fix.Source, fix.options(snapshot.Options{MaxBytes: snapByteLimit}))
	require.ErrorIs(t, byBytes.Refresh(ctx), segment.ErrMembersTooLarge, "11 ids of 2 or 3 bytes are over 20 bytes")
	assert.Equal(t, segment.Unknown, byBytes.Lookup("n1"), "a refused first pull holds nothing")
	assert.False(t, byBytes.Status().Loaded)
}

func snapNeverLoaded(t caseT, fix SnapshotFixture) {
	t.Helper()

	holder := newHolder(t, fix.Source, fix.options(snapshot.Options{}))

	assert.Equal(t, segment.Unknown, holder.Lookup("a"), "before any pull the answer is Unknown")

	err := holder.Refresh(context.Background())
	require.ErrorIs(t, err, segment.ErrSegmentMissing)

	status := holder.Status()
	assert.Equal(t, snapshot.ClassMissing, status.LastError)
	assert.Equal(t, wantMissing(fix), holder.Lookup("a"), "a missing segment answers its policy")
}

// wantMissing is the answer of a lookup in a segment that does not exist, under the policy of the fixture.
func wantMissing(fix SnapshotFixture) segment.State {
	if fix.Missing.Effective() == segment.MissingUnknown {
		return segment.Unknown
	}

	return segment.NotMember
}

func totalAlloc(run func()) uint64 {
	var before, after runtime.MemStats

	runtime.GC()
	runtime.ReadMemStats(&before)
	run()
	runtime.ReadMemStats(&after)

	return after.TotalAlloc - before.TotalAlloc
}

func snapLargeStreamed(t caseT, fix SnapshotFixture) {
	t.Helper()

	ids := idsOf("member-", snapLarge)
	require.NoError(t, fix.Seed("alpha", ids))

	var (
		reference *segment.MemberSet
		streamErr error
	)

	// one pass of the source into one set is the least that any holder can allocate
	wantMax := totalAlloc(func() {
		var builder segment.MemberSetBuilder

		for id, err := range fix.Source.Members(context.Background(), "alpha") {
			streamErr = err

			builder.Add(id)
		}

		reference = builder.Build()
	})

	require.NoError(t, streamErr)
	require.Equal(t, snapLarge, reference.Len())

	holder := newHolder(t, fix.Source, fix.options(snapshot.Options{}))

	var refreshErr error

	got := totalAlloc(func() { refreshErr = holder.Refresh(context.Background()) })

	require.NoError(t, refreshErr)

	assert.Equal(t, snapLarge, holder.Status().Members)
	assert.Equal(t, segment.Member, holder.Lookup(ids[snapLarge/2]))
	assert.LessOrEqual(t, got, wantMax+wantMax/8+snapAllocSlack,
		"a pull allocates one copy of the set (%d bytes for one pass), not a list and a set", wantMax)
}

func snapGone(t caseT, fix SnapshotFixture) {
	t.Helper()

	remove := fix.Remove
	if remove == nil {
		require.True(
			t,
			fix.NoEmptySet,
			"a fixture needs Remove, or NoEmptySet when seeding no member removes the segment",
		)

		remove = func(name string) error { return fix.Seed(name, nil) }
	}

	ctx := context.Background()
	prb := &probe{inner: fix.Source}
	holder := newHolder(t, prb, fix.options(snapshot.Options{}))

	require.NoError(t, fix.Seed("alpha", idsOf("m", snapSmall)))
	require.NoError(t, holder.Refresh(ctx))
	require.Equal(t, segment.Member, holder.Lookup("m1"))

	require.NoError(t, remove("alpha"))
	require.ErrorIs(t, holder.Refresh(ctx), segment.ErrSegmentMissing, "the store answered: the segment is gone")

	status := holder.Status()
	assert.Equal(t, snapshot.ClassMissing, status.LastError)
	assert.NotEmpty(t, status.Version, "the version of the missing segment is recorded")
	assert.Equal(t, wantMissing(fix), holder.Lookup("m1"), "a segment that is gone is not served from the old set")

	_, pulls := prb.counts()

	require.NoError(
		t,
		holder.Refresh(ctx),
		"already known to be missing: the status says so, the error was returned once",
	)
	assert.Equal(t, snapshot.ClassMissing, holder.Status().LastError)

	_, again := prb.counts()
	assert.Equal(t, pulls, again, "the version did not move: nothing is pulled")

	require.NoError(t, fix.Seed("alpha", idsOf("n", snapSmall)))
	require.NoError(t, holder.Refresh(ctx))
	assert.Equal(t, segment.Member, holder.Lookup("n1"), "the segment is back")
	assert.Equal(t, snapshot.ClassNone, holder.Status().LastError)
}
