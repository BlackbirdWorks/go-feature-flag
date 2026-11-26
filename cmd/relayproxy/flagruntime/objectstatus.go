package flagruntime

// ObjectStatus is the status of one source object.
type ObjectStatus struct {
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
	// Flags is the number of flags held (flag sets held, for a document).
	Flags int `json:"flags"`
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
