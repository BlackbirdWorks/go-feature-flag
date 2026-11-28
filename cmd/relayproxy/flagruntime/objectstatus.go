package flagruntime

import "time"

// ObjectStatus is the status of one source object.
type ObjectStatus struct {
	// Since is the time the object left the state ok, as this process saw it (the clock of the build that first
	// saw it not ok); zero for an object that is ok.
	Since time.Time `json:"since,omitzero"`
	// Name is the object name.
	Name string `json:"name"`
	// Kind is the object kind.
	Kind string `json:"kind"`
	// State is the state: ok, stale, missing, pending or invalid (undecodable, last good content kept).
	State ObjectState `json:"state"`
	// Checksum is the checksum of the content held.
	Checksum string `json:"checksum"`
	// Reason says why the state is not ok.
	Reason string `json:"reason"`
	// Rejected lists the flags that failed validation.
	Rejected []FlagRejection `json:"rejected"`
	// UnknownFieldPaths lists the first unknown field paths noticed when decoding this object (bounded).
	UnknownFieldPaths []string `json:"unknownFieldPaths,omitempty"`
	// Flags is the number of flags held (flag sets held, for a document).
	Flags int `json:"flags"`
	// UnknownFields counts the unknown fields noticed when decoding this object.
	UnknownFields int `json:"unknownFields,omitempty"`
}

// FlagRejection is one flag that failed validation.
type FlagRejection struct {
	// Flag is the flag key.
	Flag string `json:"flag"`
	// Reason says what is wrong.
	Reason string `json:"reason"`
	// Kept says that the flag keeps its previous definition.
	Kept bool `json:"kept"`
}
