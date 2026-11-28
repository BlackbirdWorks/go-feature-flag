package snapshot_test

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"sync"
	"testing"
	"time"

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
	steady   bool // after the script is used up every read answers a version that was never seen
}

func (s *scripted) Version(context.Context, string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	i := min(s.reads, len(s.versions)-1)
	read := s.reads
	s.reads++

	if s.err != nil && s.reads == s.errAt {
		return "", s.err
	}

	if s.steady && read >= len(s.versions) {
		return fmt.Sprintf("s%d", read), nil
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
	idOld2 = "old2"
	idNew1 = "new1"
	idNew2 = "new2"
)

// fakeClock is the time of a holder in a test: Sleep moves it forward at once and counts the pauses.
type fakeClock struct {
	onSleep func(n int)
	now     time.Time
	slept   []time.Duration
	mu      sync.Mutex
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeClock) Sleep(ctx context.Context, pause time.Duration) error {
	c.mu.Lock()
	c.slept = append(c.slept, pause)
	count := len(c.slept)
	hook := c.onSleep
	c.mu.Unlock()

	if hook != nil {
		hook(count)
	}

	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("sleep: %w", err)
	}

	c.advance(pause)

	return nil
}

func (c *fakeClock) advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(by)
}

// pauses returns the pauses since the last call.
func (c *fakeClock) pauses() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := c.slept
	c.slept = nil

	return out
}

const (
	debounceTen = 10 * time.Second
	maxPauses   = 100
)

