package snapshot_test

import (
	"context"
	"iter"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/snapshot"
)

func loaded(t *testing.T, members ...string) (*snapshot.Holder, *segment.MemorySource) {
	t.Helper()

	src := segment.NewMemorySource(map[string][]string{segAlpha: members})
	holder, err := snapshot.New(segAlpha, src, snapshot.Options{})
	require.NoError(t, err)
	require.NoError(t, holder.Refresh(context.Background()))

	return holder, src
}

func TestNewRejectsNilSource(t *testing.T) {
	t.Parallel()

	holder, err := snapshot.New(segAlpha, nil, snapshot.Options{})
	require.ErrorIs(t, err, snapshot.ErrNilSource)
	assert.Nil(t, holder)
}

func TestHoldersOfOneStoreAreIndependent(t *testing.T) {
	t.Parallel()

	// two holders of one store: a holder of one name does not see another
	holder, src := loaded(t, "a", "b")
	other, err := snapshot.New("beta", src, snapshot.Options{})
	require.NoError(t, err)

	require.ErrorIs(t, other.Refresh(context.Background()), segment.ErrSegmentMissing)
	assert.Equal(t, segment.NotMember, other.Lookup("a"), "a segment that does not exist has no member")
	assert.Equal(t, segment.Member, holder.Lookup("a"))
	assert.Equal(t, segment.NotMember, holder.Lookup("z"))
}

func TestStatus(t *testing.T) {
	t.Parallel()

	holder, src := loaded(t, "a", "b")

	assert.Equal(
		t,
		snapshot.Status{Version: "1", Members: 2, Pulls: 1, Loaded: true},
		holder.Status(),
		"frozen clock: age 0",
	)

	src.Set(segAlpha, []string{"a", "b", "c"})
	require.NoError(t, holder.Refresh(context.Background()))
	assert.Equal(t, snapshot.Status{Version: "2", Members: 3, Pulls: 2, Loaded: true}, holder.Status())

	src.Fail(segment.OpVersion, assert.AnError)
	require.ErrorIs(t, holder.Refresh(context.Background()), assert.AnError)
	assert.Equal(t, snapshot.Status{
		Version: "2", Members: 3, Pulls: 2, Loaded: true, LastError: snapshot.ClassVersion,
	}, holder.Status(), "the failure class shows, the set stays")
}

func TestContainsAnswersItsOwnSegmentOnly(t *testing.T) {
	t.Parallel()

	holder, _ := loaded(t, "a")
	lookups := []segment.Lookup{{Segment: segAlpha, ID: "a"}, {Segment: segAlpha, ID: "z"}, {Segment: "beta", ID: "a"}}

	require.NoError(t, holder.Contains(context.Background(), lookups))

	got := []segment.State{lookups[0].State, lookups[1].State, lookups[2].State}
	assert.Equal(t, []segment.State{segment.Member, segment.NotMember, segment.Unknown}, got)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, holder.Contains(ctx, lookups), context.Canceled)

	empty, err := snapshot.New(segAlpha, segment.NewMemorySource(nil), snapshot.Options{})
	require.NoError(t, err)

	fresh := []segment.Lookup{{Segment: segAlpha, ID: "a"}}
	require.NoError(t, empty.Contains(context.Background(), fresh))
	assert.Equal(t, segment.Unknown, fresh[0].State, "nothing loaded: Unknown")
}

//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestMembershipCheckAllocatesNothing(t *testing.T) {
	holder, _ := loaded(t, "a", "b", "c")
	lookups := []segment.Lookup{{Segment: segAlpha, ID: "a"}, {Segment: segAlpha, ID: "z"}}
	ctx := context.Background()

	var sink segment.State

	assert.Zero(t, testing.AllocsPerRun(100, func() { sink = holder.Lookup("b") }), "Lookup")
	assert.Zero(t, testing.AllocsPerRun(100, func() { assert.NoError(t, holder.Contains(ctx, lookups)) }), "Contains")
	assert.Equal(t, segment.Member, sink)
}

func TestReadersSeeEitherSetNeverAGap(t *testing.T) {
	t.Parallel()

	const swaps = 200

	holder, src := loaded(t, "always", "x0")
	done := make(chan struct{})

	var wait sync.WaitGroup

	for range 4 {
		wait.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
				}

				assert.Equal(
					t,
					segment.Member,
					holder.Lookup("always"),
					"a swap is atomic: the common member never blinks",
				)
			}
		})
	}

	for i := range swaps {
		src.Set(segAlpha, []string{"always", "x" + string(rune('a'+i%26))})
		require.NoError(t, holder.Refresh(context.Background()))
	}

	close(done)
	wait.Wait()
	assert.Equal(t, uint64(swaps+1), holder.Status().Pulls)
}

