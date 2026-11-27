package segmenttest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"runtime"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// Capability is something a backend may not be able to arrange.
type Capability string

const (
	// CapFail is [Backend.Fail]: every call fails with an error until Heal. It cannot be waived.
	CapFail Capability = "fail"
	// CapPartial is [Backend.FailSegment]: the lookups of one segment cannot be answered.
	CapPartial Capability = "partial-failure"
	// CapHang is [Backend.Hang]: a call does not answer until its context ends or Release.
	CapHang Capability = "hang"
)

// Waivers lists the capabilities that a fixture cannot arrange, each with the reason.
type Waivers map[Capability]string

// NewWaivers returns an empty set of waivers.
func NewWaivers() Waivers { return make(Waivers) }

// With adds a waiver and returns the set.
func (w Waivers) With(capability Capability, reason string) Waivers {
	w[capability] = reason

	return w
}

// Backend is the handle through which the suite makes the store behind a provider misbehave. Every method that a
// fixture waives is never called.
type Backend interface {
	// Fail makes every following call fail with an error, with no lookup answered, until Heal.
	Fail()
	// FailSegment makes the lookups of the segment unanswerable, and the call fail, until Heal.
	FailSegment(name string)
	// Hang makes the following calls wait until their context ends or Release. The channel gets one value for
	// every call that started to wait.
	Hang() <-chan struct{}
	// Release lets the calls that hang go on, and stops hanging.
	Release()
	// Heal undoes Fail and FailSegment.
	Heal()
}

// Data is the members of the segments the provider must hold.
type Data map[string][]string

// Fixture is a provider under test and the handle behind it.
type Fixture struct {
	// Provider is the provider under test, holding the members of the Data.
	Provider segment.Provider
	// Backend arranges faults in the store behind the provider. A provider that has no store to fail (an in-memory
	// one) leaves it nil: the suite then wraps the provider in a decorator of its own that fails and hangs on
	// request, so the cases that need a fault still run.
	Backend Backend
	// Unsupported lists the faults that the backend cannot arrange, with a reason for each: only CapPartial and
	// CapHang can be waived, and only with a Backend. The run must declare the same waivers (see [Run]).
	Unsupported Waivers
	// Close (optional) releases the provider after the case.
	Close func()
	// Bound is how long a case waits for a call that is known to fail by never answering. Zero means ten seconds.
	Bound time.Duration
}

// Factory builds a fixture for a case. It is called once for each case, with the data the suite needs.
type Factory func(data Data) (Fixture, error)

// Report is what a run did.
type Report struct {
	Failed      map[string][]string
	Unsupported map[string]string
	// Invalid lists the problems of the waivers: an unknown capability, an empty reason, a waiver that cannot be
	// given, or a fixture whose waivers differ from the ones the run declares. No case runs while there is one.
	Invalid []string
	Ran     []string
}

// The names of the cases.
const (
	CasePositions        = "positions-are-preserved"
	CaseUnknownSegment   = "unknown-segment-stays-unknown"
	CaseInputUntouched   = "segment-and-id-are-not-changed"
	CaseDuplicates       = "duplicates-in-one-call-get-the-same-answer"
	CaseEmptyCall        = "an-empty-call-is-not-an-error"
	CaseLargeBatch       = "a-large-batch-is-answered-in-place"
	CaseOddIDs           = "odd-ids-are-never-members"
	CasePartialFailure   = "partial-failure-leaves-the-failed-unknown"
	CaseFailure          = "a-failed-call-answers-nothing-wrongly"
	CaseRecovers         = "a-provider-recovers-after-a-failure"
	CaseCtxEndedBefore   = "a-context-that-ended-before-is-honoured"
	CaseCtxCancelDuring  = "a-cancel-during-the-call-is-honoured"
	CaseSliceNotRetained = "the-slice-is-not-retained"
	CaseConcurrent       = "concurrent-calls-are-independent"
	CasePrefilled        = "prefilled-states-are-reset"
	CaseAfterShutdown    = "a-call-after-shutdown-answers-nothing"
	CaseLongID           = "an-over-long-id-is-never-a-member"
)

