package storetest

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"runtime"
	"runtime/pprof"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// Capability is something a backend may not be able to arrange, or a store may not offer. A case that needs a
// capability that the fixture declares unsupported is reported as unsupported, and [Run] only accepts the
// declaration when the caller waived the same capability with the same reason.
type Capability string

// The capabilities. A store that has all of them waives none.
const (
	// CapDeny is [FaultDenied]: a store whose reads are refused (403, a permission error, a link out of the root).
	CapDeny Capability = "deny"
	// CapTransient is [FaultTransient]: a read that fails with an error that may pass by itself.
	CapTransient Capability = "transient"
	// CapThrottle is [FaultThrottled]: a read the backend throttles (429, SlowDown).
	CapThrottle Capability = "throttle"
	// CapHang is [Backend.Hang]: a read that does not answer until its context ends.
	CapHang Capability = "hang"
	// CapHangBody is [Backend.HangBody]: a read whose answer started and whose body then stalls.
	CapHangBody Capability = "hang-body"
	// CapVersions is [Backend.PutVersion]: the object keeps versions and a read can name one.
	CapVersions Capability = "versions"
	// CapKeyValidation is that the store refuses a key that is not a clean relative path, as a file store does.
	// The keys of an object store are opaque and it has nothing to refuse.
	CapKeyValidation Capability = "key-validation"
	// CapContentAddressedETag is that the ETag is a function of the bytes: the same bytes always give the same
	// ETag. A store whose ETag is a row version, a commit SHA or a generation number does not have it, and is
	// still correct: the reader needs only that the ETag describes the bytes returned, differs when they differ,
	// and is answered with "not modified" while the object is unchanged.
	CapContentAddressedETag Capability = "content-addressed-etag"
	// CapNestedKeys is that an object "a" and an object "a/b" can exist together, as in an object store.
	CapNestedKeys Capability = "nested-keys"
	// CapReservedNameChars is that keys can contain characters that some file systems reserve in names (a
	// question mark, a colon). Windows file names cannot.
	CapReservedNameChars Capability = "reserved-name-chars"
)

// Fault is a failure a backend arranges for the reads of a key.
type Fault int

// The faults. [FaultNone] clears the fault of a key.
const (
	FaultNone Fault = iota
	// FaultDenied makes the reads fail the way a refused access does: reason denied, not retryable.
	FaultDenied
	// FaultTransient makes the reads fail with an error that may pass: reason error, retryable.
	FaultTransient
	// FaultThrottled makes the reads fail with a throttling answer: reason error, retryable, throttled.
	FaultThrottled
)

// ErrUnsupported is what a backend returns for an operation it cannot arrange. A case that needs it is waived
// through its capability, so a suite run never reaches it.
var ErrUnsupported = errors.New("storetest: the backend cannot arrange this")

// maxHangEntries is the number of reads of one hang that [Hang.Entered] can count.
const maxHangEntries = 256

// Hang is a key whose reads do not answer until their context ends (or until [Hang.Release]). A backend makes
// one with [NewHang] and calls [Hang.Block] from the read of the key.
type Hang struct {
	entered  chan struct{}
	released chan struct{}
	once     sync.Once
	active   atomic.Int32
}

// NewHang returns a hang that nothing waits on yet.
func NewHang() *Hang {
	return &Hang{entered: make(chan struct{}, maxHangEntries), released: make(chan struct{})}
}

// Entered gets one value for every read that started to hang.
func (h *Hang) Entered() <-chan struct{} { return h.entered }

// Active is the number of reads that are hanging now. A read that returned is not counted any more.
func (h *Hang) Active() int { return int(h.active.Load()) }

// Release ends the hang: the hanging reads and the later ones are answered.
func (h *Hang) Release() { h.once.Do(func() { close(h.released) }) }

// Block holds the calling read: it returns the error of ctx when the context ends first, and nil when the hang
// is released. A backend calls it before it answers the read (or, for [Backend.HangBody], while the body is read).
func (h *Hang) Block(ctx context.Context) error {
	h.active.Add(1)
	defer h.active.Add(-1)

	select {
	case h.entered <- struct{}{}:
	default: // nobody counts more entries than the buffer holds
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("storetest: hang: %w", ctx.Err())
	case <-h.released:
		// a context that has ended wins, whatever select picked: a read that ignores its context is not excused
		// by a release that came at the same time
		err := ctx.Err()
		if err != nil {
			return fmt.Errorf("storetest: hang: %w", err)
		}

		return nil
	}
}

