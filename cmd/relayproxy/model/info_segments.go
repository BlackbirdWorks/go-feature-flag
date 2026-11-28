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
	// StoreError is the number of served segments, among Valid, whose store failed on the last call or whose breaker
	// is open: they answer not a member with STORE_ERROR. Healthy is the number of the others.
	StoreError int `json:"storeError"`
	// Healthy is the number of served segments, among Valid, whose store answers.
	Healthy int `json:"healthy"`
}

// SegmentStoreInfo is the state of one connection that can serve dynamic segments: idle (no segment uses it),
// connecting, connected or error (the last dial failed; it is dialled again until it works).
type SegmentStoreInfo struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	State string `json:"state"`
}
