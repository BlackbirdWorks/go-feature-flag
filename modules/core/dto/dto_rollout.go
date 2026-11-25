package dto

import (
	"time"
)

// ExperimentationDto is the DTO of an experimentation rollout.
type ExperimentationDto struct {
	// Start is the starting time of the experimentation
	Start *time.Time `json:"start,omitempty" jsonschema:"required,title=start,description=Time of the start of the experimentation." toml:"start,omitempty" yaml:"start,omitempty"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped

	// End is the ending time of the experimentation
	End *time.Time `json:"end,omitempty" jsonschema:"required,title=start,description=Time of the end of the experimentation." toml:"end,omitempty" yaml:"end,omitempty"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped
}
