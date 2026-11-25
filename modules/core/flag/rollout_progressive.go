package flag

import (
	"time"
)

// ProgressiveRollout represents how to progressively roll out a originalRule.
type ProgressiveRollout struct {
	// Initial contains a description of the initial state of the rollout.
	Initial *ProgressiveRolloutStep `json:"initial,omitempty" jsonschema:"title=initial,description=A description of the initial state of the rollout." toml:"initial,omitempty" yaml:"initial,omitempty"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped

	// End contains what describes the end status of the rollout.
	End *ProgressiveRolloutStep `json:"end,omitempty" jsonschema:"title=initial,description=A description of the end state of the rollout." toml:"end,omitempty" yaml:"end,omitempty"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped
}

// ProgressiveRolloutStep define a progressive rollout step (initial and end).
type ProgressiveRolloutStep struct {
	// Variation - name of the variation for this step
	Variation *string `json:"variation,omitempty" jsonschema:"required,title=variation,description=Name of the variation to apply." toml:"variation,omitempty" yaml:"variation,omitempty"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped

	// Percentage is the percentage (initial or end) for the progressive rollout
	Percentage *float64 `json:"percentage,omitempty" jsonschema:"required,title=percentage,description=The percentage (initial or end) for the progressive rollout." toml:"percentage,omitempty" yaml:"percentage,omitempty"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped

	// Date is the time it starts or ends.
	Date *time.Time `json:"date,omitempty" jsonschema:"required,title=date,description=Date is the time it starts or ends." toml:"date,omitempty" yaml:"date,omitempty"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped
}

func (p *ProgressiveRolloutStep) getVariation() string {
	if p == nil || p.Variation == nil {
		return ""
	}

	return *p.Variation
}

func (p *ProgressiveRolloutStep) getPercentage() float64 {
	if p == nil || p.Percentage == nil {
		return 0
	}

	return *p.Percentage
}

// mergeStep is a function that take a ProgressiveRolloutStep and update the current instance
// with all the fields that are here to be overridden.
func (p *ProgressiveRolloutStep) mergeStep(updatedStep *ProgressiveRolloutStep) {
	if updatedStep.Variation != nil {
		p.Variation = updatedStep.Variation
	}

	if updatedStep.Date != nil {
		p.Date = updatedStep.Date
	}

	if updatedStep.Percentage != nil {
		p.Percentage = updatedStep.Percentage
	}
}
