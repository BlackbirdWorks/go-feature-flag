package dto

import (
	"time"
)

type ExperimentationDto struct {
	// Start is the starting time of the experimentation
	Start *time.Time `json:"start,omitempty" yaml:"start,omitempty" toml:"start,omitempty" jsonschema:"required,title=start,description=Time of the start of the experimentation."` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped

	// End is the ending time of the experimentation
	End *time.Time `json:"end,omitempty" yaml:"end,omitempty" toml:"end,omitempty" jsonschema:"required,title=start,description=Time of the end of the experimentation."` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped
}
