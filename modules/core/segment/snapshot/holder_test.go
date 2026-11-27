package snapshot_test

import (
	"context"
	"iter"
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
