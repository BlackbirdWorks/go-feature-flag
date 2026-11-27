package segment

import (
	"context"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strconv"
	"sync"
)

// AbsentVersion is the version token that [MemorySource] gives a segment it does not hold.
const AbsentVersion = "absent"

// Op names a call of [MemorySource] that a test can make fail or count.
type Op string

const (
	// OpVersion is [MemorySource.Version].
	OpVersion Op = "version"
	// OpMembers is [MemorySource.Members].
	OpMembers Op = "members"
	// OpContains is [MemorySource.Contains].
	OpContains Op = "contains"
)

// MemorySource is the in-memory reference implementation of every store interface of the package: a [Provider]
// (lookup), a [SnapshotSource], a [Versioner], an [Existencer] and a [ChangeNotifier]. Unlike [MemoryProvider] it is
// mutable, with a version for each segment, injectable failures and a hook inside the member stream, so a test can
// drive any state of a store. It is safe for concurrent use.
type MemorySource struct {
	sets     map[string]*MemberSet
	versions map[string]string
	fails    map[Op]error
	calls    map[Op]int
	subs     map[*subscription]struct{}
	pinned   map[string]struct{} // every token ever pinned: a counter version never takes one
	hook     func(segment string, yielded int)
	midErr   error
	midAfter int
	counter  int
	mu       sync.Mutex
}

var (
	_ Provider       = (*MemorySource)(nil)
	_ SnapshotSource = (*MemorySource)(nil)
	_ Versioner      = (*MemorySource)(nil)
	_ Existencer     = (*MemorySource)(nil)
	_ ChangeNotifier = (*MemorySource)(nil)
)

// NewMemorySource returns a source holding the members, each segment at a fresh version.
func NewMemorySource(members map[string][]string) *MemorySource {
	src := &MemorySource{
		sets:     map[string]*MemberSet{},
		versions: map[string]string{},
		fails:    make(map[Op]error),
		calls:    make(map[Op]int),
		subs:     map[*subscription]struct{}{},
		midAfter: -1, pinned: map[string]struct{}{},
	}

	for name, ids := range members {
		src.Set(name, ids)
	}

	return src
}

// Set replaces the members of a segment (creating it) and gives it a new version.
func (m *MemorySource) Set(name string, ids []string) {
	m.mu.Lock()
	m.sets[name] = NewMemberSet(ids)
	m.versions[name] = m.nextVersion()
	m.mu.Unlock()
	m.publish(name)
}

// Delete removes a segment; its version goes back to [AbsentVersion].
func (m *MemorySource) Delete(name string) {
	m.mu.Lock()
	delete(m.sets, name)
	delete(m.versions, name)
	m.mu.Unlock()
	m.publish(name)
}

// PinVersion sets the version token of a segment without touching its members (the way a writer bumps a version
// key). An empty token unpins: the segment gets a fresh counter version (or the absent one when it does not exist).
func (m *MemorySource) PinVersion(name, token string) {
	m.mu.Lock()
	switch {
	case token != "":
		m.versions[name] = token
		m.pinned[token] = struct{}{}
	case m.sets[name] != nil:
		m.versions[name] = m.nextVersion()
	default:
		delete(m.versions, name)
	}
	m.mu.Unlock()
	m.publish(name)
}

// Fail makes every following call of kind call return err; nil heals it.
func (m *MemorySource) Fail(call Op, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err == nil {
		delete(m.fails, call)

		return
	}

	m.fails[call] = err
}

// FailMembersAfter makes the member stream yield err after n members (n zero: before the first); a negative n heals it.
func (m *MemorySource) FailMembersAfter(n int, err error) {
	m.mu.Lock()
	m.midAfter, m.midErr = n, err
	m.mu.Unlock()
}

// SetMembersHook sets a function that the member stream calls before it yields member number yielded (from zero) of
// a segment, outside the lock of the source: the hook may block (a gate) or cancel a context.
func (m *MemorySource) SetMembersHook(hook func(segment string, yielded int)) {
	m.mu.Lock()
	m.hook = hook
	m.mu.Unlock()
}

// Calls is the number of calls of op so far.
func (m *MemorySource) Calls(op Op) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.calls[op]
}

// Version implements [Versioner].
func (m *MemorySource) Version(ctx context.Context, name string) (string, error) {
	err := m.enter(ctx, OpVersion)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if token, ok := m.versions[name]; ok {
		return token, nil
	}

	return AbsentVersion, nil
}

