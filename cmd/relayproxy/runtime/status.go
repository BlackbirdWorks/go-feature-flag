package runtime //nolint:revive,nolintlint // the name is fixed: the package is cmd/relayproxy/runtime

import "github.com/blackbirdworks/semaphore/controlplane"

// Status is the immutable status tree of one [Runtime]. It never contains an API key or an admin key.
type Status struct {
	// FlagSets are the flag sets by name, including those that have no runtime entry (an invalid entry with
	// nothing to fall back on is reported with state pending).
	FlagSets map[string]*FlagSetStatus `json:"flagSets"`
	// Objects are the source objects (flags and flag-set documents) by name.
	Objects map[string]*ObjectStatus `json:"objects"`
	// LastRejected is the last candidate that was rejected, kept until a later rejection replaces it.
	LastRejected *Reload `json:"lastRejected,omitempty"`
	// Entries are the problems of the entries of the flag-set documents, in document order.
	Entries []EntryProblem `json:"entries"`
	// PendingRestart names the bootstrap settings that changed but need a restart; the values are never listed.
	PendingRestart []string `json:"pendingRestart"`
	// LastReload is the round that produced this runtime.
	LastReload Reload `json:"lastReload"`
	// Generation is the manifest generation.
	Generation uint64 `json:"generation"`
	// Snapshot is the snapshot number.
	Snapshot uint64 `json:"snapshot"`
}

// FlagSetStatus is the status of one flag set.
type FlagSetStatus struct {
	// Name is the name of the flag set.
	Name string `json:"name"`
	// State is the state of the flag set.
	State SetState `json:"state"`
	// Sources are the names of its source objects, in order.
	Sources []string `json:"sources"`
	// Problems are the reported problems of this flag set.
	Problems []Problem `json:"problems"`
	// Flags is the number of flags served.
	Flags int `json:"flags"`
	// APIKeys is the number of API keys of the flag set.
	APIKeys int `json:"apiKeys"`
	// Rejected is the number of rejected flags in its sources.
	Rejected int `json:"rejected"`
}

// Problem is one reported problem of a flag set. Subject is a source name or empty.
type Problem struct {
	Code    controlplane.Code `json:"code"`
	Subject string            `json:"subject,omitempty"`
	Reason  string            `json:"reason"`
}

// EntryProblem is a problem of one entry of a flag-set document.
type EntryProblem struct {
	// Object is the name of the document object.
	Object string `json:"object"`
	// Name is the flag set name (may be empty or invalid).
	Name string `json:"name"`
	// Reason says what is wrong; it never contains an API key.
	Reason string `json:"reason"`
	// Code is the stable reason.
	Code controlplane.Code `json:"code"`
	// Index is the position in the document.
	Index int `json:"index"`
}
