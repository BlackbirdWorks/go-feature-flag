package model

// FlagSetInfo is the state of one flag set: ready, degraded or pending.
type FlagSetInfo struct {
	Name     string        `json:"name"`
	State    string        `json:"state"`
	Problems []ProblemInfo `json:"problems"`
	Flags    int           `json:"flags"`
	APIKeys  int           `json:"apiKeys"`
	Rejected int           `json:"rejected"`
}

// ProblemInfo is a reported problem of a flag set: a closed code and the object it is about. The free text of the
// problem is not served, for it can quote the content of an object.
type ProblemInfo struct {
	Code    string `json:"code"`
	Subject string `json:"subject,omitempty"`
}

// ObjectInfo is the state of one source object: ok, stale, missing, pending or invalid.
type ObjectInfo struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	State string `json:"state"`
	// Reason is the closed code of the failure of the last load of the object, empty when it loaded.
	Reason string `json:"reason,omitempty"`
	// ChecksumHeld is the checksum of the content served, ChecksumListed the one the manifest lists.
	ChecksumHeld   string `json:"checksumHeld"`
	ChecksumListed string `json:"checksumListed,omitempty"`
	Flags          int    `json:"flags"`
	Rejected       int    `json:"rejected"`
	// Kept is the number of rejected flags that are served from an earlier version of the object. The state directory
	// holds the bytes of the object, not that earlier version: a restart serves none of these flags.
	Kept int `json:"kept"`
}

// StateInfo is the state directory as /info shows it: whether one is in use, and whether the writes work. A write that
// fails never stops the relay from serving; this is where it shows.
type StateInfo struct {
	// LastSaveError is the closed code of the failure of the last write (no-space, denied, io, unsafe), empty when it
	// worked.
	LastSaveError string `json:"lastSaveError"`
	// Saves and SaveErrors count the writes that ended, and the ones that failed.
	Saves      uint64 `json:"saves"`
	SaveErrors uint64 `json:"saveErrors"`
	// KeptFlags is the number of flags, over all objects, that are served from an earlier version of their object
	// and that a restart from the state directory would not serve (see ObjectInfo.Kept).
	KeptFlags int `json:"keptFlags"`
	// Enabled tells that a state directory is in use.
	Enabled bool `json:"enabled"`
}
