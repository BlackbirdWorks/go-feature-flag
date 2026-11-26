package storetest

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// Capability is something a backend may not be able to arrange. A case that needs a capability the backend
// declares unsupported is reported as unsupported.
type Capability string

// The capabilities.
const (
	// CapDeny is [FaultDenied]: a store whose reads are refused (403, a permission error).
	CapDeny Capability = "deny"
	// CapTransient is [FaultTransient]: a read that fails with an error that may pass by itself.
	CapTransient Capability = "transient"
	// CapThrottle is [FaultThrottled]: a read the backend throttles (429, SlowDown).
	CapThrottle Capability = "throttle"
	// CapHang is [Backend.Hang]: a read that does not answer until its context ends.
	CapHang Capability = "hang"
	// CapVersions is object versions: a read of a version that does not exist is a "not found".
	CapVersions Capability = "versions"
	// CapKeyValidation is that the store refuses a key that is not a clean relative path, as a file store does.
	// The keys of an object store are opaque and it has nothing to refuse.
	CapKeyValidation Capability = "key-validation"
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

// Hang is a key whose reads do not answer until their context ends (or until [Hang.Release]). A backend makes one
// with [NewHang] and calls [Hang.Block] from the read of the key.
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
// is released. A backend calls it before it answers the read.
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
	// Hang makes the reads of key block until their context ends or the hang is released.
	Hang(key string) (*Hang, error)
}

// Fixture is one store under test with its backend.
type Fixture struct {
	// Store is the implementation under test.
	Store manifestreader.ObjectStore
	// Backend arranges its state. Content put through it must be what Store reads.
	Backend Backend
	// Unsupported lists the capabilities the backend cannot arrange, with the reason.
	Unsupported map[Capability]string
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

// maxHangEntries is the number of reads of one hang that [Hang.Entered] can count.
const maxHangEntries = 256

type contractCase struct {
	run   func(t *testing.T, fx Fixture)
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

// Run runs every case against the stores the factory makes, as parallel subtests of t named after backend, and
// returns what ran and what was unsupported. The report is also logged.
func Run(t *testing.T, backend string, factory Factory) Report {
	t.Helper()

	report := Report{Backend: backend, Unsupported: map[string]string{}}

	var mutex sync.Mutex

	t.Run(backend, func(t *testing.T) {
		for _, current := range allCases() {
			t.Run(current.name, func(t *testing.T) {
				t.Parallel()

				fixture := factory(t)
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

				current.run(t, fixture)
			})
		}
	})

	slices.Sort(report.Ran)
	t.Logf("storetest %s: %d cases ran: %s", backend, len(report.Ran), strings.Join(report.Ran, ", "))

	for _, name := range slices.Sorted(maps.Keys(report.Unsupported)) {
		t.Logf("storetest %s: UNSUPPORTED %s (%s)", backend, name, report.Unsupported[name])
	}

	return report
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
