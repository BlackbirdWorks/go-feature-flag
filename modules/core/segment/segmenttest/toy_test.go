package segmenttest_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmenttest"
)

var errToy = errors.New("toy failure")

const (
	reasonNoStore = "no store"
	msgStuckText  = "a call did not return within the bound"
)

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
	defectKeepsPrefilled  = "keeps the state the caller left when the call fails"
	defectAnswersClosed   = "answers after Shutdown"
	defectLongIDMember    = "an id of a hundred kilobytes is a member"
	defectLeaks           = "leaks a goroutine"
	defectUnknownSegErr   = "returns an error for a segment it does not hold"
	defectKeepsOnOutage   = "keeps the state the caller left when the whole store fails"
	defectFastWrong       = "answers wrongly before it reaches the store"
	defectStuckLongID     = "deadlocks on an id of a hundred kilobytes"
	defectStuckBeforeGate = "deadlocks before it reaches the store"
)

// toy is a correct in-memory provider with one defect that a test switches on.
type toy struct {
	members  map[string]map[string]bool
	failSeg  map[string]bool
	gate     chan struct{}
	entered  chan struct{}
	leakStop chan struct{}
	defect   string
	kept     [][]segment.Lookup
	inflight int
	mu       sync.Mutex
	closed   bool
	failAll  bool
	released bool
}

