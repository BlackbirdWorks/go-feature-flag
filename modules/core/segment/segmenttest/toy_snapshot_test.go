package segmenttest_test

import (
	"context"
	"errors"
	"iter"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmenttest"
)

// brokenSource wraps a correct source and breaks one rule of the contract. The suites must fail the named cases for
// each defect: a suite that passes a broken source proves nothing about a good one.
type brokenSource struct {
	inner  *segment.MemorySource
	defect string
	mu     sync.Mutex
	calls  int
}

const (
	defMissingIsEmpty   = "yields nothing for a missing segment"
	defIgnoresCtx       = "ignores a context that ended"
	defVersionConstant  = "the version never changes"
	defEmitsAfterError  = "yields members after its error"
	defVersionEveryCall = "the version changes on every call"
	defGlobalVersion    = "one version for every segment"
)

func (b *brokenSource) Version(ctx context.Context, name string) (string, error) {
	switch b.defect {
	case defVersionConstant:
		return "1", nil
	case defIgnoresCtx:
		return b.inner.Version(context.WithoutCancel(ctx), name)
	case defVersionEveryCall:
		b.mu.Lock()
		b.calls++
		n := b.calls
		b.mu.Unlock()

		return strconv.Itoa(n), nil
	case defGlobalVersion:
		var total int

		for _, seg := range []string{"alpha", "beta", "empty", "ghost"} {
			token, _ := b.inner.Version(ctx, seg)
			total += len(token)

			for _, c := range token {
				total += int(c)
			}
		}

		return strconv.Itoa(total), nil
	}

	return b.inner.Version(ctx, name)
}

func (b *brokenSource) Members(ctx context.Context, name string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		streamCtx := ctx
		if b.defect == defIgnoresCtx {
			streamCtx = context.WithoutCancel(ctx)
		}

		for id, err := range b.inner.Members(streamCtx, name) {
			if errors.Is(err, segment.ErrSegmentMissing) && b.defect == defMissingIsEmpty {
				return
			}

			if !yield(id, err) {
				return
			}

			if err != nil && b.defect == defEmitsAfterError {
				yield("after-error", nil)

				return
			}
		}
	}
}

func brokenFixture(defect string) segmenttest.SnapshotFixture {
	src := segment.NewMemorySource(nil)

	return segmenttest.SnapshotFixture{
		Source: &brokenSource{inner: src, defect: defect}, Seed: seedOf(src),
		Remove: removeOf(src),
	}
}

func failedCases(failed map[string][]string) []string {
	names := make([]string, 0, len(failed))
	for name := range failed {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

//nolint:paralleltest // the suite measures the allocations of the whole process in one case
func TestSnapshotSuiteCatchesBrokenSources(t *testing.T) {
	tests := []struct {
		defect    string
		wantCases []string
	}{
		{defect: "none", wantCases: []string{}},
		{
			defect: defMissingIsEmpty,
			wantCases: []string{
				segmenttest.CaseSnapNeverLoaded, segmenttest.CaseSnapGone, segmenttest.CaseSnapEmptyVsMissing,
			},
		},
		{defect: defIgnoresCtx, wantCases: []string{segmenttest.CaseSnapCtxEnded}},
		{
			defect: defVersionConstant,
			wantCases: []string{
				segmenttest.CaseSnapCancelMidStream, segmenttest.CaseSnapLastKnownGood, segmenttest.CaseSnapGone,
				segmenttest.CaseSnapCtxEnded, segmenttest.CaseSnapNoRepull, segmenttest.CaseSnapMemoryGuard,
				segmenttest.CaseSnapVersionMoves, segmenttest.CaseSnapChangedInPull,
			},
		},
		{defect: defEmitsAfterError, wantCases: []string{segmenttest.CaseSnapEmptyVsMissing}},
	}

	for _, tt := range tests {
		t.Run(tt.defect, func(t *testing.T) {
			failed := segmenttest.CheckSnapshotSource(func() (segmenttest.SnapshotFixture, error) {
				return brokenFixture(tt.defect), nil
			})

			assert.Equal(t, sorted(tt.wantCases), failedCases(failed))
		})
	}
}

func sorted(names []string) []string {
	out := slices.Clone(names)
	slices.Sort(out)

	return out
}

func TestVersionerSuiteCatchesBrokenVersioners(t *testing.T) {
	t.Parallel()

	tests := []struct {
		defect    string
		wantCases []string
	}{
		{defect: "none", wantCases: []string{}},
		{
			defect:    defVersionConstant,
			wantCases: []string{segmenttest.CaseVerMoves, segmenttest.CaseVerAbsent, segmenttest.CaseVerCtxEnded},
		},
		{
			defect: defVersionEveryCall,
			wantCases: []string{
				segmenttest.CaseVerAbsent, segmenttest.CaseVerCtxEnded, segmenttest.CaseVerStable,
				segmenttest.CaseVerConcurrency, segmenttest.CaseVerPerSegment,
			},
		},
		{defect: defGlobalVersion, wantCases: []string{segmenttest.CaseVerPerSegment, segmenttest.CaseVerCtxEnded}},
		{defect: defIgnoresCtx, wantCases: []string{segmenttest.CaseVerCtxEnded}},
	}

	for _, tt := range tests {
		t.Run(tt.defect, func(t *testing.T) {
			t.Parallel()

			failed := segmenttest.CheckVersioner(func() (segmenttest.VersionerFixture, error) {
				fixture := brokenFixture(tt.defect)

				return segmenttest.VersionerFixture{Versioner: fixture.Source, Seed: fixture.Seed}, nil
			})

			assert.Equal(t, sorted(tt.wantCases), failedCases(failed))
		})
	}
}

func removeOf(src *segment.MemorySource) func(string) error {
	return func(name string) error {
		src.Delete(name)

		return nil
	}
}
