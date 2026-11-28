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

// DynamicStatesInfo is the dynamic segments of a flag set by state. A rejected definition is one that the connections
// of the bootstrap file cannot serve (a connection that is not in the file, a store that cannot serve the mode): it is
// rejected alone, the rest of its document is applied, and it answers as a segment without a provider. The reasons are
// in the log, never in this answer.
type DynamicStatesInfo struct {
	// Valid is the number of dynamic segments that the connections can serve.
	Valid int `json:"valid"`
	// Rejected is the number of dynamic definitions that were rejected.
	Rejected int `json:"rejected"`
}