const (
	alpha, beta, ghost = "alpha", "beta", "ghost"
	kept               = "kept"
	oddIDLength        = 4096
	longIDBytes        = 100_000
	longIDRepeats      = longIDBytes / 2
	bufferedEvents     = 64
	idSpan             = 2 * largeMembers
	defaultBound       = 10 * time.Second
	largeMembers       = 100
	largeLookups       = 400
	workers            = 8
	rounds             = 25
)

// suiteData is the data of every case: alpha holds m0..m99 and "shared", beta holds "shared" and "only-beta".
func suiteData() Data {
	alphaMembers := make([]string, 0, largeMembers+1)
	for i := range largeMembers {
		alphaMembers = append(alphaMembers, "m"+strconv.Itoa(i))
	}

	alphaMembers = append(alphaMembers, "shared")

	return Data{alpha: alphaMembers, beta: {"shared", "only-beta"}}
}

func lookup(seg, id string) segment.Lookup { return segment.Lookup{Segment: seg, ID: id} }

func statesOf(lookups []segment.Lookup) []segment.State {
	out := make([]segment.State, len(lookups))
	for i := range lookups {
		out[i] = lookups[i].State
	}

	return out
}

// reporter collects the failures of one case. FailNow ends the goroutine of the case.
type reporter struct {
	failures []string
	mu       sync.Mutex
	failed   bool
}

func (r *reporter) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.failed = true
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

func (r *reporter) FailNow() {
	r.mu.Lock()
	r.failed = true
	r.mu.Unlock()

	runtime.Goexit()
}

// env is what a case works with.
type scene struct {
	shutdown func(context.Context) error // Shutdown of the provider under test, nil when it has none
	require  *require.Assertions
	assert   *assert.Assertions
	fx       Fixture
}

func background() context.Context { return context.Background() }

func (sc *scene) bound() time.Duration {
	if sc.fx.Bound > 0 {
		return sc.fx.Bound
	}

	return defaultBound
}

type testCase struct {
	run  func(sc *scene)
	name string
	need Capability
}

func cases() []testCase {
	return []testCase{
		{name: CasePositions, run: casePositions},
		{name: CaseUnknownSegment, run: caseUnknownSegment},
		{name: CaseInputUntouched, run: caseInputUntouched},
		{name: CaseDuplicates, run: caseDuplicates},
		{name: CaseEmptyCall, run: caseEmptyCall},
		{name: CaseLargeBatch, run: caseLargeBatch},
		{name: CaseOddIDs, run: caseOddIDs},
		{name: CasePartialFailure, run: casePartialFailure, need: CapPartial},
		{name: CaseFailure, run: caseFailure, need: CapFail},
		{name: CaseRecovers, run: caseRecovers, need: CapFail},
		{name: CaseCtxEndedBefore, run: caseCtxEndedBefore},
		{name: CaseCtxCancelDuring, run: caseCtxCancelDuring, need: CapHang},
		{name: CaseSliceNotRetained, run: caseSliceNotRetained},
		{name: CaseConcurrent, run: caseConcurrent},
		{name: CasePrefilled, run: casePrefilled, need: CapPartial},
		{name: CaseAfterShutdown, run: caseAfterShutdown, need: capShutdown},
		{name: CaseLongID, run: caseLongID},
	}
}

// CaseNames lists the cases of the suite.
func CaseNames() []string {
	all := cases()
	names := make([]string, len(all))

	for i := range all {
		names[i] = all[i].name
	}

	return names
}

// capShutdown is not a fault: the case applies to the providers that implement [segment.Shutdowner].
const capShutdown Capability = "shutdown"

