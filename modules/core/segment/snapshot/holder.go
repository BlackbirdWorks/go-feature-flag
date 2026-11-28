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

// MaxQuietStep is the longest pause between two version reads while a refresh waits for the version to go quiet.
const MaxQuietStep = time.Second

// quietDivisor is how many quiet steps fit in one debounce.
const quietDivisor = 10

// QuietStep is the pause between two version reads while a refresh waits for the version to go quiet: a tenth of the
// debounce, at most [MaxQuietStep] and at least one millisecond (never zero).
func QuietStep(debounce time.Duration) time.Duration {
	return max(time.Millisecond, min(MaxQuietStep, debounce/quietDivisor))
}

// Clock is the time of a holder: the instant that a debounce is measured from, and the wait between two version reads.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// Sleep waits for d and returns the error of ctx when it ends first.
	Sleep(ctx context.Context, d time.Duration) error
}

type systemClock struct{}

// Now is the real time: a debounce is a measured duration, not a stamp.
func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) Sleep(ctx context.Context, pause time.Duration) error {
	timer := time.NewTimer(pause)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("waiting for the version to go quiet: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

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
	// ClassSet means the store answered with an error about this set (a key of the wrong type, a reply that is not
	// the one of the command): the store works, the set cannot be read.
	ClassSet Class = "set"
	// ClassCanceled means the context ended during the refresh.
	ClassCanceled Class = "canceled"
)

// checkEvery is how many members are read between two looks at the context.
const checkEvery = 1024

// Options bounds the memory of one holder. A zero field means the default of the segment package.
type Options struct {
	// Clock is the time of the holder; nil means the real clock and a timer.
	Clock Clock
	// Missing is what a segment that does not exist means: the default (empty) is [segment.MissingNotMember], an empty
	// set; [segment.MissingUnknown] drops the set and answers Unknown.
	Missing segment.MissingKey
	// MaxMembers is the most distinct members; more refuses the pull and keeps the last good set.
	MaxMembers int
	// MaxBytes is the most bytes of the distinct members (the length of the ids), same rule.
	MaxBytes int64
	// Debounce bounds how long a refresh waits for a set that changes while it is pulled. Zero (the default) turns the
	// check off: the first pull is installed and the version is not read again, so a set replaced during the pull can be
	// served mixed until the next poll.
	Debounce time.Duration
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
	// Unsettled is the number of sets installed without a stable version: the version kept moving until the debounce
	// elapsed, so the set was installed under the version read before its pull and the next poll pulls again.
	Unsettled uint64
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
	unstable uint64
	missing  bool // the store answered that the segment does not exist: no set is held
}

// failing reports whether the last refresh failed: an error that is not the answer "the segment is missing".
func (s *snap) failing() bool { return s.lastErr != ClassNone && s.lastErr != ClassMissing }

// Holder keeps the member set of one segment. Reads are lock-free; Refresh is driven by the caller (it starts no
// goroutine and no timer) and calls are serialised.
type Holder struct {
	clk     Clock
	src     segment.SnapshotSource
	cur     atomic.Pointer[snap]
	name    string
	opts    Options
	refresh sync.Mutex
	// debounce is the debounce in nanoseconds.
	debounce atomic.Int64
	overdue  atomic.Bool
}

var (
	_ segment.Provider      = (*Holder)(nil)
	_ segment.StaleReporter = (*Holder)(nil)
	_ segment.Live          = (*Holder)(nil)
)

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

	holder := &Holder{src: src, name: name, opts: opts, clk: opts.Clock}
	if holder.clk == nil {
		holder.clk = systemClock{}
	}

	holder.SetDebounce(opts.Debounce)

	return holder, nil
}

// SetDebounce changes the debounce for the next refresh; zero or less turns the check off. A holder that a new
// generation of the definitions takes over gets the debounce of the new definition this way.
func (h *Holder) SetDebounce(debounce time.Duration) { h.debounce.Store(int64(max(debounce, 0))) }

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