// Backend is what the suite uses to arrange the state behind the store. The methods return errors instead of
// failing a test, so that they can be called from goroutines.
type Backend interface {
	// Put creates or replaces the object atomically: a read sees the old content or the new one.
	Put(key string, content []byte) error
	// Overwrite replaces the content as an in-place rewrite that is as hard to notice as the backend allows: where
	// the backend has a size, a modification time or an inode, they are the same as before when the new content
	// has the same length. It is not atomic.
	Overwrite(key string, content []byte) error
	// Delete removes the object.
	Delete(key string) error
	// Fail makes the reads of key fail with fault until it is called again for the key (FaultNone clears it).
	Fail(key string, fault Fault) error
	// Hang makes the reads of key block until their context ends or the hang is released ([CapHang]).
	Hang(key string) (*Hang, error)
	// HangBody makes the reads of key answer, and then stall while the body is read, until their context ends or
	// the hang is released ([CapHangBody]). The object has to be larger than the part that is let through.
	HangBody(key string) (*Hang, error)
	// PutVersion writes a new version of the object and returns its version id ([CapVersions]). An id returned
	// earlier keeps naming the content it was written with.
	PutVersion(key string, content []byte) (string, error)
}

// Fixture is one store under test with its backend.
type Fixture struct {
	// Store is the implementation under test.
	Store manifestreader.ObjectStore
	// Backend arranges its state. Content put through it must be what Store reads.
	Backend Backend
	// Unsupported lists the capabilities the backend cannot arrange, with the reason.
	Unsupported Waivers
}

// Waivers are the capabilities a store does not offer, with the reason, as the test of the store states them.
// [Run] fails when the fixture declares anything else, so a store cannot drop a guarantee without the test
// saying so.
type Waivers map[Capability]string

// NewWaivers returns no waivers: a store that offers everything. Add to them with [Waivers.With].
func NewWaivers() Waivers { return make(Waivers) }

// With waives the capability, for the reason, and returns the waivers.
func (w Waivers) With(capability Capability, reason string) Waivers {
	w[capability] = reason

	return w
}

// Factory returns a fresh, empty fixture: no object of another case is visible. It is called once per case.
type Factory func(t *testing.T) Fixture

// Report tells which cases ran for a backend and which are unsupported (with the reasons).
type Report struct {
	// Unsupported maps the name of a case to why it did not run.
	Unsupported map[string]string
	// Backend is the name given to [Run].
	Backend string
	// Ran is the sorted names of the cases that ran.
	Ran []string
}

// TB is what the cases need of a test: the subset of [testing.TB] that testify uses, and the context. A
// [testing.T] pointer is one; [Check] gives the cases a recording one, so that a test can prove that a case fails.
type TB interface {
	require.TestingT
	Helper()
	Context() context.Context
}

type contractCase struct {
	run   func(t TB, fixture Fixture)
	name  string
	needs []Capability
}

// CaseNames returns the names of all cases, sorted.
func CaseNames() []string {
	cases := allCases()
	names := make([]string, 0, len(cases))

	for i := range cases {
		names = append(names, cases[i].name)
	}

	slices.Sort(names)

	return names
}

// newID returns an id that no other run has, live or not: a random one. It labels the goroutines of one run (or
// of one [Check]).
func newID(prefix string) string {
	return prefix + "#" + rand.Text()
}

// Run runs every case against the stores the factory makes, as parallel subtests of t named after backend, and
// returns what ran and what was unsupported. The report is also logged.
//
// waivers are what the store does not offer. Every case checks that the fixture declares exactly them: a store
// that starts waiving a capability fails until its test says so too.
//
// Every case also checks that it left no goroutine behind (see the package documentation), and so does the run as
// a whole.
func Run(t *testing.T, backend string, waivers Waivers, factory Factory) Report {
	t.Helper()

	report := Report{Backend: backend, Unsupported: map[string]string{}}
	runID := newID(backend)

	var mutex sync.Mutex

	t.Run(backend, func(t *testing.T) {
		for _, current := range allCases() {
			t.Run(current.name, func(t *testing.T) {
				t.Parallel()

				// registered first, so that it runs last: after the cleanups of the fixture, which stop the
				// workers of the store
				t.Cleanup(func() { checkNoLeak(t, runID, current.name) })

				fixture := factory(t)
				checkWaivers(t, waivers, &fixture)

				reason := unsupportedReason(&fixture, current.needs)

				mutex.Lock()
				if reason != "" {
					report.Unsupported[current.name] = reason
				} else {
					report.Ran = append(report.Ran, current.name)
				}
				mutex.Unlock()

				if reason != "" {
					t.Skipf("UNSUPPORTED by backend %s: %s", backend, reason)
				}

				runLabelled(t, runID, &current, fixture)
			})
		}
	})

	slices.Sort(report.Ran)
	t.Logf("storetest %s: %d cases ran: %s", backend, len(report.Ran), strings.Join(report.Ran, ", "))

	for _, name := range slices.Sorted(maps.Keys(report.Unsupported)) {
		t.Logf("storetest %s: UNSUPPORTED %s (%s)", backend, name, report.Unsupported[name])
	}

	checkNoLeak(t, runID, "")

	return report
}

