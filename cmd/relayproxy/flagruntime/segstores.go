package flagruntime

import (
	"fmt"
	"strconv"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// SegmentStore is what the relay knows about one connection of the bootstrap file when it checks a dynamic segment:
// the kind of the connection and a value of the type that serves segments for that kind.
type SegmentStore struct {
	// Store is a value of the type that serves the segments of the connection kind: it is only inspected for the
	// [segment] interfaces it implements (a nil pointer of the type is enough), no connection is opened and no method
	// is called. It is nil for a kind that cannot serve segments (a bucket, a stream).
	Store any
	// Kind is the type of the connection, as the bootstrap file names it, for example "redis". It is used in the
	// reasons only.
	Kind string
}

// SegmentStores tells the relay which connections of the bootstrap file exist and what each can serve. The relay only
// uses the interfaces of [segment]: nothing here depends on a kind of store. A nil SegmentStores means that the
// connections are not known and the definitions are not checked against them.
type SegmentStores interface {
	// SegmentStore returns the facts of the named connection; false when the bootstrap file has no such connection.
	SegmentStore(connection string) (SegmentStore, bool)
}

// StoreTable is a [SegmentStores] held in a map by connection name.
type StoreTable map[string]SegmentStore

// SegmentStore returns the facts of the named connection.
func (t StoreTable) SegmentStore(connection string) (SegmentStore, bool) {
	facts, ok := t[connection]

	return facts, ok
}

// SegmentRejection is a segments object that the build met with a problem. It is reported once, for the build that
// met the content, not for each build that finds the same content again.
type SegmentRejection struct {
	// Object is the name of the segments object.
	Object string
	// Checksum is the checksum of the content.
	Checksum string
	// Reason says what is wrong, with the path of each field: it quotes no value of the document.
	Reason string
	// Definitions is the number of dynamic definitions that the connections of the bootstrap file cannot serve and
	// that were rejected alone: the object and its other definitions are applied. Zero means that the whole object was
	// rejected and keeps its last good content.
	Definitions int
}

// dynamicCheck is the outcome of checking the dynamic definitions of a document against the connections.
type dynamicCheck struct {
	// reasons are the path addressed reasons, in document order.
	reasons []string
	// names are the names of the definitions that have a reason, in document order.
	names []string
}

// checkDynamic checks every dynamic definition of the document against the connections of the bootstrap file: the
// connection exists, its kind can serve segments, and it serves the mode of the definition. The shape of a definition
// (the keys layout with the snapshot mode, the version block) is checked by [controlplane.ValidateSegments] before.
func checkDynamic(doc *controlplane.SegmentsDocument, stores SegmentStores, snapshotUnserved bool) dynamicCheck {
	var out dynamicCheck

	for i := range doc.Segments {
		def := &doc.Segments[i]
		if def.Connection == "" {
			continue
		}

		path := "segments[" + strconv.Itoa(i) + "]"
		before := len(out.reasons)

		var (
			facts SegmentStore
			ok    = true
		)

		if stores != nil {
			facts, ok = stores.SegmentStore(def.Connection)
		}

		switch {
		case stores == nil:
			// the connections are not known: only the mode is checked
			out.reasons = appendReason(out.reasons, path+".mode: ", snapshotReason(def, snapshotUnserved))
		case !ok:
			out.reasons = append(out.reasons, fmt.Sprintf(
				"%s.connection: %q is not a connection of the bootstrap file", path, def.Connection))
		case facts.Store == nil:
			out.reasons = append(out.reasons, fmt.Sprintf(
				"%s.connection: %q is a %q connection, which cannot serve segments", path, def.Connection, facts.Kind))
		default:
			err := segment.RequireMode(def.Name, segment.Mode(def.Mode), facts.Kind, facts.Store)
			if err != nil {
				out.reasons = append(out.reasons, path+".mode: "+err.Error())
			} else {
				out.reasons = appendReason(out.reasons, path+".mode: ", snapshotReason(def, snapshotUnserved))
			}
		}

		if len(out.reasons) > before {
			out.names = append(out.names, def.Name)
		}
	}

	return out
}

// snapshotReason is the reason that a definition is refused while the relay does not serve the snapshot mode, empty
// when it is not refused.
func snapshotReason(def *controlplane.SegmentDefinition, unserved bool) string {
	if unserved && segment.Mode(def.Mode).Effective() == segment.ModeSnapshot {
		return ErrSnapshotNotServed.Error()
	}

	return ""
}

func appendReason(reasons []string, prefix, reason string) []string {
	if reason == "" {
		return reasons
	}

	return append(reasons, prefix+reason)
}