// Check runs every case against the providers of the factory and returns the report. It never fails a test.
// declared is the set of waivers that the caller states for the factory: it must be the one of every fixture.
func Check(factory Factory, declared Waivers) Report {
	report := Report{Failed: map[string][]string{}, Unsupported: map[string]string{}}

	for _, problem := range validateWaivers(declared) {
		report.Invalid = append(report.Invalid, "declared waivers: "+problem)
	}

	if len(report.Invalid) > 0 {
		return report
	}

	for _, tcase := range cases() {
		fixture, err := factory(suiteData())
		if err != nil {
			report.Ran = append(report.Ran, tcase.name)
			report.Failed[tcase.name] = []string{"the factory failed: " + err.Error()}

			continue
		}

		if problem := checkFixture(&fixture, declared); problem != "" {
			report.Invalid = append(report.Invalid, problem)

			closeFixture(fixture)

			return Report{Failed: map[string][]string{}, Unsupported: map[string]string{}, Invalid: report.Invalid}
		}

		prepared := prepare(fixture)

		if reason, skip := prepared.skips(tcase); skip {
			report.Unsupported[tcase.name] = reason

			closeFixture(fixture)

			continue
		}

		report.Ran = append(report.Ran, tcase.name)

		msgs := runCase(tcase, prepared) // closes the fixture
		if len(msgs) > 0 {
			report.Failed[tcase.name] = msgs
		}
	}

	return report
}

// validateWaivers returns the problems of a set of waivers.
func validateWaivers(waivers Waivers) []string {
	var problems []string

	for _, capability := range slices.Sorted(maps.Keys(waivers)) {
		reason := waivers[capability]

		switch {
		case capability != CapPartial && capability != CapHang && capability != CapFail:
			problems = append(problems, fmt.Sprintf("unknown capability %q", capability))
		case capability == CapFail:
			problems = append(problems, "the capability fail cannot be waived: a provider that cannot fail is wrapped")
		case strings.TrimSpace(reason) == "":
			problems = append(problems, fmt.Sprintf("the waiver of %q has no reason", capability))
		}
	}

	return problems
}

// checkFixture compares the waivers of the fixture with the ones that the run declares.
func checkFixture(fixture *Fixture, declared Waivers) string {
	for _, problem := range validateWaivers(fixture.Unsupported) {
		return "fixture waivers: " + problem
	}

	if fixture.Backend == nil && len(fixture.Unsupported) > 0 {
		return "a fixture without a Backend is wrapped by the suite and has nothing to waive"
	}

	if !maps.Equal(fixture.Unsupported, declared) {
		return fmt.Sprintf(
			"the waivers of the fixture %v differ from the ones the run declares %v",
			fixture.Unsupported,
			declared,
		)
	}

	return ""
}

// prepared is a fixture ready for the cases: its provider, the backend (its own or the decorator of the suite) and
// the Shutdown of the provider that was given.
type prepared struct {
	shutdown func(context.Context) error
	waived   Waivers
	fixture  Fixture
}

func prepare(fixture Fixture) prepared {
	prep := prepared{fixture: fixture, waived: fixture.Unsupported}

	if shutdowner, ok := fixture.Provider.(segment.Shutdowner); ok {
		prep.shutdown = shutdowner.Shutdown
	}

	if fixture.Backend == nil {
		wrapper := newFaultProvider(fixture.Provider)
		prep.fixture.Provider, prep.fixture.Backend = wrapper, wrapper
	}

	return prep
}

func (p prepared) skips(tcase testCase) (string, bool) {
	if tcase.need == capShutdown {
		if p.shutdown == nil {
			return "the provider has no Shutdown", true
		}

		return "", false
	}

	if tcase.need == "" || tcase.need == CapFail {
		return "", false
	}

	reason, waived := p.waived[tcase.need]

	return reason, waived
}

func closeFixture(fixture Fixture) {
	if fixture.Close != nil {
		fixture.Close()
	}
}

// labelKey is the pprof label that marks the goroutines that a case starts: they inherit it from the goroutine of
// the case, so the leak check finds exactly the ones of this case.
const labelKey = "segmenttest"