// deaf is a source that ignores its context, even after it ended.
type deaf struct {
	cancel context.CancelFunc
}

func (deaf) Version(context.Context, string) (string, error) { return "v1", nil }

func (d deaf) Members(context.Context, string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		d.cancel()
		yield("a", nil)
	}
}

func TestRefreshFailuresBeforeTheFirstPull(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setup      func(*segment.MemorySource)
		wantErr    error
		name       string
		wantClass  snapshot.Class
		wantVer    string
		opts       snapshot.Options
		wantState  segment.State
		wantLoaded bool
	}{
		{
			name: "version fails", setup: func(s *segment.MemorySource) { s.Fail(segment.OpVersion, errBoom) },
			wantErr: errBoom, wantClass: snapshot.ClassVersion,
		},
		{
			name: "stream fails", setup: func(s *segment.MemorySource) { s.FailMembersAfter(1, errBoom) },
			wantErr: errBoom, wantClass: snapshot.ClassRead,
		},
		{
			name:       "segment missing, default policy: nobody is in it",
			setup:      func(s *segment.MemorySource) { s.Delete("alpha") },
			wantErr:    segment.ErrSegmentMissing,
			wantClass:  snapshot.ClassMissing,
			wantVer:    segment.AbsentVersion,
			wantState:  segment.NotMember,
			wantLoaded: true,
		},
		{
			name: "segment missing, unknown policy", setup: func(s *segment.MemorySource) { s.Delete("alpha") },
			opts:    snapshot.Options{Missing: segment.MissingUnknown},
			wantErr: segment.ErrSegmentMissing, wantClass: snapshot.ClassMissing, wantVer: segment.AbsentVersion,
			wantState: segment.Unknown,
		},
		{
			name: "too many members", opts: snapshot.Options{MaxMembers: 2},
			wantErr: segment.ErrTooManyMembers, wantClass: snapshot.ClassTooLarge,
		},
		{
			name: "too many bytes", opts: snapshot.Options{MaxBytes: 2},
			wantErr: segment.ErrMembersTooLarge, wantClass: snapshot.ClassTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := segment.NewMemorySource(map[string][]string{segAlpha: {"a", "b", "c"}})
			if tt.setup != nil {
				tt.setup(src)
			}

			holder, err := snapshot.New(segAlpha, src, tt.opts)
			require.NoError(t, err)
			require.ErrorIs(t, holder.Refresh(context.Background()), tt.wantErr)

			assert.Equal(
				t,
				snapshot.Status{Version: tt.wantVer, LastError: tt.wantClass, Loaded: tt.wantLoaded},
				holder.Status(),
			)
			assert.Equal(t, tt.wantState, holder.Lookup("a"))
		})
	}
}

// repeats is a source that emits every member several times, as SSCAN may.
type repeats struct{ members []string }

func (repeats) Version(context.Context, string) (string, error) { return "v1", nil }

func (r repeats) Members(context.Context, string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		for range 3 {
			for _, member := range r.members {
				if !yield(member, nil) {
					return
				}
			}
		}
	}
}

// The guard counts distinct members and the bytes of distinct members: a repeat costs nothing.
func TestTheGuardCountsDistinctMembersOfTheStream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr     error
		name        string
		opts        snapshot.Options
		wantMembers int
	}{
		{name: "two distinct members, limit two", opts: snapshot.Options{MaxMembers: 2}, wantMembers: 2},
		{
			name:    "two distinct members, limit one",
			opts:    snapshot.Options{MaxMembers: 1},
			wantErr: segment.ErrTooManyMembers,
		},
		{name: "two bytes, limit two bytes", opts: snapshot.Options{MaxBytes: 2}, wantMembers: 2},
		{name: "two bytes, limit one byte", opts: snapshot.Options{MaxBytes: 1}, wantErr: segment.ErrMembersTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			holder, err := snapshot.New(segAlpha, repeats{members: []string{"a", "b"}}, tt.opts)
			require.NoError(t, err)

			err = holder.Refresh(context.Background())
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantMembers, holder.Status().Members, "six rows, two members")
		})
	}
}

func TestAnUnchangedVersionClearsTheLastError(t *testing.T) {
	t.Parallel()

	holder, src := loaded(t, "a")

	src.Fail(segment.OpVersion, errBoom)
	require.ErrorIs(t, holder.Refresh(context.Background()), errBoom)
	assert.Equal(t, snapshot.ClassVersion, holder.Status().LastError)

	src.Fail(segment.OpVersion, nil)
	require.NoError(t, holder.Refresh(context.Background()))
	assert.Equal(t, snapshot.ClassNone, holder.Status().LastError)
	assert.Equal(t, 1, src.Calls(segment.OpMembers), "the members were not pulled again")
}

