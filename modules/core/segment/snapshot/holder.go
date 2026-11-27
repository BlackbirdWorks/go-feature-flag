package snapshot

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/clock"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// ErrNilSource is returned by [New] for a nil source.
var ErrNilSource = errors.New("snapshot: nil source")

// Class is the class of the last failure of a holder. It never holds a member id.
type Class string

const (
	// ClassNone means the last refresh succeeded.
	ClassNone Class = ""
	// ClassVersion means the version of the segment could not be read.
	ClassVersion Class = "version"
	// ClassRead means the members could not be read.
	ClassRead Class = "read"
	// ClassMissing means the store does not have the segment.
	ClassMissing Class = "missing"
	// ClassTooLarge means the segment is over the memory guard.
	ClassTooLarge Class = "too-large"
	// ClassCanceled means the context ended during the refresh.
	ClassCanceled Class = "canceled"
)

// checkEvery is how many members are read between two looks at the context.
const checkEvery = 1024

// Options bounds the memory of one holder. A zero field means the default of the segment package.
type Options struct {
	// Missing is what a segment that does not exist means: the default (empty) is [segment.MissingNotMember], an empty
	// set; [segment.MissingUnknown] drops the set and answers Unknown.
	Missing segment.MissingKey
	// MaxMembers is the most distinct members; more refuses the pull and keeps the last good set.
	MaxMembers int
	// MaxBytes is the most bytes of the distinct members (the length of the ids), same rule.
	MaxBytes int64
}

// Status is what a holder reports. It holds no member id.
type Status struct {
	// Version is the version of the held set, empty before the first pull.
	Version string
	// LastError is the class of the failure of the last refresh, [ClassNone] when it succeeded.
	LastError Class
	// Members is the size of the held set.
	Members int
	// Age is the time since the held set was pulled.
	Age time.Duration
	// Pulls is the number of member sets pulled and swapped in.
	Pulls uint64
	// Loaded is true once a set is held.
	Loaded bool
}

// snap is an immutable view of the holder. A refresh that fails or finds nothing new publishes a copy with another
// last error and the same set.
type snap struct {
	loadedAt time.Time
	set      *segment.MemberSet
	version  string
	lastErr  Class
	pulls    uint64
	missing  bool // the store answered that the segment does not exist: no set is held
}

// Holder keeps the member set of one segment. Reads are lock-free; Refresh is driven by the caller (it starts no
// goroutine and no timer) and calls are serialised.
type Holder struct {
	src     segment.SnapshotSource
	cur     atomic.Pointer[snap]
	name    string
	opts    Options
	refresh sync.Mutex
}

var _ segment.Provider = (*Holder)(nil)

// New returns a holder of the segment name, empty until the first successful Refresh.
func New(name string, src segment.SnapshotSource, opts Options) (*Holder, error) {
	if src == nil {
		return nil, ErrNilSource
	}

	if opts.MaxMembers <= 0 {
		opts.MaxMembers = segment.DefaultMaxMembers
	}

	if opts.MaxBytes <= 0 {
		opts.MaxBytes = segment.DefaultMaxBytes
	}

	return &Holder{src: src, name: name, opts: opts}, nil
}

// Lookup answers a membership question from the held set. It does not allocate and does not lock. Before the first
// pull the answer is Unknown, never NotMember.
func (h *Holder) Lookup(id string) segment.State {
	cur := h.cur.Load()
	if cur == nil || cur.set == nil {
		return segment.Unknown
	}

	if cur.set.Contains(id) {
		return segment.Member
	}

	return segment.NotMember
}

// Contains implements [segment.Provider] for the lookups of this segment; others are left alone. It does not allocate.
func (h *Holder) Contains(ctx context.Context, lookups []segment.Lookup) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("snapshot lookup: %w", err)
	}

	cur := h.cur.Load()
	if cur == nil || cur.set == nil {
		return nil
	}

	for i := range lookups {
		if lookups[i].Segment != h.name {
			continue
		}

		lookups[i].State = segment.NotMember
		if cur.set.Contains(lookups[i].ID) {
			lookups[i].State = segment.Member
		}
	}

	return nil
}

// Status returns the state of the holder.
func (h *Holder) Status() Status {
	cur := h.cur.Load()
	if cur == nil {
		return Status{}
	}

	if cur.set == nil {
		return Status{Version: cur.version, LastError: cur.lastErr}
	}

	return Status{
		Version: cur.version, LastError: cur.lastErr, Members: cur.set.Len(), Age: clock.Now().Sub(cur.loadedAt),
		Pulls: cur.pulls, Loaded: true,
	}
}

