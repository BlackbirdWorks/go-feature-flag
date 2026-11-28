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

// scripted is a source that answers the version reads and the pulls from a script, in order. The last version read and
// the last set repeat when the script is used up. onPull runs at the start of each pull, with its number from 1.
type scripted struct {
	onPull   func(n int)
	err      error // the error of the version read that has the number errAt (from 1)
	versions []string
	sets     [][]string
	errAt    int
	mu       sync.Mutex
	reads    int
	pulls    int
}

func (s *scripted) Version(context.Context, string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	i := min(s.reads, len(s.versions)-1)
	s.reads++

	if s.err != nil && s.reads == s.errAt {
		return "", s.err
	}

	return s.versions[i], nil
}

func (s *scripted) Members(context.Context, string) iter.Seq2[string, error] {
	s.mu.Lock()
	s.pulls++
	pull := s.pulls
	set := s.sets[min(pull-1, len(s.sets)-1)]
	hook := s.onPull
	s.mu.Unlock()

	return func(yield func(string, error) bool) {
		if hook != nil {
			hook(pull)
		}

		for _, id := range set {
			if !yield(id, nil) {
				return
			}
		}
	}
}

func (s *scripted) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.reads, s.pulls
}

// warmReads and warmPulls are the calls of the first refresh that loads the prior set.
const (
	warmReads = 2
	warmPulls = 1
)

const (
	idOld1 = "old1"
	idNew1 = "new1"
	idNew2 = "new2"
)

func TestARefreshNeverInstallsASetThatChangedDuringItsPull(t *testing.T) {
	t.Parallel()

	prior := []string{idOld1, "old2"}
	mixed := []string{idOld1, idNew2}
	fresh := []string{idNew1, idNew2}
	moving := []string{"v1", "v2", "v3", "v4", "v5", "v6"}

	tests := []struct {
		name        string
		prior       []string // the set held before, under v0
		versions    []string // the version reads of the refresh under test
		sets        [][]string
		wantErr     error
		wantClass   snapshot.Class
		wantVersion string
		wantMembers []string
		wantAbsent  []string
		wantReads   int
		wantPulls   int
		wantLoaded  bool
		wantStatus  segment.Status
	}{
		{
			name: "the version moves during the pull, the next try installs the new set", prior: prior,
			versions: []string{"v1", "v2", "v2"}, sets: [][]string{mixed, fresh},
			wantVersion: "v2", wantMembers: fresh, wantAbsent: []string{idOld1, "old2"},
			wantReads: 3, wantPulls: 2, wantLoaded: true, wantStatus: segment.StatusHealthy,
		},
		{
			name: "an unchanged version is one pull and two reads", prior: prior,
			versions: []string{"v1", "v1"}, sets: [][]string{fresh},
			wantVersion: "v1", wantMembers: fresh, wantAbsent: []string{idOld1},
			wantReads: 2, wantPulls: 1, wantLoaded: true, wantStatus: segment.StatusHealthy,
		},
		{
			name: "a version that moves on every read keeps the previous set", prior: prior,
			versions: moving, sets: [][]string{mixed},
			wantErr: snapshot.ErrUnsettled, wantClass: snapshot.ClassSet, wantVersion: "v0",
			wantMembers: prior, wantAbsent: []string{idNew2},
			wantReads: 1 + snapshot.MaxPullTries, wantPulls: snapshot.MaxPullTries, wantLoaded: true,
			wantStatus: segment.StatusStale,
		},
		{
			name:     "a first load that never settles answers as before a first load",
			versions: moving, sets: [][]string{mixed},
			wantErr: snapshot.ErrUnsettled, wantClass: snapshot.ClassSet, wantAbsent: []string{idOld1, idNew2},
			wantReads: 1 + snapshot.MaxPullTries, wantPulls: snapshot.MaxPullTries,
			wantStatus: segment.StatusStoreError,
		},
		{
			name:     "a first load that settles on the second try installs that set",
			versions: []string{"v1", "v2", "v2"}, sets: [][]string{mixed, fresh},
			wantVersion: "v2", wantMembers: fresh, wantAbsent: []string{idOld1},
			wantReads: 3, wantPulls: 2, wantLoaded: true, wantStatus: segment.StatusHealthy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := &scripted{versions: tt.versions, sets: tt.sets}
			warm := 0

			if tt.prior != nil {
				src = &scripted{
					versions: append([]string{"v0", "v0"}, tt.versions...),
					sets:     append([][]string{tt.prior}, tt.sets...),
				}
				warm = 1
			}

			holder, err := snapshot.New(segAlpha, src, snapshot.Options{})
			require.NoError(t, err)

			if warm == 1 {
				require.NoError(t, holder.Refresh(t.Context()))
			}

			err = holder.Refresh(t.Context())
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}

			reads, pulls := src.counts()
			assert.Equal(t, tt.wantReads+warm*warmReads, reads, "version reads")
			assert.Equal(t, tt.wantPulls+warm*warmPulls, pulls, "pulls")

			status := holder.Status()
			assert.Equal(t, tt.wantClass, status.LastError)
			assert.Equal(t, tt.wantVersion, status.Version)
			assert.Equal(t, tt.wantLoaded, status.Loaded)
			assert.Len(t, tt.wantMembers, status.Members)

			for _, id := range tt.wantMembers {
				assert.Equal(t, segment.Member, holder.Lookup(id), id)
			}

			for _, id := range tt.wantAbsent {
				assert.NotEqual(t, segment.Member, holder.Lookup(id), id)
			}

			_, answer := holder.Answer("probe")
			assert.Equal(t, tt.wantStatus, answer)
		})
	}
}