func TestASourceThatIgnoresItsContextIsNotTrusted(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	holder, err := snapshot.New(segAlpha, deaf{cancel: cancel}, snapshot.Options{})
	require.NoError(t, err)

	require.ErrorIs(t, holder.Refresh(ctx), context.Canceled)
	assert.Equal(t, snapshot.Status{LastError: snapshot.ClassCanceled}, holder.Status())
}

// reportsMissing is a source whose Version says that the segment does not exist while gone is set.
type reportsMissing struct {
	inner *segment.MemorySource
	gone  *atomic.Bool
}

func (r reportsMissing) Version(ctx context.Context, name string) (string, error) {
	if r.gone.Load() {
		return "", segment.ErrSegmentMissing
	}

	return r.inner.Version(ctx, name)
}

func (r reportsMissing) Members(ctx context.Context, name string) iter.Seq2[string, error] {
	return r.inner.Members(ctx, name)
}

func TestAVersionThatReportsMissingIsAnAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		missing       segment.MissingKey
		startGone     bool
		wantLookup    segment.State
		wantLoaded    bool
		wantFirstLook segment.State
	}{
		{
			name:          "present then missing, default",
			wantLookup:    segment.NotMember,
			wantLoaded:    true,
			wantFirstLook: segment.Member,
		},
		{
			name:          "present then missing, unknown",
			missing:       segment.MissingUnknown,
			wantLookup:    segment.Unknown,
			wantFirstLook: segment.Member,
		},
		{
			name:          "missing from the start, default",
			startGone:     true,
			wantLookup:    segment.NotMember,
			wantLoaded:    true,
			wantFirstLook: segment.NotMember,
		},
		{
			name:          "missing from the start, unknown",
			startGone:     true,
			missing:       segment.MissingUnknown,
			wantLookup:    segment.Unknown,
			wantFirstLook: segment.Unknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			gone := &atomic.Bool{}
			src := reportsMissing{inner: segment.NewMemorySource(map[string][]string{segAlpha: {"a"}}), gone: gone}

			holder, err := snapshot.New(segAlpha, src, snapshot.Options{Missing: tt.missing})
			require.NoError(t, err)

			gone.Store(tt.startGone)

			if tt.startGone {
				require.ErrorIs(t, holder.Refresh(ctx), segment.ErrSegmentMissing)
			} else {
				require.NoError(t, holder.Refresh(ctx))
				assert.Equal(t, tt.wantFirstLook, holder.Lookup("a"))

				gone.Store(true)
				require.ErrorIs(t, holder.Refresh(ctx), segment.ErrSegmentMissing, "the answer is returned once")
			}

			assert.Equal(t, tt.wantLookup, holder.Lookup("a"), "the old set is dropped and the policy answers")
			assert.Equal(t, snapshot.ClassMissing, holder.Status().LastError)
			assert.Equal(t, tt.wantLoaded, holder.Status().Loaded)
			require.NoError(t, holder.Refresh(ctx), "already known: no second error")
			assert.Equal(t, snapshot.ClassMissing, holder.Status().LastError)

			gone.Store(false)
			require.NoError(t, holder.Refresh(ctx), "the segment is back")
			assert.Equal(t, segment.Member, holder.Lookup("a"))
			assert.Equal(t, snapshot.ClassNone, holder.Status().LastError)
		})
	}
}

func TestAPollThatWorksRestoresTheTrueStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// a present segment: an outage and then a poll of the same version leave no error
	holder, src := loaded(t, "a")

	src.Fail(segment.OpVersion, errBoom)
	require.ErrorIs(t, holder.Refresh(ctx), errBoom)
	assert.Equal(t, snapshot.ClassVersion, holder.Status().LastError)

	src.Fail(segment.OpVersion, nil)
	require.NoError(t, holder.Refresh(ctx))
	assert.Equal(t, snapshot.ClassNone, holder.Status().LastError)

	// a missing segment: an outage and then a poll of the same version show missing again, not healthy
	src.Delete(segAlpha)
	require.ErrorIs(t, holder.Refresh(ctx), segment.ErrSegmentMissing)
	assert.Equal(t, snapshot.ClassMissing, holder.Status().LastError)

	src.Fail(segment.OpVersion, errBoom)
	require.ErrorIs(t, holder.Refresh(ctx), errBoom)
	assert.Equal(t, snapshot.ClassVersion, holder.Status().LastError)

	src.Fail(segment.OpVersion, nil)
	require.NoError(t, holder.Refresh(ctx))
	assert.Equal(t, snapshot.ClassMissing, holder.Status().LastError, "still missing: the true state")
	assert.Equal(t, segment.NotMember, holder.Lookup("a"))
}