// checkWaivers fails unless the fixture declares exactly the waivers the test of the store passed to [Run], and
// every waiver has a reason.
func checkWaivers(t TB, waivers Waivers, fixture *Fixture) {
	t.Helper()

	assert.Equal(t, nilIfEmpty(waivers), nilIfEmpty(fixture.Unsupported),
		"the fixture declares other waivers than the test of the store")

	for _, declared := range []Waivers{waivers, fixture.Unsupported} {
		for capability, reason := range declared {
			assert.NotEmpty(t, strings.TrimSpace(reason), "the waiver of %s has no reason", capability)
		}
	}
}

func nilIfEmpty(m Waivers) Waivers {
	if len(m) == 0 {
		return nil
	}

	return m
}

// Check runs the named case against fixture and returns what it reported as failed, and whether it ran (it does
// not when the name is not a case, or when the case needs a capability the fixture declares unsupported). It exists
// so that a test can show that the suite catches a broken store: the case runs on a goroutine of its own, with a
// recording [TB].
func Check(name string, fixture Fixture) ([]string, bool) {
	return CheckWithin(name, fixture, wait)
}

// CheckWithin is [Check] with a shorter bound for the events a case waits for. A store that is expected to fail by
// never answering (it ignores its context) then fails in about the bound and not in ten seconds. A store that is
// expected to pass needs the default bound: a short one can fail it on a busy machine.
func CheckWithin(name string, fixture Fixture, bound time.Duration) ([]string, bool) {
	for i := range allCases() {
		current := allCases()[i]
		if current.name != name {
			continue
		}

		if unsupportedReason(&fixture, current.needs) != "" {
			return nil, false
		}

		rec := newRecorder()
		rec.bound = bound
		done := make(chan struct{})
		runID := newID("check")

		go func() {
			defer close(done)

			// deferred, so that it also runs when a failed require ends the case with runtime.Goexit; the context
			// of the test ends first, as it does for a testing.T, so that a worker that waits for it can stop
			defer func() {
				rec.stop()
				checkNoLeak(rec, runID, current.name)
			}()

			runLabelled(rec, runID, &current, fixture)
		}()

		<-done

		return rec.failures(), true
	}

	return []string{"no such case: " + name}, false
}

// unsupportedReason is why the fixture cannot run a case that needs these capabilities; empty when it can.
func unsupportedReason(fixture *Fixture, needs []Capability) string {
	var missing []string

	for _, need := range needs {
		reason, no := fixture.Unsupported[need]
		if no {
			missing = append(missing, fmt.Sprintf("%s: %s", need, reason))
		}
	}

	return strings.Join(missing, "; ")
}

// Labels of the goroutines of a case.
const (
	labelRun  = "storetest-run"
	labelCase = "storetest-case"
)

// runLabelled runs the case with its goroutines labelled.
func runLabelled(t TB, runID string, current *contractCase, fixture Fixture) {
	t.Helper()

	pprof.Do(t.Context(), pprof.Labels(labelRun, runID, labelCase, current.name), func(ctx context.Context) {
		// the case code gets the labelled context: a goroutine that a store starts from it, even under a pprof.Do
		// of its own, carries the labels
		current.run(labelledTB{TB: t, ctx: ctx}, fixture)
	})
}

// labelledTB is the test of a case with the context that carries the labels of the case. The wait bound of the
// test is kept.
type labelledTB struct {
	TB

	ctx context.Context //nolint:containedctx // it is the context of the test, as testing.T.Context is
}

func (l labelledTB) Context() context.Context { return l.ctx }

func (l labelledTB) waitBound() time.Duration { return waitBound(l.TB) }

// checkNoLeak fails when a goroutine of the case (of the whole run when caseID is empty) is left.
func checkNoLeak(t TB, runID, caseID string) {
	t.Helper()

	assert.Empty(t, settledLeaks(runID, caseID), "[left-goroutines-behind] the case left goroutines behind")
}

// recorder is a [TB] that records instead of failing.
type recorder struct {
	ctx    context.Context //nolint:containedctx // it is the context of the test, as testing.T.Context is
	cancel context.CancelFunc
	fails  []string
	bound  time.Duration
	mu     sync.Mutex
}

func newRecorder() *recorder {
	ctx, cancel := context.WithCancel(context.Background())

	return &recorder{ctx: ctx, cancel: cancel}
}

func (r *recorder) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.fails = append(r.fails, fmt.Sprintf(format, args...))
}

// FailNow ends the goroutine of the case, as testing.T.FailNow does.
func (*recorder) FailNow() { runtime.Goexit() }

func (*recorder) Helper() {}

func (r *recorder) Context() context.Context { return r.ctx }

func (r *recorder) stop() { r.cancel() }

func (r *recorder) failures() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.fails)
}

func (r *recorder) waitBound() time.Duration { return r.bound }