// Members implements [SnapshotSource].
func (m *MemorySource) Members(ctx context.Context, name string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		err := m.enter(ctx, OpMembers)
		if err != nil {
			yield("", err)

			return
		}

		m.mu.Lock()
		set, ok := m.sets[name]
		hook, midAfter, midErr := m.hook, m.midAfter, m.midErr
		m.mu.Unlock()

		if !ok {
			yield("", ErrSegmentMissing)

			return
		}

		yielded := 0

		for id := range set.All() {
			if hook != nil {
				hook(name, yielded)
			}

			if midAfter >= 0 && yielded == midAfter {
				yield("", midErr)

				return
			}

			if !yield(id, nil) {
				return
			}

			yielded++
		}
	}
}

// Contains implements [Provider]: a segment the source does not hold stays Unknown.
func (m *MemorySource) Contains(ctx context.Context, lookups []Lookup) error {
	err := m.enter(ctx, OpContains)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range lookups {
		set, ok := m.sets[lookups[i].Segment]
		if !ok {
			continue
		}

		lookups[i].State = NotMember
		if set.Contains(lookups[i].ID) {
			lookups[i].State = Member
		}
	}

	return nil
}

// Exists implements [Existencer].
func (m *MemorySource) Exists(_ context.Context, names []string) ([]State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	states := make([]State, len(names))
	for i, name := range names {
		states[i] = NotMember
		if _, ok := m.sets[name]; ok {
			states[i] = Member
		}
	}

	return states, nil
}

// subscription is one consumer of Changes: the latest version of each changed segment, and a signal.
type subscription struct {
	pending map[string]string
	signal  chan struct{}
	mu      sync.Mutex
}

// Changes implements [ChangeNotifier]. Changes of one segment between two reads are coalesced into the latest one.
func (m *MemorySource) Changes(ctx context.Context) (iter.Seq2[Change, error], error) {
	err := ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("memory source changes: %w", err)
	}

	sub := &subscription{pending: map[string]string{}, signal: make(chan struct{}, 1)}

	m.mu.Lock()
	m.subs[sub] = struct{}{}
	m.mu.Unlock()

	unsubscribe := func() {
		m.mu.Lock()
		delete(m.subs, sub)
		m.mu.Unlock()
	}
	stop := context.AfterFunc(ctx, unsubscribe)

	return func(yield func(Change, error) bool) {
		defer func() {
			stop()
			unsubscribe()
		}()

		for {
			select {
			case <-ctx.Done():
				yield(Change{}, fmt.Errorf("memory source changes: %w", ctx.Err()))

				return
			case <-sub.signal:
			}

			sub.mu.Lock()
			batch := sub.pending
			sub.pending = map[string]string{}
			sub.mu.Unlock()

			for _, name := range slices.Sorted(maps.Keys(batch)) {
				if !yield(Change{Segment: name, Version: batch[name]}, nil) {
					return
				}
			}
		}
	}, nil
}

// enter counts a call and returns its injected failure or the end of its context.
func (m *MemorySource) enter(ctx context.Context, kind Op) error {
	m.mu.Lock()
	m.calls[kind]++
	err := m.fails[kind]
	m.mu.Unlock()

	if err != nil {
		return fmt.Errorf("memory source %s: %w", kind, err)
	}

	err = ctx.Err()
	if err != nil {
		return fmt.Errorf("memory source %s: %w", kind, err)
	}

	return nil
}

// publish tells the subscribers the current version of a segment.
func (m *MemorySource) publish(name string) {
	m.mu.Lock()
	defer m.mu.Unlock() // read and write in one step: overlapping publishes keep their order

	token, ok := m.versions[name]

	if !ok {
		token = AbsentVersion
	}

	for sub := range m.subs {
		sub.mu.Lock()
		sub.pending[name] = token
		sub.mu.Unlock()

		select {
		case sub.signal <- struct{}{}:
		default:
		}
	}
}

// nextVersion returns a counter version that no pin ever used. The caller holds the lock.
func (m *MemorySource) nextVersion() string {
	for {
		m.counter++

		token := strconv.Itoa(m.counter)
		if _, pinned := m.pinned[token]; !pinned {
			return token
		}
	}
}