// how a holder learns that a segment is missing.
const (
	byVersion = "version"
	byMembers = "members"
)

func TestASegmentThatComesBackUnderTheSameTokenIsPulled(t *testing.T) {
	t.Parallel()

	const token = "same"

	tests := []struct {
		name      string
		learnedBy string // how the holder learns that the segment is missing: "version" or "members"
		missing   segment.MissingKey
		wantGone  segment.State
	}{
		{name: "missing by Version, default", learnedBy: byVersion, wantGone: segment.NotMember},
		{
			name:      "missing by Version, unknown",
			learnedBy: byVersion,
			missing:   segment.MissingUnknown,
			wantGone:  segment.Unknown,
		},
		{name: "missing by Members, default", learnedBy: byMembers, wantGone: segment.NotMember},
		{
			name:      "missing by Members, unknown",
			learnedBy: byMembers,
			missing:   segment.MissingUnknown,
			wantGone:  segment.Unknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			gone := &atomic.Bool{}
			inner := segment.NewMemorySource(nil)
			inner.PinVersion(segAlpha, token)

			src := reportsMissing{inner: inner, gone: gone}
			gone.Store(tt.learnedBy == byVersion)

			holder, err := snapshot.New(segAlpha, src, snapshot.Options{Missing: tt.missing})
			require.NoError(t, err)

			require.ErrorIs(t, holder.Refresh(ctx), segment.ErrSegmentMissing, "learnt as missing")
			assert.Equal(t, tt.wantGone, holder.Lookup("a"))

			// still missing: a Version that says so pulls nothing; a Version that works asks the members again
			wantPulls := 0
			if tt.learnedBy == byMembers {
				wantPulls = 2
			}

			require.NoError(t, holder.Refresh(ctx), "already known")
			assert.Equal(t, wantPulls, inner.Calls(segment.OpMembers), "exact number of member pulls while missing")

			// the segment comes back under the very same token
			inner.Set(segAlpha, []string{"a"})
			inner.PinVersion(segAlpha, token)
			gone.Store(false)

			require.NoError(t, holder.Refresh(ctx))
			assert.Equal(t, segment.Member, holder.Lookup("a"), "the members are served")
			assert.Equal(t, snapshot.ClassNone, holder.Status().LastError)
			assert.Equal(t, wantPulls+1, inner.Calls(segment.OpMembers))

			// and now that it is present under an unchanged token, nothing is pulled
			require.NoError(t, holder.Refresh(ctx))
			assert.Equal(t, wantPulls+1, inner.Calls(segment.OpMembers))
		})
	}
}

// unversioned is a source with no version: Version says so.
type unversioned struct{ inner *segment.MemorySource }

func (unversioned) Version(context.Context, string) (string, error) { return "", segment.ErrNoVersion }

func (u unversioned) Members(ctx context.Context, name string) iter.Seq2[string, error] {
	return u.inner.Members(ctx, name)
}

func TestWithoutAVersionEveryRefreshPulls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		refreshes     int
		wantPulls     int
		wantAfterEdit segment.State
	}{
		{name: "one refresh", refreshes: 1, wantPulls: 1, wantAfterEdit: segment.Member},
		{name: "three refreshes", refreshes: 3, wantPulls: 3, wantAfterEdit: segment.Member},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			inner := segment.NewMemorySource(map[string][]string{segAlpha: {"a"}})

			holder, err := snapshot.New(segAlpha, unversioned{inner: inner}, snapshot.Options{})
			require.NoError(t, err)

			for range tt.refreshes {
				require.NoError(t, holder.Refresh(ctx))
			}

			assert.Equal(t, tt.wantPulls, inner.Calls(segment.OpMembers), "no version: one pull for each refresh")

			inner.Set(segAlpha, []string{"b"})
			require.NoError(t, holder.Refresh(ctx))
			assert.Equal(t, tt.wantAfterEdit, holder.Lookup("b"), "a change is seen at the next refresh")
			assert.Equal(t, segment.NotMember, holder.Lookup("a"))
			assert.Equal(t, snapshot.ClassNone, holder.Status().LastError)
		})
	}
}