// Refresh reads the version of the segment and, only when it differs from the held one, pulls the members and swaps
// them in. On an error (the store is down, a timeout, a stream that broke, the memory guard, a malformed reply) the
// held set stays and keeps answering, and the class is in Status. A pull that completes and finds the segment missing
// is an answer, not an error: the held set is dropped and the lookups answer the policy (NotMember, or Unknown with
// MissingUnknown), and Status shows ClassMissing with the version. While the segment is missing, every Refresh whose
// Version works pulls the members again (the token cannot tell that the segment came back); a Version that reports it
// missing again pulls nothing. A writer that deletes or replaces a set must change the version: a deletion under an
// unchanged token is not seen. Refresh returns the missing error once, when it learns it. The version is read before
// the members, so a write that lands between the two makes the next Refresh pull again.
func (h *Holder) Refresh(ctx context.Context) error {
	h.refresh.Lock()
	defer h.refresh.Unlock()

	cur := h.cur.Load()

	version, err := h.src.Version(ctx, h.name)
	if errors.Is(err, segment.ErrSegmentMissing) && ctx.Err() == nil {
		// the store answered that the segment does not exist: same answer as a stream that reports it
		return h.markMissing(cur, "", fmt.Errorf("snapshot of segment %q: %w", h.name, err))
	}

	if err != nil {
		return h.fail(ctx, cur, ClassVersion, err)
	}

	// "missing" is a state of the holder, not a version: after a missing answer the token says nothing (a segment that
	// comes back may carry the very token it had, or the same absent token), so the members are pulled again until the
	// store has the segment.
	if cur != nil && cur.set != nil && !cur.missing && cur.version == version {
		held := *cur // a poll that worked: the outage is over
		held.lastErr = ClassNone

		h.cur.Store(&held)

		return nil
	}

	set, class, err := h.pull(ctx)
	if class == ClassMissing {
		return h.markMissing(cur, version, err)
	}

	if err != nil {
		return h.fail(ctx, cur, class, err)
	}

	next := &snap{set: set, version: version, loadedAt: clock.Now(), pulls: 1}
	if cur != nil {
		next.pulls = cur.pulls + 1
	}

	h.cur.Store(next)

	return nil
}

// pull streams the members into a new set. It refuses a set over the guard as soon as it crosses it.
func (h *Holder) pull(ctx context.Context) (*segment.MemberSet, Class, error) {
	var (
		builder segment.MemberSetBuilder
		bytes   int64
		read    int
	)

	for id, err := range h.src.Members(ctx, h.name) {
		if err != nil {
			if errors.Is(err, segment.ErrSegmentMissing) {
				return nil, ClassMissing, fmt.Errorf("snapshot of segment %q: %w", h.name, err)
			}

			return nil, ClassRead, fmt.Errorf("snapshot of segment %q: reading the members: %w", h.name, err)
		}

		read++
		if read%checkEvery == 0 && ctx.Err() != nil {
			return nil, ClassCanceled, fmt.Errorf("snapshot of segment %q: %w", h.name, ctx.Err())
		}

		if !builder.Add(id) {
			continue
		}

		bytes += int64(len(id))

		if builder.Len() > h.opts.MaxMembers {
			return nil, ClassTooLarge, fmt.Errorf("snapshot of segment %q: %w: more than %d",
				h.name, segment.ErrTooManyMembers, h.opts.MaxMembers)
		}

		if bytes > h.opts.MaxBytes {
			return nil, ClassTooLarge, fmt.Errorf("snapshot of segment %q: %w: more than %d bytes",
				h.name, segment.ErrMembersTooLarge, h.opts.MaxBytes)
		}
	}

	// a source that ignores its context may end the stream quietly after the cancel: that set is not trusted
	if ctx.Err() != nil {
		return nil, ClassCanceled, fmt.Errorf("snapshot of segment %q: %w", h.name, ctx.Err())
	}

	return builder.Build(), ClassNone, nil
}

// fail records the class on the held snapshot (the set stays) and returns the error.
func (h *Holder) fail(ctx context.Context, cur *snap, class Class, err error) error {
	if ctx.Err() != nil {
		class = ClassCanceled
	}

	held := snap{lastErr: class}
	if cur != nil {
		held = *cur
		held.lastErr = class
	}

	h.cur.Store(&held)

	return err
}

// markMissing records an answer of the store: the segment does not exist. The held set is dropped and the lookups
// answer the policy. The error is returned when the answer is new, and nil when the segment was already known to be
// missing under the same version token.
func (h *Holder) markMissing(cur *snap, version string, err error) error {
	next := &snap{version: version, lastErr: ClassMissing, missing: true, loadedAt: clock.Now()}
	if h.opts.Missing.Effective() == segment.MissingNotMember {
		next.set = &segment.MemberSet{} // nobody is in a segment that does not exist
	}

	known := false

	if cur != nil {
		next.pulls = cur.pulls
		known = cur.missing && cur.version == version

		if known {
			next.loadedAt = cur.loadedAt
		}
	}

	h.cur.Store(next)

	if known {
		return nil
	}

	return err
}
