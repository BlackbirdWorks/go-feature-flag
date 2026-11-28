package relaycore

import (
	"maps"
	"sync"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// maxPending is a guard against an engine that stalls: the snapshots that wait for a decision are forgotten from the
// oldest when there are more. In the normal course the guard never acts, because the snapshots older than the one the
// engine took last are dropped as they are offered (the engine keeps the latest content and never decides an older
// revision), so only the one in flight and the newer ones wait. A snapshot that was forgotten is never written, and the
// state directory keeps the generation it holds, so a stall costs freshness, never correctness.
const maxPending = 64

// persister makes the state directory hold what the engine applied. The reader hands it every snapshot it emits; the
// engine tells which revision it decided. Only a snapshot that the engine applied is written, with the content the
// engine serves: an object it could not decode keeps the last good content, as in the runtime.
type persister struct {
	save func(manifestreader.Persist)
	// taken tells the revision that the engine took last.
	taken   func() uint64
	pending map[uint64]manifestreader.Persist
	// applied is the objects of the state that was last handed to the store.
	applied map[string]manifestreader.ObjectState
	mu      sync.Mutex
}

func newPersister(save func(manifestreader.Persist), taken func() uint64) *persister {
	return &persister{save: save, taken: taken, pending: map[uint64]manifestreader.Persist{}}
}

// offer keeps a snapshot until the engine decided it. It runs on the polling goroutine of the reader.
func (p *persister) offer(persist manifestreader.Persist) {
	if persist.Snapshot == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.pending[persist.Snapshot.Revision] = persist

	// the engine never decides a revision older than the one it took: those can be forgotten
	taken := p.taken()
	for revision := range p.pending {
		if revision < taken {
			delete(p.pending, revision)
		}
	}

	for len(p.pending) > maxPending {
		oldest := persist.Snapshot.Revision
		for revision := range p.pending {
			oldest = min(oldest, revision)
		}

		delete(p.pending, oldest)
	}
}

// decided is told the result of a round and the object status of the runtime that is stored. It hands the store the
// snapshot of the revision when the round applied it, and forgets every snapshot up to the revision. The store only
// queues the write: the engine never waits for the disk.
func (p *persister) decided(res *flagruntime.RoundResult, objects map[string]*flagruntime.ObjectStatus) {
	if res.Revision == 0 {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	persist, held := p.pending[res.Revision]

	for revision := range p.pending {
		if revision <= res.Revision {
			delete(p.pending, revision)
		}
	}

	if !held || res.Outcome != flagruntime.OutcomeApplied {
		return
	}

	persist.Snapshot = p.served(persist.Snapshot, objects)
	p.applied = persist.Snapshot.Objects

	p.save(persist)
}

// served returns the snapshot with the content that the runtime serves: an object that the engine could not decode
// is written with the content it kept (the last one written), as a stale object; when there is none, as missing.
func (p *persister) served(
	snap *manifestreader.Snapshot,
	objects map[string]*flagruntime.ObjectStatus,
) *manifestreader.Snapshot {
	var out map[string]manifestreader.ObjectState

	for name := range snap.Objects {
		if status := objects[name]; status == nil || status.State != flagruntime.ObjectInvalid {
			continue
		}

		if out == nil {
			out = make(map[string]manifestreader.ObjectState, len(snap.Objects))
			maps.Copy(out, snap.Objects)
		}

		rejected := snap.Objects[name]
		out[name] = p.lastGood(name, &rejected)
	}

	if out == nil {
		return snap
	}

	kept := *snap
	kept.Objects = out

	return &kept
}

// lastGood is the state of an object that the engine rejected: the content that was written for it before, or, when
// there is none, an object that is listed and has no content.
func (p *persister) lastGood(name string, rejected *manifestreader.ObjectState) manifestreader.ObjectState {
	before, ok := p.applied[name]
	if ok && before.Bytes != nil && before.Kind == rejected.Kind && before.Format == rejected.Format {
		before.State, before.Reason = manifestreader.StateStale, manifestreader.ReasonNotRestored

		return before
	}

	missing := *rejected
	missing.State, missing.Reason, missing.Bytes = manifestreader.StateMissing, manifestreader.ReasonNotRestored, nil

	return missing
}