func runCase(tcase testCase, prep prepared) []string {
	rep := &reporter{}
	done := make(chan struct{})
	label := fmt.Sprintf("%s-%p", tcase.name, rep) // unique to this run: runs in parallel must not see each other

	go func() {
		defer close(done)

		pprof.Do(context.Background(), pprof.Labels(labelKey, label), func(context.Context) {
			tcase.run(
				&scene{fx: prep.fixture, shutdown: prep.shutdown, require: require.New(rep), assert: assert.New(rep)},
			)
		})
	}()

	<-done

	// the case is over: release what hangs and close the fixture, then every goroutine that it started must end
	if prep.fixture.Backend != nil {
		prep.fixture.Backend.Release()
	}

	closeFixture(prep.fixture)

	if leaked := waitForLeaks(label, prep.bound()); leaked > 0 {
		rep.Errorf("goroutine leak: %d goroutines started by the case are still running", leaked)
	}

	return rep.failures
}

func (p prepared) bound() time.Duration {
	if p.fixture.Bound > 0 {
		return p.fixture.Bound
	}

	return defaultBound
}

// waitForLeaks waits, for a bounded time and without sleeping, until no goroutine carries the label of the case. It
// returns the number that are left.
func waitForLeaks(name string, bound time.Duration) int {
	deadline := time.After(bound)

	for {
		left := labelled(name)
		if left == 0 {
			return 0
		}

		select {
		case <-deadline:
			return left
		default:
			runtime.Gosched()
		}
	}
}

// labelled counts the goroutines that carry the label of the case in the goroutine profile.
func labelled(name string) int {
	var dump strings.Builder

	_ = pprof.Lookup("goroutine").WriteTo(&dump, 1)

	return strings.Count(dump.String(), fmt.Sprintf("# labels: {%q:%q}", labelKey, name))
}

// Run checks the factory and fails the test for each case that failed. waivers is the set of faults that the
// fixtures cannot arrange: it is compared with the Unsupported of every fixture, and a difference fails the run. It
// logs the cases that a waiver skipped.
func Run(t *testing.T, factory Factory, waivers Waivers) Report {
	t.Helper()

	report := Check(factory, waivers)

	for _, problem := range report.Invalid {
		assert.Fail(t, "invalid waivers: "+problem)
	}

	for _, name := range slices.Sorted(maps.Keys(report.Unsupported)) {
		t.Logf("unsupported: %s (%s)", name, report.Unsupported[name])
	}

	for _, name := range report.Ran {
		t.Run(name, func(t *testing.T) {
			t.Helper()

			for _, msg := range report.Failed[name] {
				assert.Fail(t, msg)
			}
		})
	}

	return report
}

// faultProvider wraps a provider that has no store to fail: it fails, fails for one segment, or hangs on request,
// and calls the provider the rest of the time. It answers the way a provider that follows the contract does:
// nothing is answered when the call fails, and a state that the caller left is reset.
type faultProvider struct {
	inner   segment.Provider
	gate    chan struct{}
	entered chan struct{}
	failSeg map[string]bool
	mu      sync.Mutex
	failed  bool
}

func newFaultProvider(inner segment.Provider) *faultProvider {
	return &faultProvider{inner: inner, failSeg: map[string]bool{}}
}

var errArranged = errors.New("segmenttest: the suite arranged this failure")

func (f *faultProvider) Contains(ctx context.Context, lookups []segment.Lookup) error {
	f.mu.Lock()
	gate, entered, failed, failSeg := f.gate, f.entered, f.failed, maps.Clone(f.failSeg)
	f.mu.Unlock()

	if gate != nil {
		entered <- struct{}{}

		select {
		case <-gate:
		case <-ctx.Done():
			resetStates(lookups)

			return ctx.Err() //nolint:wrapcheck // the context error is the failure
		}
	}

	if failed {
		resetStates(lookups)

		return errArranged
	}

	if len(failSeg) == 0 {
		return f.inner.Contains(ctx, lookups) //nolint:wrapcheck // the provider's own result
	}

	var (
		sub  []segment.Lookup
		back []int
	)

	for i := range lookups {
		if failSeg[lookups[i].Segment] {
			lookups[i].State = segment.Unknown

			continue
		}

		sub = append(sub, lookups[i])
		back = append(back, i)
	}

	_ = f.inner.Contains(ctx, sub)

	for j := range sub {
		lookups[back[j]].State = sub[j].State
	}

	return errArranged
}

