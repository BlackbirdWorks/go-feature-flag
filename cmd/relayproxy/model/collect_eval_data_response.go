package model

// CollectEvalDataResponse is the object returned by the health API.
type CollectEvalDataResponse struct {
	// IngestedContentCount number of model.FeatureEvents that have been sent to the data exporter
	IngestedContentCount int `form:"ingestedContentCount" json:"ingestedContentCount" query:"ingestedContentCount" xml:"ingestedContentCount"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped
}
