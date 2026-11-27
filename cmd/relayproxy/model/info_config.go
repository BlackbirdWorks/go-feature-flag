package model

import "time"

// ConfigInfo is what /info says about the config v2 runtime. It never holds an API key, an admin key, a secret or the
// bytes of an object.
type ConfigInfo struct {
	// LastReload is the last round of the engine, applied or not.
	LastReload ReloadInfo `json:"lastReload"`
	// LastRejected is the last rejected candidate, absent when none was.
	LastRejected *ReloadInfo `json:"lastRejected,omitempty"`
	// Staleness says how long ago the held configuration was last confirmed by the control plane.
	Staleness StalenessInfo `json:"staleness"`
	// Reloads counts the rounds by outcome: applied, rejected, ignored.
	Reloads map[string]uint64 `json:"reloads"`
	// FlagSets are the flag sets by name.
	FlagSets []FlagSetInfo `json:"flagSets"`
	// Objects are the source objects by name.
	Objects []ObjectInfo `json:"objects"`
	// Withdrawn lists the flag sets removed by the publisher.
	Withdrawn []WithdrawnInfo `json:"withdrawn"`
	// PendingRestart names the settings that changed and need a restart.
	PendingRestart []string `json:"pendingRestart"`
	// Generation is the applied manifest generation, Snapshot the local swap counter.
	Generation uint64 `json:"generation"`
	Snapshot   uint64 `json:"snapshot"`
}

// ReloadInfo is the result of one round.
//
// The free text of a round (the detail) is not served: it can quote the content of an object. The code is a closed
// reason; the detail is in the logs.
type ReloadInfo struct {
	Outcome    string `json:"outcome"`
	Code       string `json:"code"`
	Generation uint64 `json:"generation"`
}

// StalenessInfo is the age of the configuration.
type StalenessInfo struct {
	// LastConfirmed is the last poll that confirmed the held manifest, absent when there was none.
	LastConfirmed *time.Time `json:"lastConfirmed,omitempty"`
	// LastSuccessfulReload is the last applied round, absent when there was none.
	LastSuccessfulReload *time.Time `json:"lastSuccessfulReload,omitempty"`
	// AgeSeconds is the time since LastConfirmed, 0 when there was none.
	AgeSeconds float64 `json:"ageSeconds"`
}

// WithdrawnInfo is a flag set removed by the publisher.
type WithdrawnInfo struct {
	Name       string `json:"name"`
	Generation uint64 `json:"generation"`
}
