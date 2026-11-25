package flagdef

import (
	"time"
)

// ScheduledStep is one change of the flag.
type ScheduledStep struct {
	InternalFlag `yaml:",inline"`

	Date *time.Time `json:"date,omitempty" toml:"date,omitempty" yaml:"date,omitempty"`
}
