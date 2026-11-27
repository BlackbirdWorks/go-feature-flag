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
}
