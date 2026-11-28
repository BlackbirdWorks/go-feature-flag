package flagruntime

import (
	"maps"
	"strings"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// A segments object is accepted and checked, not used yet: the relay decodes it strictly, validates it with the rules
// of the control-plane format, keeps its last good content when a later version is invalid, and reports it in the
// status. Its name resolves for the segmentSources of the flag sets, so a flag set that lists a good segments object
// is served; a flag set that lists one that was never good is an invalid entry and keeps the flags it had. The
// evaluation of segments follows in a later change.

// segState is the state of one segments object.
type segState struct {
	status    *ObjectStatus
	doc       *controlplane.SegmentsDocument // the last good document, parsed once for its checksum and shared
	sets      map[string]*segment.MemberSet  // the members of its static segments, built once and shared
	hashes    map[string]uint64              // the content hash of each definition (members included), built once
	rejected  map[string]struct{}            // the dynamic definitions that the connections cannot serve
	name      string
	checksum  string
	attempted string
	reason    string
	state     ObjectState
	segments  int
	loaded    bool
}

// segObject computes the state of one segments object; it returns prev itself when nothing changed.
func (st *stage) segObject(name string, obj *Object, prev *segState) *segState {
	state := obj.state()
	if state == ObjectStale && adoptsStale(obj, prev != nil && prev.loaded) {
		fresh := *obj
		fresh.State, fresh.Reason = ObjectOK, ""

		next := st.segObject(name, &fresh, prev)
		if next.state != ObjectOK {
			return next
		}

		held := *next
		held.state, held.reason = ObjectStale, obj.Reason
		held.status = segStatus(&held)

		return &held
	}

	if state != ObjectOK {
		return segKept(name, obj, state, prev)
	}

	sum := obj.checksum()
	if prev != nil && prev.state == ObjectOK && prev.checksum == sum {
		return prev
	}

	doc, err := parseSegments(obj.Bytes)

	st.emit(name, KindSegments, sum)

	if err != nil {
		if prev != nil && prev.state == ObjectInvalid && prev.attempted == sum {
			return prev
		}

		next := keepSeg(name, prev)
		next.state, next.reason, next.attempted = ObjectInvalid, err.Error(), sum
		next.status = segStatus(next)

		st.built.SegmentRejections = append(st.built.SegmentRejections,
			SegmentRejection{Object: name, Checksum: sum, Reason: next.reason})

		return next
	}

	check := st.noteDynamic(name, sum, doc)

	next := &segState{
		name: name, checksum: sum, state: ObjectOK, loaded: true, segments: len(doc.Segments),
		doc: doc, sets: memberSets(doc), hashes: definitionHashes(doc), rejected: nameSet(check.names),
	}
	next.status = segStatus(next)

	return next
}

// noteDynamic checks the dynamic definitions of an accepted document against the connections and lists the ones that
// are rejected, once for this content.
func (st *stage) noteDynamic(name, sum string, doc *controlplane.SegmentsDocument) dynamicCheck {
	check := checkDynamic(doc, st.b.opts.SegmentStores)
	if len(check.reasons) > 0 {
		st.built.SegmentRejections = append(st.built.SegmentRejections, SegmentRejection{
			Object: name, Checksum: sum, Reason: "the dynamic definitions that the connections cannot serve are " +
				"rejected, the others are applied: " + strings.Join(check.reasons, "; "),
			Definitions: len(check.names),
		})
	}

	return check
}

// parseSegments decodes and validates a segments document.
func parseSegments(body []byte) (*controlplane.SegmentsDocument, error) {
	doc, _, err := controlplane.UnmarshalSegments(body)
	if err != nil {
		return nil, err //nolint:wrapcheck // the decoder error is the reason of the status
	}

	err = controlplane.ValidateSegments(doc)
	if err != nil {
		return nil, err //nolint:wrapcheck // the validation error is the reason of the status
	}

	return doc, nil
}

func keepSeg(name string, prev *segState) *segState {
	next := &segState{name: name}

	if prev != nil && prev.loaded {
		next.checksum, next.segments, next.loaded = prev.checksum, prev.segments, true
		next.doc, next.sets, next.hashes, next.rejected = prev.doc, prev.sets, prev.hashes, prev.rejected
	}

	return next
}

// segKept handles a segments object that is stale, missing or pending: the last good content stays.
func segKept(name string, obj *Object, state ObjectState, prev *segState) *segState {
	if prev != nil && prev.state == state && prev.reason == obj.Reason {
		return prev
	}

	next := keepSeg(name, prev)
	next.state, next.reason = state, obj.Reason

	if prev != nil {
		next.attempted = prev.attempted
	}

	next.status = segStatus(next)

	return next
}

func segStatus(held *segState) *ObjectStatus {
	return &ObjectStatus{
		Name: held.name, Kind: KindSegments, State: held.state, Checksum: held.checksum, Reason: held.reason,
		Flags: held.segments,
	}
}

func (st *stage) putSeg(name string, held *segState) {
	if !st.cloneS {
		st.segs = maps.Clone(st.segs)
		st.cloneS = true
	}

	st.segs[name] = held
}

func (st *stage) delSeg(name string) {
	if !st.cloneS {
		st.segs = maps.Clone(st.segs)
		st.cloneS = true
	}

	delete(st.segs, name)
}

// segNamesChanged tells whether the set of names that segmentSources resolve to changed in this build.
func (st *stage) segNamesChanged(changed bool) bool { return changed || st.segGood }