func TestARefreshInstallsASetOnlyOnceItsVersionIsQuiet(t *testing.T) {
	t.Parallel()

	prior := []string{idOld1, idOld2}
	mixed := []string{idOld1, idNew2}
	fresh := []string{idNew1, idNew2}

	tests := []struct {
		name          string
		wantVersion   string
		prior         []string // the set held before, under v0
		versions      []string // the version reads of the refresh under test
		sets          [][]string
		wantMembers   []string
		wantAbsent    []string
		pullTakes     time.Duration
		debounce      time.Duration
		wantPauses    int
		wantReads     int
		wantPulls     int
		wantUnsettled uint64
		wantLoaded    bool
		steady        bool
	}{
		{
			name: "one change during the pull, then quiet: only the consistent second pull is installed", prior: prior,
			debounce: debounceTen, versions: []string{"v1", "v2", "v2", "v2"}, sets: [][]string{mixed, fresh},
			wantVersion: "v2", wantMembers: fresh, wantAbsent: []string{idOld1, idOld2},
			wantPauses: 1, wantReads: 4, wantPulls: 2, wantLoaded: true,
		},
		{
			name: "an unchanged version is one pull and two reads", prior: prior,
			debounce: debounceTen, versions: []string{"v1", "v1"}, sets: [][]string{fresh},
			wantVersion: "v1", wantMembers: fresh, wantAbsent: []string{idOld1},
			wantReads: 2, wantPulls: 1, wantLoaded: true,
		},
		{
			name: "steady edits: two pulls, the latest installed under the version read before it", prior: prior,
			debounce: debounceTen, steady: true, versions: []string{}, sets: [][]string{mixed, fresh},
			wantVersion: "s13", wantMembers: fresh, wantAbsent: []string{idOld1},
			wantPauses: 10, wantReads: 13, wantPulls: 2, wantUnsettled: 1, wantLoaded: true,
		},
		{
			name:     "a first load under steady edits completes once the debounce passed",
			debounce: debounceTen, steady: true, versions: []string{}, sets: [][]string{mixed, fresh},
			wantVersion: "s11", wantMembers: fresh, wantAbsent: []string{idOld1},
			wantPauses: 10, wantReads: 13, wantPulls: 2, wantUnsettled: 1, wantLoaded: true,
		},
		{
			name: "a pull that takes longer than the debounce is installed at once", prior: prior,
			debounce: debounceTen, pullTakes: 2 * debounceTen, versions: []string{"v1", "v2"}, sets: [][]string{fresh},
			wantVersion: "v1", wantMembers: fresh, wantAbsent: []string{idOld1},
			wantReads: 2, wantPulls: 1, wantUnsettled: 1, wantLoaded: true,
		},
		{
			name: "no debounce: the first pull is installed, one version read", prior: prior,
			versions: []string{"v1", "v2", "v3"}, sets: [][]string{mixed, fresh},
			wantVersion: "v1", wantMembers: mixed, wantAbsent: []string{idOld2},
			wantReads: 1, wantPulls: 1, wantLoaded: true,
		},
		{
			name:     "no debounce on a first load: one pull, one read",
			versions: []string{"v1", "v2"}, sets: [][]string{fresh},
			wantVersion: "v1", wantMembers: fresh, wantReads: 1, wantPulls: 1, wantLoaded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// a refresh that never ends is stopped after the pauses and pulls that a debounce allows, so it fails the row
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)

			clk := newFakeClock()
			clk.onSleep = func(count int) {
				if count > maxPauses {
					cancel()
				}
			}

			warm, warmRead := 0, warmReads
			if tt.debounce == 0 {
				warmRead = 1 // no debounce: the warm refresh does not read the version again
			}

			src := &scripted{versions: tt.versions, sets: tt.sets, steady: tt.steady}
			if tt.prior != nil {
				warm = 1
				src.versions = slices.Concat([]string{"v0", "v0"}[:warmRead], tt.versions)
				src.sets = slices.Concat([][]string{tt.prior}, tt.sets)
			}

			if tt.steady && len(src.versions) == 0 {
				src.versions = []string{"x"} // a first load: the first read, then reads that were never seen
			}

			src.onPull = func(count int) {
				clk.advance(tt.pullTakes)

				if count > maxPauses {
					cancel() // pulls with no pause between them are a refresh that never waits
				}
			}

			holder, err := snapshot.New(segAlpha, src, snapshot.Options{Debounce: tt.debounce, Clock: clk})
			require.NoError(t, err)

			if warm == 1 {
				require.NoError(t, holder.Refresh(ctx))
				clk.pauses()
			}

			require.NoError(t, holder.Refresh(ctx))

			reads, pulls := src.counts()
			assert.Equal(t, tt.wantReads+warm*warmRead, reads, "version reads")
			assert.Equal(t, tt.wantPulls+warm*warmPulls, pulls, "pulls")

			pauses := clk.pauses()
			assert.Len(t, pauses, tt.wantPauses)

			for _, pause := range pauses {
				assert.Equal(t, snapshot.QuietStep(tt.debounce), pause)
			}

			status := holder.Status()
			assert.Equal(t, snapshot.ClassNone, status.LastError)
			assert.Equal(t, tt.wantVersion, status.Version)
			assert.Equal(t, tt.wantLoaded, status.Loaded)
			assert.Equal(t, tt.wantUnsettled, status.Unsettled)
			assertMembers(t, holder, tt.wantMembers, tt.wantAbsent)

			_, answer := holder.Answer("probe")
			assert.Equal(t, segment.StatusHealthy, answer, "a set installed without a stable version is not stale")
		})
	}
}

// assertMembers checks the held set: exactly the members, and none of the absent ids.
func assertMembers(t *testing.T, holder *snapshot.Holder, members, absent []string) {
	t.Helper()

	assert.Len(t, members, holder.Status().Members)

	for _, id := range members {
		assert.Equal(t, segment.Member, holder.Lookup(id), id)
	}

	for _, id := range absent {
		assert.NotEqual(t, segment.Member, holder.Lookup(id), id)
	}
}

func TestTheQuietStepIsATenthOfTheDebounceBetweenOneMillisecondAndOneSecond(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		debounce time.Duration
		want     time.Duration
	}{
		{"short debounce", 50 * time.Millisecond, 5 * time.Millisecond},
		{"tiny debounce is never zero", time.Nanosecond, time.Millisecond},
		{"long debounce is capped", time.Hour, time.Second},
		{"ten seconds", debounceTen, time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, snapshot.QuietStep(tt.debounce))
		})
	}
}

func TestADebounceThatIsChangedAppliesToTheNextRefresh(t *testing.T) {
	t.Parallel()

	src := &scripted{versions: []string{"v1", "v2", "v3"}, sets: [][]string{{idOld1}}}

	holder, err := snapshot.New(segAlpha, src, snapshot.Options{Debounce: debounceTen, Clock: newFakeClock()})
	require.NoError(t, err)

	holder.SetDebounce(-time.Second) // zero or less is off

	require.NoError(t, holder.Refresh(t.Context()))

	reads, pulls := src.counts()
	assert.Equal(t, 1, reads)
	assert.Equal(t, 1, pulls)
}