func resetStates(lookups []segment.Lookup) {
	for i := range lookups {
		lookups[i].State = segment.Unknown
	}
}

// Fail, FailSegment, Hang, Release and Heal make the decorator the backend of its provider.
func (f *faultProvider) Fail() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.failed = true
}

func (f *faultProvider) FailSegment(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.failSeg[name] = true
}

func (f *faultProvider) Hang() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.gate, f.entered = make(chan struct{}), make(chan struct{}, bufferedEvents)

	return f.entered
}

func (f *faultProvider) Release() {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.gate != nil {
		close(f.gate)

		f.gate = nil
	}
}

func (f *faultProvider) Heal() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.failed = false
	f.failSeg = map[string]bool{}
}

func casePositions(cas *scene) {
	lookups := []segment.Lookup{
		lookup(alpha, "m1"), lookup(beta, "only-beta"), lookup(alpha, "absent"), lookup(beta, "m1"),
		lookup(alpha, "shared"), lookup(beta, "absent"),
	}

	cas.require.NoError(cas.fx.Provider.Contains(background(), lookups))
	cas.assert.Equal([]segment.State{
		segment.Member, segment.Member, segment.NotMember, segment.NotMember, segment.Member, segment.NotMember,
	}, statesOf(lookups))
}

func caseUnknownSegment(cas *scene) {
	lookups := []segment.Lookup{lookup(ghost, "m1"), lookup(alpha, "m1")}

	_ = cas.fx.Provider.Contains(background(), lookups)

	cas.assert.Equal(segment.Unknown, lookups[0].State, "a segment the provider does not hold")
	cas.assert.Equal(segment.Member, lookups[1].State, "the other lookup is answered")
}

func caseInputUntouched(cas *scene) {
	lookups := []segment.Lookup{lookup(alpha, " M1 "), lookup(beta, "only-beta"), lookup(ghost, "x")}
	want := slices.Clone(lookups)

	_ = cas.fx.Provider.Contains(background(), lookups)

	for i := range lookups {
		cas.assert.Equal(want[i].Segment, lookups[i].Segment)
		cas.assert.Equal(want[i].ID, lookups[i].ID)
	}
}

func caseDuplicates(cas *scene) {
	lookups := []segment.Lookup{
		lookup(alpha, "m1"),
		lookup(alpha, "m1"),
		lookup(alpha, "absent"),
		lookup(alpha, "absent"),
	}

	cas.require.NoError(cas.fx.Provider.Contains(background(), lookups))
	cas.assert.Equal(
		[]segment.State{segment.Member, segment.Member, segment.NotMember, segment.NotMember},
		statesOf(lookups),
	)
}

func caseEmptyCall(cas *scene) {
	cas.require.NoError(cas.fx.Provider.Contains(background(), nil))
	cas.require.NoError(cas.fx.Provider.Contains(background(), []segment.Lookup{}))
}

func caseLargeBatch(cas *scene) {
	lookups := make([]segment.Lookup, largeLookups)
	want := make([]segment.State, largeLookups)

	for i := range lookups {
		lookups[i] = lookup(alpha, "m"+strconv.Itoa(i))
		want[i] = segment.NotMember

		if i < largeMembers {
			want[i] = segment.Member
		}
	}

	cas.require.NoError(cas.fx.Provider.Contains(background(), lookups))
	cas.assert.Equal(want, statesOf(lookups))
}

func caseOddIDs(cas *scene) {
	ids := []string{"", " ", "m1\n", "M1", "m1\x00", "ünï", "a\tb", string(make([]byte, oddIDLength))}
	lookups := make([]segment.Lookup, len(ids))

	for i, id := range ids {
		lookups[i] = lookup(alpha, id)
	}

	_ = cas.fx.Provider.Contains(background(), lookups)

	for i := range lookups {
		cas.assert.NotEqual(segment.Member, lookups[i].State, "an id that is not a member must not be one: %q", ids[i])
	}
}