// emptyToken reports a segment missing while gone is set, and a version that is the valid empty token otherwise.
type emptyToken struct {
	inner *segment.MemorySource
	gone  *atomic.Bool
}

func (e emptyToken) Version(context.Context, string) (string, error) {
	if e.gone.Load() {
		return "", segment.ErrSegmentMissing
	}

	return "", nil
}

func (e emptyToken) Members(ctx context.Context, name string) iter.Seq2[string, error] {
	return e.inner.Members(ctx, name)
}

func TestAMissingVersionThenTheEmptyTokenIsPulled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		missing      segment.MissingKey
		wantWhenGone segment.State
	}{
		{name: "default policy", wantWhenGone: segment.NotMember},
		{name: "unknown policy", missing: segment.MissingUnknown, wantWhenGone: segment.Unknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			gone := &atomic.Bool{}
			inner := segment.NewMemorySource(map[string][]string{segAlpha: {"a"}})

			holder, err := snapshot.New(
				segAlpha,
				emptyToken{inner: inner, gone: gone},
				snapshot.Options{Missing: tt.missing},
			)
			require.NoError(t, err)

			gone.Store(true)
			require.ErrorIs(t, holder.Refresh(ctx), segment.ErrSegmentMissing)
			assert.Equal(t, tt.wantWhenGone, holder.Lookup("a"))

			gone.Store(false)
			require.NoError(t, holder.Refresh(ctx))
			assert.Equal(
				t,
				segment.Member,
				holder.Lookup("a"),
				"the empty token of an existing segment is not the missing state",
			)
			assert.Equal(t, snapshot.ClassNone, holder.Status().LastError)
			assert.Equal(t, 1, inner.Calls(segment.OpMembers))
		})
	}
}

// switchable serves a set, and from the moment trip is set its stream ends the context and then reports the segment
// missing, as a source that ignores its context would.
type switchable struct {
	cancel  context.CancelFunc
	trip    *atomic.Bool
	version *atomic.Int64
}

func (s switchable) Version(context.Context, string) (string, error) {
	return strconv.FormatInt(s.version.Load(), 10), nil
}

func (s switchable) Members(context.Context, string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		if s.trip.Load() {
			s.cancel()
			yield("", segment.ErrSegmentMissing)

			return
		}

		yield("a", nil)
	}
}

func TestAMissingResultAfterTheContextEndedKeepsTheLastKnownGood(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	src := switchable{cancel: cancel, trip: &atomic.Bool{}, version: &atomic.Int64{}}

	holder, err := snapshot.New(segAlpha, src, snapshot.Options{})
	require.NoError(t, err)
	require.NoError(t, holder.Refresh(ctx))
	require.Equal(t, segment.Member, holder.Lookup("a"))

	src.trip.Store(true)
	src.version.Store(1) // the version moved: the members are pulled

	err = holder.Refresh(ctx)
	require.ErrorIs(t, err, context.Canceled, "the end of the context is the error, as the status says")
	require.NotErrorIs(t, err, segment.ErrSegmentMissing)
	assert.Equal(t, snapshot.ClassCanceled, holder.Status().LastError, "the cancel decides, not the missing answer")
	assert.Equal(t, segment.Member, holder.Lookup("a"), "the last known good is kept")
}

func TestAHolderReportsStaleOnlyWhileItServesALastKnownGoodSet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// the steps of one holder, in order: each acts on the source and then reads Stale
	steps := []struct {
		act       func(src *segment.MemorySource) error
		name      string
		wantStale bool
	}{
		{name: "fresh", act: func(*segment.MemorySource) error { return nil }},
		{
			name: "the refresh failed, the last known good set answers",
			act: func(src *segment.MemorySource) error {
				src.Fail(segment.OpVersion, errBoom)

				return errBoom
			},
			wantStale: true,
		},
		{
			name: "the outage is over",
			act: func(src *segment.MemorySource) error {
				src.Fail(segment.OpVersion, nil)

				return nil
			},
		},
		{
			name: "a missing segment is an answer, not a stale set",
			act: func(src *segment.MemorySource) error {
				src.Delete(segAlpha)

				return segment.ErrSegmentMissing
			},
		},
	}

	assert.False(t, (&snapshot.Holder{}).Stale(), "nothing held, nothing stale")

	holder, src := loaded(t, "a")

	for _, step := range steps {
		wantErr := step.act(src)
		if wantErr != nil {
			require.ErrorIs(t, holder.Refresh(ctx), wantErr, step.name)
		} else if step.name != "fresh" {
			require.NoError(t, holder.Refresh(ctx), step.name)
		}

		assert.Equal(t, step.wantStale, holder.Stale(), step.name)
	}
}
