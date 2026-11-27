package segmenttest

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// VersionerFixture is a store under test: the versioner, and the way to write to the store behind it.
type VersionerFixture struct {
	// Versioner is the implementation under test.
	Versioner segment.Versioner
	// Seed writes members to the store and must change the version of the segment.
	Seed Seed
	// Close (optional) releases the store after the case.
	Close func()
}

// VersionerFactory builds a fixture of an empty store for a case.
type VersionerFactory func() (VersionerFixture, error)

// The names of the cases of [RunVersioner].
const (
	CaseVerStable      = "an-unchanged-segment-has-a-stable-version"
	CaseVerMoves       = "a-write-moves-the-version"
	CaseVerPerSegment  = "versions-are-per-segment"
	CaseVerAbsent      = "an-absent-segment-has-a-stable-token-or-is-missing"
	CaseVerCtxEnded    = "an-ended-context-fails"
	CaseVerConcurrency = "concurrent-reads-agree"
)

const (
	verWorkers = 8
	verRounds  = 25
)

// RunVersioner runs the conformance suite of [segment.Versioner] against the implementation that the factory
// builds, once per case. Tokens are opaque: the suite only compares them for equality.
func RunVersioner(t *testing.T, factory VersionerFactory) {
	t.Helper()

	cases := []struct {
		run  func(*testing.T, VersionerFixture)
		name string
	}{
		{verStable, CaseVerStable},
		{verMoves, CaseVerMoves},
		{verPerSegment, CaseVerPerSegment},
		{verAbsent, CaseVerAbsent},
		{verCtxEnded, CaseVerCtxEnded},
		{verConcurrency, CaseVerConcurrency},
	}

	for _, tcase := range cases {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			fixture, err := factory()
			require.NoError(t, err, "the factory builds a fixture")

			if fixture.Close != nil {
				t.Cleanup(fixture.Close)
			}

			tcase.run(t, fixture)
		})
	}
}

func verStable(t *testing.T, fix VersionerFixture) {
	t.Helper()

	require.NoError(t, fix.Seed("alpha", []string{"a"}))

	first, err := fix.Versioner.Version(context.Background(), "alpha")
	require.NoError(t, err)

	for range verRounds {
		again, againErr := fix.Versioner.Version(context.Background(), "alpha")
		require.NoError(t, againErr)
		assert.Equal(t, first, again)
	}
}

func verMoves(t *testing.T, fix VersionerFixture) {
	t.Helper()

	ctx := context.Background()

	require.NoError(t, fix.Seed("alpha", []string{"a"}))

	before, err := fix.Versioner.Version(ctx, "alpha")
	require.NoError(t, err)

	require.NoError(t, fix.Seed("alpha", []string{"a", "b"}))

	after, err := fix.Versioner.Version(ctx, "alpha")
	require.NoError(t, err)
	assert.NotEqual(t, before, after)
}

func verPerSegment(t *testing.T, fix VersionerFixture) {
	t.Helper()

	ctx := context.Background()

	require.NoError(t, fix.Seed("alpha", []string{"a"}))
	require.NoError(t, fix.Seed("beta", []string{"b"}))

	alpha, err := fix.Versioner.Version(ctx, "alpha")
	require.NoError(t, err)

	require.NoError(t, fix.Seed("beta", []string{"b", "c"}))

	alphaAfter, err := fix.Versioner.Version(ctx, "alpha")
	require.NoError(t, err)
	assert.Equal(t, alpha, alphaAfter, "a write to beta does not move the version of alpha")
}

func verAbsent(t *testing.T, fix VersionerFixture) {
	t.Helper()

	ctx := context.Background()

	first, err := fix.Versioner.Version(ctx, "ghost")
	if err != nil {
		require.ErrorIs(t, err, segment.ErrSegmentMissing, "an absent segment is missing, not a failure")

		return
	}

	again, againErr := fix.Versioner.Version(ctx, "ghost")
	require.NoError(t, againErr)
	assert.Equal(t, first, again, "the token of an absent segment is fixed")

	require.NoError(t, fix.Seed("ghost", []string{"a"}))

	created, createdErr := fix.Versioner.Version(ctx, "ghost")
	require.NoError(t, createdErr)
	assert.NotEqual(t, first, created, "creating the segment moves it off the absent token")
}

func verCtxEnded(t *testing.T, fix VersionerFixture) {
	t.Helper()

	require.NoError(t, fix.Seed("alpha", []string{"a"}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := fix.Versioner.Version(ctx, "alpha")
	require.ErrorIs(t, err, context.Canceled)
}

func verConcurrency(t *testing.T, fix VersionerFixture) {
	t.Helper()

	require.NoError(t, fix.Seed("alpha", []string{"a"}))

	want, err := fix.Versioner.Version(context.Background(), "alpha")
	require.NoError(t, err)

	var (
		wait sync.WaitGroup
		errs [verWorkers]error
		bad  [verWorkers]int
	)

	for worker := range verWorkers {
		wait.Go(func() {
			for range verRounds {
				got, callErr := fix.Versioner.Version(context.Background(), "alpha")
				if callErr != nil {
					errs[worker] = callErr

					return
				}

				if got != want {
					bad[worker]++
				}
			}
		})
	}

	wait.Wait()

	assert.Equal(t, [verWorkers]error{}, errs, "no call failed")
	assert.Equal(t, [verWorkers]int{}, bad, "every call saw the same token")
}
