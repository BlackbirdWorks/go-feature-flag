package flag

import (
	"time"
)

type ExperimentationRollout struct {
	// Start is the starting time of the experimentation
	Start *time.Time `json:"start,omitempty" toml:"start,omitempty" yaml:"start,omitempty"`

	// End is the ending time of the experimentation
	End *time.Time `json:"end,omitempty" toml:"end,omitempty" yaml:"end,omitempty"`
}