func TestARefreshThatEndsDuringARetryKeepsThePreviousSet(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	src := &scripted{
		versions: []string{"v0", "v0", "v1", "v2", "v3"},
		sets:     [][]string{{idOld1}, {idOld1, idNew1}, {idNew1}},
		onPull: func(n int) {
			if n == 3 { // the second try of the refresh under test
				cancel()
			}
		},
	}

	holder, err := snapshot.New(segAlpha, src, snapshot.Options{})
	require.NoError(t, err)
	require.NoError(t, holder.Refresh(ctx))

	require.ErrorIs(t, holder.Refresh(ctx), context.Canceled)

	_, pulls := src.counts()
	assert.Equal(t, warmPulls+2, pulls, "no third try after the context ended")
	assert.Equal(t, snapshot.ClassCanceled, holder.Status().LastError)
	assert.Equal(t, "v0", holder.Status().Version)
	assert.Equal(t, segment.Member, holder.Lookup(idOld1))
	assert.Equal(t, segment.NotMember, holder.Lookup(idNew1))
}

func TestAFailedRereadKeepsThePreviousSet(t *testing.T) {
	t.Parallel()

	src := &scripted{
		versions: []string{"v0", "v0", "v1", "v1"}, sets: [][]string{{idOld1}, {idNew1}},
		err: errBoom, errAt: warmReads + 2, // the read after the pull of the refresh under test
	}

	holder, err := snapshot.New(segAlpha, src, snapshot.Options{})
	require.NoError(t, err)
	require.NoError(t, holder.Refresh(t.Context()))

	require.ErrorIs(t, holder.Refresh(t.Context()), errBoom)
	assert.Equal(t, snapshot.ClassVersion, holder.Status().LastError)
	assert.Equal(t, "v0", holder.Status().Version)
	assert.Equal(t, segment.NotMember, holder.Lookup(idNew1))
}

func TestASourceWithoutAVersionIsPulledOnceForEveryRefresh(t *testing.T) {
	t.Parallel()

	inner := segment.NewMemorySource(map[string][]string{segAlpha: {"a", "b"}})
	src := unversioned{inner: inner}

	holder, err := snapshot.New(segAlpha, src, snapshot.Options{})
	require.NoError(t, err)

	for range 2 {
		require.NoError(t, holder.Refresh(t.Context()))
	}

	assert.Equal(t, 2, inner.Calls(segment.OpMembers), "one pull for each refresh, no second try")
	assert.Equal(t, segment.Member, holder.Lookup("a"))
}
