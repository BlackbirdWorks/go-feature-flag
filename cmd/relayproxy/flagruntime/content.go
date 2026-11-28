package flagruntime

import "github.com/blackbirdworks/semaphore/controlplane"

// ObjectState is the state of an [Object] in a [Content].
type ObjectState string

// The states of an input object. An empty state means [ObjectOK].
const (
	// ObjectOK: Bytes hold the verified content of the object.
	ObjectOK ObjectState = "ok"
	// ObjectStale: the content could not be refreshed; keep the last good content. A stale object that carries
	// Bytes and whose name this runtime never loaded (a restart that restored the manifest reader's state) is read
	// from them as the last good content, and is reported stale.
	ObjectStale ObjectState = "stale"
	// ObjectMissing: the object is listed but its content is not available; keep the last good content.
	ObjectMissing ObjectState = "missing"
	// ObjectPending: the object was never loaded.
	ObjectPending ObjectState = "pending"
	// ObjectInvalid is only reported in a [Status]: the bytes could not be decoded and the last good content
	// is kept. It is not accepted as input.
	ObjectInvalid ObjectState = "invalid"
)

// Kinds and formats of the objects that the runtime reads.
const (
	// KindFlags is an object of flags, parsed with the engine parser.
	KindFlags = controlplane.KindFlags
	// KindFlagSets is an object of flag-set documents.
	KindFlagSets = controlplane.KindFlagSets
	// KindSegments is an object of segments documents: accepted and validated, not used for evaluation yet.
	KindSegments = controlplane.KindSegments
)

// Content is one verified snapshot of published content. The manifest reader builds it; this package never
// reads a manifest. Objects of another kind than [KindFlags], [KindFlagSets] and [KindSegments] are ignored.
type Content struct {
	// Objects are the objects by manifest object name.
	Objects map[string]Object
	// Generation is the manifest generation of the content, at least 1.
	Generation uint64
}

// Object is one object of a [Content].
type Object struct {
	// Kind is [KindFlags], [KindFlagSets] or [KindSegments].
	Kind string
	// Format is "yaml", "json" or (flags only) "toml".
	Format string
	// Checksum identifies the bytes (the manifest value). When empty, the checksum is computed from Bytes.
	Checksum string
	// State is the state of the object; empty means [ObjectOK].
	State ObjectState
	// Reason says why the state is not ok; it is shown in the status.
	Reason string
	// Bytes are the verified bytes; only read when the state is ok, or stale for a name never loaded.
	Bytes []byte
}

func (o *Object) state() ObjectState {
	if o.State == "" {
		return ObjectOK
	}

	return o.State
}

func (o *Object) checksum() string {
	if o.Checksum != "" {
		return o.Checksum
	}

	return controlplane.ChecksumOf(o.Bytes).Value
}
