package controlplane

import "time"

// Rule is a targeting rule or the default rule.
type Rule struct { //nolint:govet // fieldalignment: the field order is the wire order
	Name               string              `json:"name,omitempty"               yaml:"name,omitempty"`
	Query              string              `json:"query,omitempty"              yaml:"query,omitempty"`
	Variation          string              `json:"variation,omitempty"          yaml:"variation,omitempty"`
	Percentage         map[string]float64  `json:"percentage,omitempty"         yaml:"percentage,omitempty"`
	ProgressiveRollout *ProgressiveRollout `json:"progressiveRollout,omitempty" yaml:"progressiveRollout,omitempty"`
	Disable            *bool               `json:"disable,omitempty"            yaml:"disable,omitempty"`
}

// ProgressiveRollout ramps a rule from an initial to an end step.
type ProgressiveRollout struct {
	Initial *ProgressiveRolloutStep `json:"initial,omitempty" yaml:"initial,omitempty"`
	End     *ProgressiveRolloutStep `json:"end,omitempty"     yaml:"end,omitempty"`
}

// ProgressiveRolloutStep is one end of a progressive rollout.
type ProgressiveRolloutStep struct { //nolint:govet // fieldalignment: the field order is the wire order
	Variation  string     `json:"variation,omitempty"  yaml:"variation,omitempty"`
	Percentage *float64   `json:"percentage,omitempty" yaml:"percentage,omitempty"`
	Date       *time.Time `json:"date,omitempty"       yaml:"date,omitempty"`
}
