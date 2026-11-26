package flagdef

// ResolutionDetails is the object used to manipulate the data internally, it allows to retrieve
// the details of your evaluation.
type ResolutionDetails struct {
	// RuleIndex indicates which rules applied
	RuleIndex *int
	// RuleName (optional) is the name of the associated rule if we have one
	RuleName *string
	// Metadata is a field containing information about your flag such as an issue tracker link, a description, etc ...
	// It is never the map of the flag: it belongs to the caller, who can add keys to it (the cacheable keys of the
	// OFREP responses are added so) without changing the flag or racing with the other evaluations.
	Metadata map[string]any
	// Variant indicates the name of the variant used when evaluating the flag
	Variant string
	// Reason indicates the reason of the decision
	Reason ResolutionReason
	// ErrorCode indicates the error code for this evaluation
	ErrorCode ErrorCode
	// ErrorMessage gives more information about the error
	ErrorMessage string
	// Cacheable is set to true if an SDK/provider can cache the value locally.
	Cacheable bool
}
