package model

type AllFlagRequest struct {
	// LegacyUser captures the removed `user` request field so that the API can reject it
	// with an explicit error instead of silently ignoring it. It is never documented.
	LegacyUser any `json:"user,omitempty" swaggerignore:"true"`
	// EvaluationContext The representation of a EvaluationContext for your feature flag system.
	EvaluationContext *EvaluationContextRequest `json:"evaluationContext,omitempty" xml:"evaluationContext,omitempty" form:"evaluationContext" query:"evaluationContext"` // nolint: lll
}

type EvalFlagRequest struct {
	AllFlagRequest `json:",inline"`
	// The value will we use if we are not able to get the variation of the flag.
	DefaultValue any `json:"defaultValue" xml:"defaultValue" form:"defaultValue" query:"defaultValue"`
}

// EvaluationContextRequest The representation of a EvaluationContext for your feature flag system.
type EvaluationContextRequest struct {
	// Key is the identifier of the evaluation context.
	Key string `json:"key" xml:"key" form:"key" query:"key" example:"08b5ffb7-7109-42f4-a6f2-b85560fbd20f"`

	// Custom is a map containing all extra information for this user.
	Custom map[string]any `json:"custom" xml:"custom" form:"custom" query:"custom" swaggertype:"object,string" example:"email:contact@gofeatureflag.org,firstname:John,lastname:Doe,company:GO Feature Flag"` // nolint: lll
}