func TestAStoreThatAnswersMissingWhileTheRefreshWaitsIsMissingAtOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		errAt     int // the version read that reports the segment missing, counting the warm refresh
		wantReads int
		wantPulls int
	}{
		{
			name:      "on the re-read after the pull",
			errAt:     warmReads + 2,
			wantReads: warmReads + 2,
			wantPulls: warmPulls + 1,
		},
		{name: "while waiting for quiet", errAt: warmReads + 3, wantReads: warmReads + 3, wantPulls: warmPulls + 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := &scripted{
				versions: []string{"v0", "v0", "v1", "v2", "v3"}, sets: [][]string{{idOld1}, {idNew1}},
				err: segment.ErrSegmentMissing, errAt: tt.errAt,
			}

			holder, err := snapshot.New(segAlpha, src, snapshot.Options{Debounce: debounceTen, Clock: newFakeClock()})
			require.NoError(t, err)
			require.NoError(t, holder.Refresh(t.Context()))

			require.ErrorIs(t, holder.Refresh(t.Context()), segment.ErrSegmentMissing)

			reads, pulls := src.counts()
			assert.Equal(t, tt.wantReads, reads)
			assert.Equal(t, tt.wantPulls, pulls, "no pull after the answer")
			assert.Equal(t, snapshot.ClassMissing, holder.Status().LastError)
			assert.Equal(t, segment.NotMember, holder.Lookup(idOld1), "the held set is dropped")
			assert.Equal(t, segment.NotMember, holder.Lookup(idNew1))

			_, answer := holder.Answer("probe")
			assert.Equal(t, segment.StatusHealthy, answer)
		})
	}
}

func TestARefreshThatEndsBeforeTheDebounceKeepsThePreviousSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		endInWait bool
		wantPulls int
	}{
		{name: "during the second pull", wantPulls: warmPulls + 2},
		{name: "during the wait for quiet", endInWait: true, wantPulls: warmPulls + 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)

			clk := newFakeClock()
			src := &scripted{
				versions: []string{"v0", "v0", "v1", "v2", "v2"},
				sets:     [][]string{{idOld1}, {idOld1, idNew1}, {idNew1}},
				onPull: func(n int) {
					if n == 3 && !tt.endInWait { // the second pull of the refresh under test
						cancel()
					}
				},
			}

			if tt.endInWait {
				clk.onSleep = func(int) { cancel() }
			}

			holder, err := snapshot.New(segAlpha, src, snapshot.Options{Debounce: debounceTen, Clock: clk})
			require.NoError(t, err)
			require.NoError(t, holder.Refresh(ctx))

			require.ErrorIs(t, holder.Refresh(ctx), context.Canceled)

			_, pulls := src.counts()
			assert.Equal(t, tt.wantPulls, pulls)
			assert.Equal(t, snapshot.ClassCanceled, holder.Status().LastError)
			assert.Equal(t, "v0", holder.Status().Version)
			assert.Equal(t, segment.Member, holder.Lookup(idOld1))
			assert.Equal(t, segment.NotMember, holder.Lookup(idNew1))
		})
	}
}

func TestAFailedRereadKeepsThePreviousSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		errAt int
	}{
		{name: "after the pull", errAt: warmReads + 2},
		{name: "while waiting for quiet", errAt: warmReads + 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := &scripted{
				versions: []string{"v0", "v0", "v1", "v2", "v3"}, sets: [][]string{{idOld1}, {idNew1}},
				err: errBoom, errAt: tt.errAt,
			}

			holder, err := snapshot.New(segAlpha, src, snapshot.Options{Debounce: debounceTen, Clock: newFakeClock()})
			require.NoError(t, err)
			require.NoError(t, holder.Refresh(t.Context()))

			require.ErrorIs(t, holder.Refresh(t.Context()), errBoom)
			assert.Equal(t, snapshot.ClassVersion, holder.Status().LastError)
			assert.Equal(t, "v0", holder.Status().Version)
			assert.Equal(t, segment.NotMember, holder.Lookup(idNew1))
		})
	}
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
