package flagruntime

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// SetState is the state of a flag set.
type SetState string

// The states of a flag set (issue 6).
const (
	// StateServing: every source is current and every flag is valid.
	StateServing SetState = "serving"
	// StateDegraded: the flag set serves, but a source is stale or missing, a flag was rejected, an entry
	// problem was reported, or a source does not resolve.
	StateDegraded SetState = "degraded"
	// StatePending: no source of the flag set was ever loaded, or its entry is invalid and it never had a valid
	// definition; it serves zero flags (its keys still route to it).
	StatePending SetState = "pending"
	// StateWithdrawn: the publisher removed the flag set. It is the state of the entries of Status.Withdrawn and
	// of the Removed change event; a withdrawn flag set is not in FlagSets and its keys are not routed.
	StateWithdrawn SetState = "withdrawn"
)

// Runtime is one immutable generation of what the relay serves. Nothing may mutate a Runtime or anything it
// reaches (the maps, the flag sets, the flags, the variation values, the status): they are shared between
// generations and read without a lock.
type Runtime struct {
	// FlagSets are the flag sets by name. A flag set that did not change is the same pointer as in the
	// previous Runtime.
	FlagSets map[string]*FlagSet
	// ByKey routes an API key to its flag set. It is rebuilt for every generation that changes a flag set
	// or a key: O(keys), not O(flags).
	ByKey map[string]*FlagSet
	// AdminKeys are the admin keys of the bootstrap file.
	AdminKeys map[string]struct{}
	// Status is the status tree of this runtime.
	Status *Status
	// Generation is the manifest generation of the content.
	Generation uint64
	// Snapshot is a local counter, strictly increasing with every successful swap.
	Snapshot uint64
}

// FlagSetByName returns the flag set with a name. It does not allocate.
func (r *Runtime) FlagSetByName(name string) (*FlagSet, bool) {
	fs, ok := r.FlagSets[name]

	return fs, ok
}

// FlagSet is the immutable flag table of one flag set. It is reused by pointer across generations while its
// definition and the content of its sources are unchanged.
type FlagSet struct {
	ModifiedAt time.Time
	change     *changeMemo
	Flags      map[string]*flagdef.InternalFlag
	Name       string
	State      SetState
	Hash       uint64
	APIKeys    int
	Rejected   int
	changeOnce sync.Once
}

// Flag returns the flag with a name. It does not allocate.
func (f *FlagSet) Flag(name string) (*flagdef.InternalFlag, bool) {
	if f == nil {
		return nil, false
	}

	fl, ok := f.Flags[name]

	return fl, ok
}

// Holder is the one place readers get the current Runtime from. The zero value is usable: the first Load stores
// one empty Runtime, so Load does not allocate afterwards. [NewHolder] stores it up front.
type Holder struct {
	p atomic.Pointer[Runtime]
}

// NewHolder returns a Holder that holds a valid empty Runtime.
func NewHolder() *Holder {
	h := &Holder{}
	h.p.Store(newEmptyRuntime())

	return h
}

// Load returns the current Runtime. It never returns nil and does not allocate once the Holder holds a Runtime.
func (h *Holder) Load() *Runtime {
	if r := h.p.Load(); r != nil {
		return r
	}

	h.p.CompareAndSwap(nil, newEmptyRuntime())

	return h.p.Load()
}

// store publishes a Runtime: the only write of the Holder, made by the engine goroutine.
func (h *Holder) store(r *Runtime) {
	h.p.Store(r)
}

func newEmptyRuntime() *Runtime {
	return &Runtime{
		FlagSets:  map[string]*FlagSet{},
		ByKey:     map[string]*FlagSet{},
		AdminKeys: map[string]struct{}{},
		Status: &Status{
			FlagSets: map[string]*FlagSetStatus{},
			Objects:  map[string]*ObjectStatus{},
		},
	}
}
