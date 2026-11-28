package model

// SegmentCountsInfo is the number of segments of a flag set: static (members in the document) and dynamic (members in
// a store).
type SegmentCountsInfo struct {
	// DynamicStates tells how many of the dynamic definitions are valid and how many were rejected; it is left out
	// when the flag set has no dynamic segment and no rejection.
	DynamicStates *DynamicStatesInfo `json:"dynamicStates,omitempty"`
	Static        int                `json:"static"`
	Dynamic       int                `json:"dynamic"`
}

// DynamicStatesInfo is the dynamic segments of a flag set by state. Valid ones are served. A rejected definition is one
// that the last content of its segments object offered and the relay refused (a connection that is not in the
// bootstrap file, a store that cannot serve the mode): the object keeps its last good content, and the reasons are in
// the log, never in this answer.
type DynamicStatesInfo struct {
	// Valid is the number of dynamic segments that are served.
	Valid int `json:"valid"`
	// Rejected is the number of dynamic definitions that were rejected.
	Rejected int `json:"rejected"`
}
