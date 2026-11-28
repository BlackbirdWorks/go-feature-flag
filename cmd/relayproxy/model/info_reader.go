package model

import "time"

// ReaderInfo reports the outcome of the last manifest read by the manifest reader. A manifest that the reader
// rejects never reaches the engine, so it shows here rather than in LastRejected.
type ReaderInfo struct {
	// At is when the reader processed the last manifest; absent before any manifest was read.
	At *time.Time `json:"at,omitempty"`
	// Outcome is the closed outcome of the last read manifest.
	Outcome string `json:"outcome"`
	// Reason uses the same closed rejection-reason set as semaphore_config_reader_rejections_total; empty when accepted.
	Reason string `json:"reason,omitempty"`
}

// UnknownFieldsInfo reports unknown fields noticed when decoding control-plane documents of one kind.
type UnknownFieldsInfo struct {
	// Kind is the document kind ("manifest", "flagSets", "flags", "segments").
	Kind string `json:"kind"`
	// Fields lists the first field paths (bounded, sorted), never field values.
	Fields []string `json:"fields,omitempty"`
	// Count is the total number of unknown fields across documents of this kind.
	Count int `json:"count"`
}