func casePartialFailure(cas *scene) {
	cas.fx.Backend.FailSegment(beta)

	lookups := []segment.Lookup{
		lookup(alpha, "m1"),
		lookup(beta, "only-beta"),
		lookup(alpha, "absent"),
		lookup(beta, "absent"),
	}

	err := cas.fx.Provider.Contains(background(), lookups)

	cas.require.Error(err, "the failure is reported")
	cas.assert.Equal(segment.Unknown, lookups[1].State, "a failed lookup stays unknown")
	cas.assert.Equal(segment.Unknown, lookups[3].State, "a failed lookup stays unknown")

	// the lookups that were answered keep a correct answer; a provider may also leave them unknown
	cas.assert.Contains([]segment.State{segment.Member, segment.Unknown}, lookups[0].State)
	cas.assert.Contains([]segment.State{segment.NotMember, segment.Unknown}, lookups[2].State)
}

func caseFailure(cas *scene) {
	cas.fx.Backend.Fail()

	lookups := []segment.Lookup{lookup(alpha, "m1"), lookup(beta, "absent")}

	err := cas.fx.Provider.Contains(background(), lookups)

	cas.require.Error(err)
	cas.assert.Equal([]segment.State{segment.Unknown, segment.Unknown}, statesOf(lookups))
}

func caseRecovers(cas *scene) {
	cas.fx.Backend.Fail()
	cas.require.Error(cas.fx.Provider.Contains(background(), []segment.Lookup{lookup(alpha, "m1")}))
	cas.fx.Backend.Heal()

	lookups := []segment.Lookup{lookup(alpha, "m1")}

	cas.require.NoError(cas.fx.Provider.Contains(background(), lookups))
	cas.assert.Equal(segment.Member, lookups[0].State)
}

func caseCtxEndedBefore(cas *scene) {
	ended, cancel := context.WithCancel(background())
	cancel()

	lookups := []segment.Lookup{lookup(alpha, "m1"), lookup(alpha, "absent")}

	err := cas.fx.Provider.Contains(ended, lookups)
	if err != nil {
		cas.assert.Equal([]segment.State{segment.Unknown, segment.Unknown}, statesOf(lookups))

		return
	}

	// a provider that has the answers at hand may give them, and they must be right
	cas.assert.Equal([]segment.State{segment.Member, segment.NotMember}, statesOf(lookups))
}

func caseCtxCancelDuring(cas *scene) {
	entered := cas.fx.Backend.Hang()
	ctx, cancel := context.WithCancel(background())

	defer cancel()

	lookups := []segment.Lookup{lookup(alpha, "m1")}
	result := make(chan error, 1)

	go func() { result <- cas.fx.Provider.Contains(ctx, lookups) }()

	select {
	case <-entered:
	case err := <-result:
		// the provider answered without reaching the store: nothing to cancel
		cas.fx.Backend.Release()
		cas.require.NoError(err)

		return
	}

	cancel()

	var err error

	select {
	case err = <-result:
	case <-time.After(cas.bound()):
		cas.fx.Backend.Release()
		cas.assert.Fail("the call did not return when its context ended")

		return
	}

	cas.require.Error(err)
	cas.assert.Equal(segment.Unknown, lookups[0].State)

	// the slice is the caller's again: writing it must not race with a call that is still finishing
	lookups[0] = lookup("changed", "changed")

	cas.fx.Backend.Release()
}

