package relaycore

import (
	"maps"
	"sync"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// maxPending is a guard against an engine that stalls: the snapshots that wait for a decision are forgotten from the
// oldest when there are more. In the normal course the guard never acts, because the snapshots older than the one the
// engine took last are dropped as they are offered (the engine keeps the latest content and never decides an older
// revision), so only the one in flight and the newer ones wait. A snapshot that was forgotten is never written, and the
// state directory keeps the generation it holds, so a stall costs freshness, never correctness.
const maxPending = 64

// persister makes the state directory hold what the runtime serves. The reader offers every snapshot it emits (the
// manifest and the bytes it fetched); the engine tells which revision it stored and what the runtime holds per object
// (the checksum). What is written comes from the runtime: the manifest of the generation it runs, and for every object
// the bytes of the checksum the runtime holds, never the latest bytes the reader has for the name. The offer and the
// decision can come in either order: the write is made when both are there.
type persister struct {
	save func(manifestreader.Persist)
	// taken tells the revision that the engine took last.
	taken   func() uint64
	pending map[uint64]manifestreader.Persist
	// held is the content of the objects of the state that was last written, by kind and checksum: the bytes of an
	// object that the runtime keeps although the reader moved on.
	held map[string]content
	// applied is the content decision that waits for its snapshot, nil when there is none.
	applied *appliedRound
	// visits counts the objects the indexes visited: a test asserts that it grows with the objects, not with their square.
	visits uint64
	mu     sync.Mutex
}

// appliedRound is a round that stored content: its revision and what the runtime holds per object.
type appliedRound struct {
	objects  map[string]*flagruntime.ObjectStatus
	revision uint64
}

// content is the bytes of one object, with what a state directory needs to name them.
type content struct {
	kind, format string
	sum          controlplane.Checksum
	bytes        []byte
}

func newPersister(save func(manifestreader.Persist), taken func() uint64) *persister {
	return &persister{
		save: save, taken: taken, pending: map[uint64]manifestreader.Persist{}, held: map[string]content{},
	}
}

func contentKey(kind, sum string) string { return kind + "\x00" + sum }

// offer keeps a snapshot until the engine stored its content. It runs on the polling goroutine of the reader.
func (p *persister) offer(persist manifestreader.Persist) {
	if persist.Snapshot == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.pending[persist.Snapshot.Revision] = persist

	p.flush()

	// the engine never decides a revision older than the one it took: those can be forgotten
	p.forgetBelow(p.taken())

	for len(p.pending) > maxPending {
		oldest := persist.Snapshot.Revision
		for revision := range p.pending {
			oldest = min(oldest, revision)
		}

		delete(p.pending, oldest)
	}
}

// decided is told the result of a round and the object status of the runtime that is stored. A round that stored
// content makes the state to write; a round that did not (a rejected content, a bootstrap change alone) writes
// nothing and leaves the state directory as it is. The store only queues the write: the engine never waits for disk.
func (p *persister) decided(res *flagruntime.RoundResult, objects map[string]*flagruntime.ObjectStatus) {
	if res.Revision == 0 {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if res.ContentApplied {
		p.applied = &appliedRound{revision: res.Revision, objects: objects}
	}

	p.flush()
	p.forgetBelow(res.Revision + 1)
}

// forgetBelow drops the snapshots under the revision, except the one that waits to be written.
func (p *persister) forgetBelow(revision uint64) {
	for rev := range p.pending {
		if rev < revision && (p.applied == nil || p.applied.revision != rev) {
			delete(p.pending, rev)
		}
	}
}

// flush writes the applied round when its snapshot is there.
func (p *persister) flush() {
	if p.applied == nil {
		return
	}

	persist, ok := p.pending[p.applied.revision]
	if !ok {
		return
	}

	persist.Snapshot = p.served(persist.Snapshot, p.applied.objects)

	delete(p.pending, p.applied.revision)

	p.applied = nil

	p.save(persist)
}

// served returns the snapshot as the runtime holds it: per object the bytes of the held checksum. An object the
// runtime holds as listed is ok; one it holds from earlier content (stale, missing or undecodable now) is stale with
// those bytes; one it holds nothing of is missing. The cost is linear in the objects of the manifest: the snapshot is
// indexed by kind and checksum once, and every object is a lookup.
func (p *persister) served(
	snap *manifestreader.Snapshot, objects map[string]*flagruntime.ObjectStatus,
) *manifestreader.Snapshot {
	out := make(map[string]manifestreader.ObjectState, len(snap.Objects))
	now := make(map[string]content, len(snap.Objects))
	finder := &finder{p: p, own: p.index(snap)}

	for name, listed := range snap.Objects {
		state := manifestreader.ObjectState{
			Kind: listed.Kind, Format: listed.Format, Checksum: listed.Checksum,
			State: manifestreader.StateMissing, Reason: manifestreader.ReasonNotRestored,
		}

		status := objects[name]
		if status != nil && status.Checksum != "" {
			found, ok := finder.find(listed.Kind, status.Checksum)
			if ok {
				state.Bytes, state.Format = found.bytes, found.format
				state.Checksum = found.sum

				if status.State == flagruntime.ObjectOK && status.Checksum == listed.Checksum.Value {
					state.State, state.Reason = manifestreader.StateOK, ""
				} else {
					state.State = manifestreader.StateStale
				}

				now[contentKey(listed.Kind, status.Checksum)] = found
			}
		}

		out[name] = state
	}

	p.held = now

	kept := *snap
	kept.Objects = out

	return &kept
}

// index lists the bytes of a snapshot by kind and checksum.
func (p *persister) index(snap *manifestreader.Snapshot) map[string]content {
	out := make(map[string]content, len(snap.Objects))

	for _, obj := range snap.Objects {
		p.visits++

		if obj.Bytes != nil && (obj.State == manifestreader.StateOK || obj.State == manifestreader.StateStale) {
			out[contentKey(obj.Kind, obj.Checksum.Value)] = content{
				kind: obj.Kind, format: obj.Format, sum: obj.Checksum, bytes: obj.Bytes,
			}
		}
	}

	return out
}

// finder looks the bytes of a kind and checksum up: in the snapshot being written, in what was written last, and
// only when both miss in the other snapshots that wait (indexed once, on the first miss).
type finder struct {
	p      *persister
	own    map[string]content
	others map[string]content
	built  bool
}

func (f *finder) find(kind, sum string) (content, bool) {
	key := contentKey(kind, sum)

	if found, ok := f.own[key]; ok {
		return found, true
	}

	if found, ok := f.p.held[key]; ok {
		return found, true
	}

	if !f.built {
		f.built = true
		f.others = map[string]content{}

		for _, other := range f.p.pending {
			maps.Copy(f.others, f.p.index(other.Snapshot))
		}
	}

	found, ok := f.others[key]

	return found, ok
}
