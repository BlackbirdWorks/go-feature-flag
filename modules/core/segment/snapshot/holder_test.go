package snapshot_test

import (
	"context"
	"iter"
	"sync"
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
	assert.Equal(t, segment.Unknown, other.Lookup("a"))
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
		setup     func(*segment.MemorySource)
		wantErr   error
		name      string
		wantClass snapshot.Class
		opts      snapshot.Options
	}{
		{
			name: "version fails", setup: func(s *segment.MemorySource) { s.Fail(segment.OpVersion, errBoom) },
			wantErr: errBoom, wantClass: snapshot.ClassVersion,
		},
		{
			name: "stream fails", setup: func(s *segment.MemorySource) { s.FailMembersAfter(1, errBoom) },
			wantErr: errBoom, wantClass: snapshot.ClassRead,
		},
		{name: "segment missing", setup: func(s *segment.MemorySource) { s.Delete("alpha") },
			wantErr: segment.ErrSegmentMissing, wantClass: snapshot.ClassMissing},
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

			assert.Equal(t, snapshot.Status{LastError: tt.wantClass}, holder.Status())
			assert.Equal(t, segment.Unknown, holder.Lookup("a"))
		})
	}
}

func TestDuplicatesDoNotCountAgainstTheGuard(t *testing.T) {
	t.Parallel()

	src := segment.NewMemorySource(map[string][]string{segAlpha: {"a", "a", "a", "b"}})
	holder, err := snapshot.New(segAlpha, src, snapshot.Options{MaxMembers: 2})
	require.NoError(t, err)
	require.NoError(t, holder.Refresh(context.Background()))
	assert.Equal(t, 2, holder.Status().Members)
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