// Answer implements [segment.Live]: the membership from the held set with the status of the answer, and no store call.
// A set that was never loaded answers Unknown with a store error; a set older than it should be (the last refresh
// failed, or the set is over the guard) answers with a stale status; a segment that is missing is an answer, healthy.
// It does not allocate and does not lock.
func (h *Holder) Answer(id string) (segment.State, segment.Status) {
	cur := h.cur.Load()
	if cur == nil {
		return segment.Unknown, segment.StatusStoreError
	}

	if cur.set == nil {
		if cur.missing && (cur.lastErr == ClassMissing || cur.lastErr == ClassNone) {
			// the store answered that the segment is missing and the policy is unknown: that is an answer, not a failure
			return segment.Unknown, segment.StatusHealthy
		}

		return segment.Unknown, segment.StatusStoreError
	}

	state := segment.NotMember
	if cur.set.Contains(id) {
		state = segment.Member
	}

	if cur.failing() || h.overdue.Load() {
		return state, segment.StatusStale
	}

	return state, segment.StatusHealthy
}

// SetOverdue tells the holder that its set could not be refreshed for longer than the caller allows: the answers are
// stale (the set is still used) until the caller clears it, which it does when a refresh works. The holder has no clock
// of its own for this, and the lookups never read one.
func (h *Holder) SetOverdue(overdue bool) { h.overdue.Store(overdue) }

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

// Stale reports whether the held set is served after a failed refresh: the last known good set, older than it should
// be. It implements [segment.StaleReporter]. A segment that is missing is an answer, not a stale set.
func (h *Holder) Stale() bool {
	cur := h.cur.Load()

	return cur != nil && cur.set != nil && (cur.failing() || h.overdue.Load())
}

// Status returns the state of the holder.
func (h *Holder) Status() Status {
	cur := h.cur.Load()
	if cur == nil {
		return Status{}
	}

	if cur.set == nil {
		return Status{Version: cur.version, LastError: cur.lastErr, Unsettled: cur.unstable}
	}

	return Status{
		Version: cur.version, LastError: cur.lastErr, Members: cur.set.Len(), Age: clock.Now().Sub(cur.loadedAt),
		Pulls: cur.pulls, Unsettled: cur.unstable, Loaded: true,
	}
}

// Refresh reads the version of the segment and, only when it differs from the held one, pulls the members and swaps
// them in. On an error (the store is down, a timeout, a stream that broke, the memory guard, a malformed reply) the
// held set stays and keeps answering, and the class is in Status. A pull that completes and finds the segment missing
// is an answer, not an error: the held set is dropped and the lookups answer the policy (NotMember, or Unknown with
// MissingUnknown), and Status shows ClassMissing with the version. While the segment is missing, every Refresh whose
// Version works pulls the members again (the token cannot tell that the segment came back); a Version that reports it
// missing again pulls nothing. A writer that deletes or replaces a set must change the version, and the version must
// never return to an earlier value: a deletion under an unchanged token is not seen. Refresh returns the missing error
// once, when it learns it.
//
// The version is read before the members and again after them. When it moved during the pull the pulled set may mix
// two sets: it is dropped, and the refresh reads only the version until it is quiet (two reads in a row are equal, a
// pause of [QuietStep] apart) or the debounce has passed since the first pull started, then pulls once more. When the
// debounce has passed and the version still moves, the latest pull is installed under the version read before it (the
// next poll pulls again) and counted in [Status.Unsettled]; that is a success. With no debounce (the default) the
// first pull is installed and the version is not read again. A source with no version ([segment.ErrNoVersion]) has
// nothing to compare and is pulled once.
func (h *Holder) Refresh(ctx context.Context) error {
	h.refresh.Lock()
	defer h.refresh.Unlock()

	cur := h.cur.Load()

	version, err := h.src.Version(ctx, h.name)
	if errors.Is(err, segment.ErrSegmentMissing) && ctx.Err() == nil {
		// the store answered that the segment does not exist: same answer as a stream that reports it
		return h.markMissing(cur, "", fmt.Errorf("snapshot of segment %q: %w", h.name, err))
	}

	noVersion := errors.Is(err, segment.ErrNoVersion)
	if noVersion {
		version, err = "", nil // nothing tells a change: the members are pulled on every refresh
	}

	if err != nil {
		return h.fail(ctx, cur, ClassVersion, err)
	}

	// "missing" is a state of the holder, not a version: after a missing answer the token says nothing (a segment that
	// comes back may carry the very token it had, or the same absent token), so the members are pulled again until the
	// store has the segment.
	if upToDate(cur, version, noVersion) {
		held := *cur // a poll that worked: the outage is over
		held.lastErr = ClassNone

		h.cur.Store(&held)

		return nil
	}

	got, err := h.settle(ctx, version, noVersion)
	set, version, class := got.set, got.version, got.class

	if class == ClassMissing && ctx.Err() == nil {
		return h.markMissing(cur, version, err)
	}

	if err != nil {
		return h.fail(ctx, cur, class, err)
	}

	next := &snap{set: set, version: version, loadedAt: clock.Now(), pulls: 1}
	if cur != nil {
		next.pulls = cur.pulls + 1
		next.unstable = cur.unstable
	}

	if got.unstable {
		next.unstable++
	}

	h.cur.Store(next)

	return nil
}