func newToy(data segmenttest.Data, defect string) *toy {
	toyProvider := &toy{
		members: map[string]map[string]bool{}, failSeg: map[string]bool{}, defect: defect,
		entered: make(chan struct{}, 64), leakStop: make(chan struct{}),
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

	if p.refuses(lookups) {
		return errToy
	}

	if p.stalls(lookups, cfg) {
		return fastAnswer(lookups)
	}

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

	if p.answersEndedContext(ctx, lookups) {
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

func toLower(s string) string {
	out := []byte(s)

	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + 'a' - 'A'
		}
	}

	return string(out)
}

func toyFactory(t *testing.T, defect string) segmenttest.Factory {
	t.Helper()

	return func(data segmenttest.Data) (segmenttest.Fixture, error) {
		provider := newToy(data, defect)
		t.Cleanup(provider.stopLeaks)

		return segmenttest.Fixture{
			Provider: provider, Backend: provider, Bound: 300 * time.Millisecond,
			Close: provider.Release,
		}, nil
	}
}

func TestSuitePassesACorrectProvider(t *testing.T) {
	t.Parallel()

	report := segmenttest.Run(t, toyFactory(t, defectNone), nil)

	assert.Equal(t, segmenttest.CaseNames(), report.Ran)
	assert.Empty(t, report.Failed)
	assert.Empty(t, report.Unsupported)
}

func TestSuiteCatchesEachDefect(t *testing.T) {
	t.Parallel()

	// each row names the case that must catch the defect and the text of the failure that it must give: the text
	// identifies the defect, so a case that fails for another reason does not pass the row
	tests := []struct {
		defect      string
		wantCase    string
		wantMessage string
	}{
		{defectReversesStates, segmenttest.CasePositions, "the answers are in the positions of the lookups"},
		{defectAnswersGhost, segmenttest.CaseUnknownSegment, "a segment the provider does not hold"},
		{
			defectUnknownSegErr,
			segmenttest.CaseUnknownSegment,
			"a segment that is not loaded is not a failure of the call",
		},
		{defectChangesID, segmenttest.CaseInputUntouched, `expected: " M1 "`},
		{defectDuplicates, segmenttest.CaseDuplicates, "actual  : []segment.State{0x2, 0x0, 0x1, 0x1}"},
		{defectEmptyIsError, segmenttest.CaseEmptyCall, "an empty call must not fail"},
		{defectTruncates, segmenttest.CaseLargeBatch, "a large batch is answered in place"},
		{defectEmptyIsMember, segmenttest.CaseOddIDs, `an id that is not a member must not be one: ""`},
		{defectPartialWrong, segmenttest.CasePartialFailure, "a failed lookup stays unknown"},
		{defectFailureAnswers, segmenttest.CaseFailure, "actual  : []segment.State{0x1, 0x1}"},
		{defectKeepsOnOutage, segmenttest.CaseFailure, "actual  : []segment.State{0x2, 0x1}"},
		{defectNeverRecovers, segmenttest.CaseRecovers, "the provider must answer after the failure is healed"},
		{
			defectIgnoresEndedCtx,
			segmenttest.CaseCtxEndedBefore,
			"a context that ended is answered rightly or not at all",
		},
		{defectIgnoresCtx, segmenttest.CaseCtxCancelDuring, "the call did not return when its context ended"},
		{defectStuckBeforeGate, segmenttest.CaseCtxCancelDuring, msgStuckText},
		{
			defectFastWrong,
			segmenttest.CaseCtxCancelDuring,
			"an answer given before the store was reached must be right",
		},
		{defectRetainsSlice, segmenttest.CaseSliceNotRetained, "a later call changed a slice that was returned"},
		{defectRace, segmenttest.CaseConcurrent, "a concurrent call failed"},
		{defectKeepsPrefilled, segmenttest.CasePrefilled, "a failed lookup keeps the state it came with"},
		{defectAnswersClosed, segmenttest.CaseAfterShutdown, "a call after Shutdown must fail"},
		{defectLongIDMember, segmenttest.CaseLongID, "an id that is not a member must not be one"},
		{defectStuckLongID, segmenttest.CaseLongID, msgStuckText},
		{defectLeaks, segmenttest.CasePositions, "goroutine leak: "},
	}

	seen := map[string]bool{}

	for _, tt := range tests {
		key := tt.wantCase + "|" + tt.wantMessage
		require.False(t, seen[key], "two rows name the same failure: %s", key)

		seen[key] = true

		t.Run(tt.defect, func(t *testing.T) {
			t.Parallel()

			report := segmenttest.Check(toyFactory(t, tt.defect), nil)

			require.Contains(t, report.Failed, tt.wantCase, "the case that must catch %q", tt.defect)
			assert.Contains(t, strings.Join(report.Failed[tt.wantCase], "\n"), tt.wantMessage)
		})
	}
}

// waivingFactory returns the toy with a Backend of its own and the waivers that the fixture states.
func waivingFactory(t *testing.T, fixtureWaivers segmenttest.Waivers) segmenttest.Factory {
	t.Helper()

	return func(data segmenttest.Data) (segmenttest.Fixture, error) {
		provider := newToy(data, defectNone)
		t.Cleanup(provider.stopLeaks)

		return segmenttest.Fixture{
			Provider:    provider,
			Backend:     provider,
			Unsupported: fixtureWaivers,
			Close:       provider.Release,
			Bound:       300 * time.Millisecond,
		}, nil
	}
}

func TestWaiversAreDeclaredAndReported(t *testing.T) {
	t.Parallel()

	both := segmenttest.NewWaivers().
		With(segmenttest.CapPartial, reasonNoStore).
		With(segmenttest.CapHang, reasonNoStore)

	report := segmenttest.Check(waivingFactory(t, both), both)

	assert.Empty(t, report.Invalid)
	assert.Empty(t, report.Failed)
	assert.Equal(t, map[string]string{
		segmenttest.CasePartialFailure:  reasonNoStore,
		segmenttest.CaseCtxCancelDuring: reasonNoStore,
		segmenttest.CasePrefilled:       reasonNoStore,
	}, report.Unsupported)
	assert.Len(t, report.Ran, len(segmenttest.CaseNames())-3)
}

func TestInvalidWaiversStopTheRun(t *testing.T) {
	t.Parallel()

	partial := segmenttest.NewWaivers().With(segmenttest.CapPartial, reasonNoStore)

	tests := []struct {
		name     string
		fixture  segmenttest.Waivers
		declared segmenttest.Waivers
		factory  func(t *testing.T, w segmenttest.Waivers) segmenttest.Factory
		want     string
	}{
		{
			name:     "the fixture waives what the run does not declare",
			fixture:  partial,
			declared: nil,
			want:     "the waivers of the fixture map[partial-failure:no store] differ from the ones the run declares map[]",
		},
		{
			name:     "the run declares what the fixture does not waive",
			fixture:  nil,
			declared: partial,
			want:     "the waivers of the fixture map[] differ from the ones the run declares map[partial-failure:no store]",
		},
		{
			name:     "an unknown capability",
			fixture:  nil,
			declared: segmenttest.NewWaivers().With("teleport", reasonNoStore),
			want:     `declared waivers: unknown capability "teleport"`,
		},
		{
			name:     "a waiver without a reason",
			fixture:  nil,
			declared: segmenttest.NewWaivers().With(segmenttest.CapHang, "  "),
			want:     `declared waivers: the waiver of "hang" has no reason`,
		},
		{
			name:     "the failure cannot be waived",
			fixture:  nil,
			declared: segmenttest.NewWaivers().With(segmenttest.CapFail, reasonNoStore),
			want:     "declared waivers: the capability fail cannot be waived: a provider that cannot fail is wrapped",
		},
		{
			name: "a fixture without a backend has nothing to waive", fixture: partial, declared: partial,
			factory: func(t *testing.T, w segmenttest.Waivers) segmenttest.Factory {
				t.Helper()

				return func(data segmenttest.Data) (segmenttest.Fixture, error) {
					return segmenttest.Fixture{Provider: newToy(data, defectNone), Unsupported: w}, nil
				}
			},
			want: "a fixture without a Backend is wrapped by the suite and has nothing to waive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			factory := waivingFactory(t, tt.fixture)
			if tt.factory != nil {
				factory = tt.factory(t, tt.fixture)
			}

			report := segmenttest.Check(factory, tt.declared)

			assert.Equal(t, []string{tt.want}, report.Invalid)
			assert.Empty(t, report.Ran, "nothing runs while the waivers are wrong")
		})
	}
}