func caseSliceNotRetained(cas *scene) {
	// one goroutine: a provider that keeps the slice and writes it in a later call clobbers the sentinel
	first := []segment.Lookup{lookup(alpha, "m1"), lookup(beta, "only-beta")}

	cas.require.NoError(cas.fx.Provider.Contains(background(), first))

	sentinel := []segment.Lookup{
		{Segment: kept, ID: kept, State: segment.Member},
		{Segment: kept, ID: kept, State: segment.Member},
	}
	copy(first, sentinel)

	for range rounds {
		again := []segment.Lookup{lookup(alpha, "m1"), lookup(beta, "only-beta")}
		cas.require.NoError(cas.fx.Provider.Contains(background(), again))
	}

	cas.require.Equal(sentinel, first, "a later call changed a slice that was returned")

	// many goroutines: a provider that touches the slice after the return races with these writes (-race)
	var wg sync.WaitGroup

	for range workers {
		wg.Go(func() {
			for range rounds {
				lookups := []segment.Lookup{lookup(alpha, "m1"), lookup(beta, "only-beta")}
				cas.require.NoError(cas.fx.Provider.Contains(background(), lookups))

				for i := range lookups {
					lookups[i] = lookup("x", "y")
				}
			}
		})
	}

	wg.Wait()
}

func caseConcurrent(cas *scene) {
	var wg sync.WaitGroup

	for worker := range workers {
		wg.Go(func() {
			for round := range rounds {
				id := "m" + strconv.Itoa((worker*rounds+round)%idSpan)
				lookups := []segment.Lookup{lookup(alpha, id), lookup(alpha, "shared"), lookup(beta, "absent")}
				err := cas.fx.Provider.Contains(background(), lookups)
				want := segment.NotMember

				if (worker*rounds+round)%idSpan < largeMembers {
					want = segment.Member
				}

				cas.require.NoError(err)
				cas.assert.Equal([]segment.State{want, segment.Member, segment.NotMember}, statesOf(lookups))
			}
		})
	}

	wg.Wait()
}

// casePrefilled: a state that the caller left in the slice never survives a failure.
func casePrefilled(cas *scene) {
	cas.fx.Backend.FailSegment(beta)

	partial := []segment.Lookup{
		{Segment: alpha, ID: "m1", State: segment.NotMember}, {Segment: beta, ID: "only-beta", State: segment.Member},
		{Segment: beta, ID: "absent", State: segment.NotMember},
	}

	cas.require.Error(cas.fx.Provider.Contains(background(), partial))
	cas.assert.Equal(segment.Unknown, partial[1].State, "a failed lookup keeps the state it came with")
	cas.assert.Equal(segment.Unknown, partial[2].State, "a failed lookup keeps the state it came with")
	cas.assert.NotEqual(segment.NotMember, partial[0].State, "an answered lookup is the answer, not what it came with")

	cas.fx.Backend.Heal()
	cas.fx.Backend.Fail()

	// an id that no earlier step asked about: a provider that caches may answer one that it already knows
	whole := []segment.Lookup{
		{Segment: alpha, ID: "m2", State: segment.Member}, {Segment: ghost, ID: "x", State: segment.NotMember},
	}

	cas.require.Error(cas.fx.Provider.Contains(background(), whole))
	cas.assert.Equal([]segment.State{segment.Unknown, segment.Unknown}, statesOf(whole))
}

// caseAfterShutdown: Shutdown is safe to call twice, and a call after it answers nothing.
func caseAfterShutdown(cas *scene) {
	cas.require.NoError(cas.shutdown(background()))
	cas.require.NoError(cas.shutdown(background()), "the second Shutdown")

	lookups := []segment.Lookup{lookup(alpha, "m1"), lookup(alpha, "absent")}

	cas.require.Error(cas.fx.Provider.Contains(background(), lookups))
	cas.assert.Equal([]segment.State{segment.Unknown, segment.Unknown}, statesOf(lookups))
}

// caseLongID: an id of a hundred kilobytes is not a member, and does not hang or panic the provider.
func caseLongID(cas *scene) {
	long := strings.Repeat("m1", longIDRepeats)
	lookups := []segment.Lookup{lookup(alpha, long), lookup(alpha, "m1")}

	_ = cas.fx.Provider.Contains(background(), lookups)

	cas.assert.NotEqual(segment.Member, lookups[0].State, "an id that is not a member must not be one")
	cas.assert.Equal(segment.Member, lookups[1].State, "the other lookup is answered")
}