// settled is what [Holder.settle] pulled: the set, the version that it belongs to and the class of a failure.
type settled struct {
	set      *segment.MemberSet
	version  string
	class    Class
	unstable bool // installed although the version still moved: the debounce had passed
}

// settle pulls the members and returns a set that is consistent with its version, or the latest pull once the debounce
// has passed. version is the one read before the first pull.
//
// Memory: a pulled set that is not returned is unreachable before the next pull allocates, so a refresh holds the
// held set plus one pulled set at most. Nothing but the loop body names a pulled set: it is returned, or dropped when
// the body ends, before the wait and the next pull.
func (h *Holder) settle(ctx context.Context, version string, noVersion bool) (settled, error) {
	debounce := time.Duration(h.debounce.Load())
	start := h.clk.Now()

	for {
		got, again, err := h.pullChecked(ctx, version, noVersion, debounce, start)
		if !again {
			return got, err
		}

		// the pulled set was dropped by pullChecked: only the version is read until it is quiet
		version, err = h.waitQuiet(ctx, got.version, debounce, start)
		if err != nil {
			return settled{version: version, class: waitClass(err)}, err
		}
	}
}

// waitClass is the class of a failure while the version is read for quiet: the segment that went missing, or the
// version that cannot be read.
func waitClass(err error) Class {
	if errors.Is(err, segment.ErrSegmentMissing) {
		return ClassMissing
	}

	return ClassVersion
}

// pullChecked pulls once and reads the version again. It reports again when the version moved and the debounce has not
// passed: the pulled set is dropped (it goes out of scope here), and got.version is the version that moved to.
func (h *Holder) pullChecked(
	ctx context.Context, version string, noVersion bool, debounce time.Duration, start time.Time,
) (settled, bool, error) {
	set, class, err := h.pull(ctx)
	if err != nil || noVersion || debounce <= 0 {
		return settled{set: set, version: version, class: class}, false, err
	}

	after, verr := h.src.Version(ctx, h.name)
	if errors.Is(verr, segment.ErrSegmentMissing) && ctx.Err() == nil {
		return settled{version: after, class: ClassMissing},
			false, fmt.Errorf("snapshot of segment %q: %w", h.name, verr)
	}

	if verr != nil {
		return settled{version: version, class: ClassVersion}, false,
			fmt.Errorf("snapshot of segment %q: reading the version after the pull: %w", h.name, verr)
	}

	if after == version {
		return settled{set: set, version: version}, false, nil
	}

	if h.clk.Now().Sub(start) >= debounce {
		return settled{set: set, version: version, unstable: true}, false, nil
	}

	return settled{version: after}, true, nil
}

// waitQuiet reads only the version, a [QuietStep] apart, until two reads in a row are equal or the debounce has passed
// since start. It returns the last version read, which the next pull starts under.
func (h *Holder) waitQuiet(ctx context.Context, last string, debounce time.Duration, start time.Time) (string, error) {
	step := QuietStep(debounce)

	for {
		err := h.clk.Sleep(ctx, step)
		if err != nil {
			return last, fmt.Errorf("snapshot of segment %q: waiting for the version to go quiet: %w", h.name, err)
		}

		next, err := h.src.Version(ctx, h.name)
		if err != nil {
			return last, fmt.Errorf("snapshot of segment %q: reading the version while it moves: %w", h.name, err)
		}

		if next == last || h.clk.Now().Sub(start) >= debounce {
			return next, nil
		}

		last = next
	}
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
	if ctx.Err() == nil && segment.IsStoreAnswered(err) {
		class = ClassSet
	}

	if ctx.Err() != nil {
		class = ClassCanceled
		err = fmt.Errorf("snapshot of segment %q: %w", h.name, ctx.Err()) // the end of the context is the cause
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

// upToDate tells whether the held set is the one that the version names: a set is held, the segment is not in the
// missing state (a token says nothing about a segment that came back), and the store has a version that did not move.
func upToDate(cur *snap, version string, noVersion bool) bool {
	return !noVersion && cur != nil && cur.set != nil && !cur.missing && cur.version == version
}