func TestAGoroutineThatTheCaseStartsAndLeavesIsAFailure(t *testing.T) {
	t.Parallel()

	report := segmenttest.Check(toyFactory(t, defectLeaks), nil)

	for _, name := range segmenttest.CaseNames() {
		if _, ran := report.Failed[name]; ran {
			assert.Contains(t, report.Failed[name][len(report.Failed[name])-1], "goroutine leak: ")
		}
	}

	assert.Contains(t, report.Failed, segmenttest.CasePositions)
}

func TestFactoryErrorFailsTheCase(t *testing.T) {
	t.Parallel()

	report := segmenttest.Check(func(segmenttest.Data) (segmenttest.Fixture, error) {
		return segmenttest.Fixture{}, errToy
	}, nil)

	require.Len(t, report.Failed, len(segmenttest.CaseNames()))
	assert.Equal(t, []string{"the factory failed: toy failure"}, report.Failed[segmenttest.CasePositions])
}

// TestMemoryProviderConforms runs the suite on a provider that has no store: it has no Backend, so the suite wraps
// it, and nothing is skipped but the cases that need a Shutdown.
func TestMemoryProviderConforms(t *testing.T) {
	t.Parallel()

	report := segmenttest.Run(t, func(data segmenttest.Data) (segmenttest.Fixture, error) {
		return segmenttest.Fixture{Provider: segment.NewMemoryProvider(data)}, nil
	}, nil)

	assert.Equal(
		t,
		map[string]string{segmenttest.CaseAfterShutdown: "the provider has no Shutdown"},
		report.Unsupported,
	)
	assert.Empty(t, report.Failed)
}

// Shutdown makes the toy refuse calls. It is idempotent.
func (p *toy) Shutdown(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true

	return nil
}

func (p *toy) stopLeaks() {
	select {
	case <-p.leakStop:
	default:
		close(p.leakStop)
	}
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
	case !held:
		return p.unheld(look)
	case set[look.ID] || (p.defect == defectEmptyIsMember && look.ID == "") ||
		(p.defect == defectLongIDMember && len(look.ID) > 50000):
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

func (p *toy) snapshot() toyConfig {
	p.mu.Lock()
	defer p.mu.Unlock()

	return toyConfig{gate: p.gate, failAll: p.failAll, failSeg: p.failSeg}
}

// waitGate waits on the gate of the backend until the context ends, unless the toy ignores the context.
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

func (p *toy) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.closed
}

// answersEndedContext is the defect of a toy that answers although its context ended.
func (p *toy) answersEndedContext(ctx context.Context, lookups []segment.Lookup) bool {
	if p.defect != defectIgnoresEndedCtx || ctx.Err() == nil {
		return false
	}

	for i := range lookups {
		lookups[i].State = segment.Member
	}

	return true
}

// refuses resets the states the way a correct provider does, and tells whether the toy is closed.
func (p *toy) refuses(lookups []segment.Lookup) bool {
	keeps := p.defect == defectKeepsPrefilled || (p.defect == defectKeepsOnOutage && p.snapshot().failAll)

	if !keeps {
		for i := range lookups {
			lookups[i].State = segment.Unknown
		}
	}

	if p.defect == defectLeaks {
		go func() { <-p.leakStop }()
	}

	return p.isClosed() && p.defect != defectAnswersClosed
}

// stalls applies the defects that block or answer before the store is reached. It returns true when the toy
// answers fast instead of going on.
func (p *toy) stalls(lookups []segment.Lookup, cfg toyConfig) bool {
	if p.defect == defectStuckLongID {
		for i := range lookups {
			if len(lookups[i].ID) > 50000 {
				<-p.leakStop
			}
		}
	}

	if p.defect == defectStuckBeforeGate && cfg.gate != nil {
		<-p.leakStop
	}

	return p.defect == defectFastWrong && cfg.gate != nil
}

// fastAnswer is the defect of a toy that answers wrongly, without an error, when the store is slow.
func fastAnswer(lookups []segment.Lookup) error {
	for i := range lookups {
		lookups[i].State = segment.NotMember
	}

	return nil
}

// unheld is what the toy does with a lookup of a segment that it does not hold.
func (p *toy) unheld(look *segment.Lookup) error {
	switch p.defect {
	case defectAnswersGhost:
		look.State = segment.NotMember
	case defectUnknownSegErr:
		return errToy
	}

	return nil
}
