package segmenttest_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmenttest"
)

var errToy = errors.New("toy failure")

const reasonNoStore = "no store"

// The defects of the toy provider. The suite has to fail the named case for each of them: a suite that passes a
// broken provider proves nothing about a good one.
const (
	defectNone            = "none"
	defectReversesStates  = "answers in the reverse order"
	defectAnswersGhost    = "answers a segment it does not hold"
	defectChangesID       = "lower-cases the id of the caller"
	defectDuplicates      = "leaves the second duplicate unknown"
	defectEmptyIsError    = "an empty call is an error"
	defectTruncates       = "answers only the first 256 lookups"
	defectEmptyIsMember   = "an empty id is a member"
	defectPartialWrong    = "a failed lookup is a non member"
	defectFailureAnswers  = "answers while failing"
	defectNeverRecovers   = "stays failed after Heal"
	defectIgnoresEndedCtx = "answers wrongly for a context that ended"
	defectIgnoresCtx      = "ignores the context while it hangs"
	defectRetainsSlice    = "keeps writing the slice after the return"
	defectRace            = "fails when two calls overlap"
)

// toy is a correct in-memory provider with one defect that a test switches on.
type toy struct {
	members  map[string]map[string]bool
	failSeg  map[string]bool
	gate     chan struct{}
	entered  chan struct{}
	defect   string
	kept     [][]segment.Lookup
	inflight int
	mu       sync.Mutex
	failAll  bool
	released bool
}

func newToy(data segmenttest.Data, defect string) *toy {
	toyProvider := &toy{
		members: map[string]map[string]bool{}, failSeg: map[string]bool{}, defect: defect,
		entered: make(chan struct{}, 64),
	}

	for name, ids := range data {
		toyProvider.members[name] = map[string]bool{}
		for _, id := range ids {
			toyProvider.members[name][id] = true
		}
	}

	return toyProvider
}

func (p *toy) Fail() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.failAll = true
}

func (p *toy) FailSegment(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.failSeg[name] = true
}

func (p *toy) Hang() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.gate = make(chan struct{})

	return p.entered
}

func (p *toy) Release() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.gate != nil && !p.released {
		p.released = true

		close(p.gate)
	}
}

func (p *toy) Heal() {
	if p.defect == defectNeverRecovers {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.failAll = false
	p.failSeg = map[string]bool{}
}

type toyConfig struct {
	gate    chan struct{}
	failSeg map[string]bool
	failAll bool
}

func (p *toy) Contains(ctx context.Context, lookups []segment.Lookup) error {
	cfg := p.snapshot()

	if len(lookups) == 0 {
		if p.defect == defectEmptyIsError {
			return errToy
		}

		return nil
	}

	err := p.waitGate(ctx, cfg.gate)
	if err != nil {
		return err
	}

	if p.defect == defectIgnoresEndedCtx && ctx.Err() != nil {
		for i := range lookups {
			lookups[i].State = segment.Member
		}

		return nil
	}

	if cfg.failAll && p.defect != defectFailureAnswers {
		return errToy
	}

	if p.defect == defectRace && p.overlaps() {
		return errToy
	}

	err = p.answer(lookups, cfg)
	p.applyDefects(lookups)

	return err
}

func (p *toy) answer(lookups []segment.Lookup, cfg toyConfig) error {
	var err error

	limit := len(lookups)
	if p.defect == defectTruncates {
		limit = min(limit, 256)
	}

	for i := range limit {
		oneErr := p.answerOne(&lookups[i], cfg)
		if oneErr != nil {
			err = oneErr
		}
	}

	return err
}

func (p *toy) answerOne(look *segment.Lookup, cfg toyConfig) error {
	set, held := p.members[look.Segment]

	if p.defect == defectChangesID {
		look.ID = toLower(look.ID)
	}

	switch {
	case cfg.failSeg[look.Segment]:
		if p.defect == defectPartialWrong {
			look.State = segment.NotMember
		}

		return errToy
	case cfg.failAll:
		look.State = segment.NotMember

		return errToy
	case !held && p.defect == defectAnswersGhost:
		look.State = segment.NotMember
	case !held:
	case set[look.ID] || (p.defect == defectEmptyIsMember && look.ID == ""):
		look.State = segment.Member
	default:
		look.State = segment.NotMember
	}

	return nil
}

// applyDefects changes the answers after they were computed, for the defects that act on the whole call.
func (p *toy) applyDefects(lookups []segment.Lookup) {
	switch p.defect {
	case defectReversesStates:
		for i, j := 0, len(lookups)-1; i < j; i, j = i+1, j-1 {
			lookups[i].State, lookups[j].State = lookups[j].State, lookups[i].State
		}
	case defectDuplicates:
		if len(lookups) > 1 && lookups[0].ID == lookups[1].ID && lookups[0].Segment == lookups[1].Segment {
			lookups[1].State = segment.Unknown
		}
	case defectRetainsSlice:
		// only the lookups of the case that tests this keep it, so that no other case races with the retained slice
		if len(lookups) == 2 && lookups[1].ID == "only-beta" {
			p.mu.Lock()
			defer p.mu.Unlock()

			for _, kept := range p.kept {
				kept[0].State = segment.Unknown
			}

			p.kept = append(p.kept, lookups)
		}
	}
}

// overlaps reports whether another call is running at the same time. It yields while it runs so that the calls of
// a case that starts several goroutines overlap.
func (p *toy) overlaps() bool {
	p.mu.Lock()
	p.inflight++
	overlapped := p.inflight > 1
	p.mu.Unlock()

	for range 200 {
		runtime.Gosched()
	}

	p.mu.Lock()
	overlapped = overlapped || p.inflight > 1
	p.inflight--
	p.mu.Unlock()

	return overlapped
}

func toLower(s string) string {
	out := []byte(s)

	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + 'a' - 'A'
		}
	}

	return string(out)
}

