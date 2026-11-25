package model

// AllFlagRequest is the request body of the all flags evaluation API.
type AllFlagRequest struct {
	// LegacyUser captures the removed `user` request field so that the API can reject it
	// with an explicit error instead of silently ignoring it. It is never documented.
	LegacyUser any `hidden:"true" json:"user,omitempty"`
	// EvaluationContext The representation of a EvaluationContext for your feature flag system.
	EvaluationContext *EvaluationContextRequest `form:"evaluationContext" json:"evaluationContext,omitempty" query:"evaluationContext" xml:"evaluationContext,omitempty"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped
}

// EvalFlagRequest is the request body of a single flag evaluation API.
type EvalFlagRequest struct {
	AllFlagRequest `json:",inline"`

	// The value will we use if we are not able to get the variation of the flag.
	DefaultValue any `form:"defaultValue" json:"defaultValue" query:"defaultValue" required:"false" xml:"defaultValue"`
}

// EvaluationContextRequest The representation of a EvaluationContext for your feature flag system.
type EvaluationContextRequest struct {
	// Custom is a map containing all extra information for this user.
	Custom map[string]any `example:"{\"email\":\"contact@gofeatureflag.org\",\"firstname\":\"John\",\"lastname\":\"Doe\",\"company\":\"Semaphore\"}" form:"custom" json:"custom" query:"custom" required:"false" xml:"custom"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped
	// Key is the identifier of the evaluation context.
	Key string `example:"08b5ffb7-7109-42f4-a6f2-b85560fbd20f" form:"key" json:"key" query:"key" required:"false" xml:"key"` //nolint:lll // struct tags can not be wrapped
}