func toyFactory(defect string) segmenttest.Factory {
	return func(data segmenttest.Data) (segmenttest.Fixture, error) {
		provider := newToy(data, defect)

		return segmenttest.Fixture{
			Provider: provider, Backend: provider, Bound: 300 * time.Millisecond,
			Close: provider.Release,
		}, nil
	}
}

func TestSuitePassesACorrectProvider(t *testing.T) {
	t.Parallel()

	report := segmenttest.Run(t, toyFactory(defectNone))

	assert.Equal(t, segmenttest.CaseNames(), report.Ran)
	assert.Empty(t, report.Failed)
	assert.Empty(t, report.Unsupported)
}

func TestSuiteCatchesEachDefect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		defect   string
		wantCase string
	}{
		{defectReversesStates, segmenttest.CasePositions},
		{defectAnswersGhost, segmenttest.CaseUnknownSegment},
		{defectChangesID, segmenttest.CaseInputUntouched},
		{defectDuplicates, segmenttest.CaseDuplicates},
		{defectEmptyIsError, segmenttest.CaseEmptyCall},
		{defectTruncates, segmenttest.CaseLargeBatch},
		{defectEmptyIsMember, segmenttest.CaseOddIDs},
		{defectPartialWrong, segmenttest.CasePartialFailure},
		{defectFailureAnswers, segmenttest.CaseFailure},
		{defectNeverRecovers, segmenttest.CaseRecovers},
		{defectIgnoresEndedCtx, segmenttest.CaseCtxEndedBefore},
		{defectIgnoresCtx, segmenttest.CaseCtxCancelDuring},
		{defectRetainsSlice, segmenttest.CaseSliceNotRetained},
		{defectRace, segmenttest.CaseConcurrent},
	}

	for _, tt := range tests {
		t.Run(tt.defect, func(t *testing.T) {
			t.Parallel()

			report := segmenttest.Check(toyFactory(tt.defect))

			assert.Contains(t, report.Failed, tt.wantCase, "the case that must catch %q", tt.defect)
			assert.NotEmpty(t, report.Failed[tt.wantCase])
		})
	}
}

func TestWaiversAreReportedNotSilentlySkipped(t *testing.T) {
	t.Parallel()

	factory := func(data segmenttest.Data) (segmenttest.Fixture, error) {
		return segmenttest.Fixture{
			Provider: newToy(data, defectNone),
			Unsupported: segmenttest.NewWaivers().
				With(segmenttest.CapFail, reasonNoStore).
				With(segmenttest.CapPartial, reasonNoStore).
				With(segmenttest.CapHang, reasonNoStore),
		}, nil
	}

	report := segmenttest.Check(factory)

	assert.Empty(t, report.Failed)
	assert.Equal(t, map[string]string{
		segmenttest.CasePartialFailure:  reasonNoStore,
		segmenttest.CaseFailure:         reasonNoStore,
		segmenttest.CaseRecovers:        reasonNoStore,
		segmenttest.CaseCtxCancelDuring: reasonNoStore,
	}, report.Unsupported)
	assert.Len(t, report.Ran, len(segmenttest.CaseNames())-4)
}

func TestFactoryErrorFailsTheCase(t *testing.T) {
	t.Parallel()

	report := segmenttest.Check(func(segmenttest.Data) (segmenttest.Fixture, error) {
		return segmenttest.Fixture{}, errToy
	})

	require.Len(t, report.Failed, len(segmenttest.CaseNames()))
	assert.Equal(t, []string{"the factory failed: toy failure"}, report.Failed[segmenttest.CasePositions])
}

func TestMemoryProviderConforms(t *testing.T) {
	t.Parallel()

	segmenttest.Run(t, func(data segmenttest.Data) (segmenttest.Fixture, error) {
		return segmenttest.Fixture{
			Provider: segment.NewMemoryProvider(data),
			Unsupported: segmenttest.NewWaivers().
				With(segmenttest.CapFail, "an in-memory provider has no store to fail").
				With(segmenttest.CapPartial, "an in-memory provider has no store to fail").
				With(segmenttest.CapHang, "an in-memory provider never waits"),
		}, nil
	})
}

func (p *toy) snapshot() toyConfig {
	p.mu.Lock()
	defer p.mu.Unlock()

	return toyConfig{gate: p.gate, failAll: p.failAll, failSeg: p.failSeg}
}

// hang waits on the gate of the backend until the context ends, unless the toy ignores the context.
func (p *toy) waitGate(ctx context.Context, gate chan struct{}) error {
	if gate == nil {
		return nil
	}

	p.entered <- struct{}{}

	if p.defect == defectIgnoresCtx {
		<-gate

		return nil
	}

	select {
	case <-gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
