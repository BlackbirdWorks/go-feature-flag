package controlplane

import "time"

// Flags is a flag file: flag name to flag. It has no header.
type Flags map[string]Flag

// Flag is one flag of a flag file.
type Flag struct { //nolint:govet // fieldalignment: the field order is the wire order
	TrackEvents      *bool            `json:"trackEvents,omitempty"      yaml:"trackEvents,omitempty"`
	Disable          *bool            `json:"disable,omitempty"          yaml:"disable,omitempty"`
	Version          *string          `json:"version,omitempty"          yaml:"version,omitempty"`
	Converter        *string          `json:"converter,omitempty"        yaml:"converter,omitempty"`
	Variations       map[string]any   `json:"variations,omitempty"       yaml:"variations,omitempty"`
	Targeting        []Rule           `json:"targeting,omitempty"        yaml:"targeting,omitempty"`
	BucketingKey     *string          `json:"bucketingKey,omitempty"     yaml:"bucketingKey,omitempty"`
	DefaultRule      *Rule            `json:"defaultRule,omitempty"      yaml:"defaultRule,omitempty"`
	ScheduledRollout []ScheduledStep  `json:"scheduledRollout,omitempty" yaml:"scheduledRollout,omitempty"`
	Experimentation  *Experimentation `json:"experimentation,omitempty"  yaml:"experimentation,omitempty"`
	Metadata         map[string]any   `json:"metadata,omitempty"         yaml:"metadata,omitempty"`
}

// ScheduledStep updates fields of a flag at a date. The flag fields are the optional fields of Flag that the
// engine allows in a step (a nested scheduledRollout is not part of the format).
type ScheduledStep struct { //nolint:govet // fieldalignment: the field order is the wire order
	Date            *time.Time       `json:"date,omitempty"            yaml:"date,omitempty"`
	TrackEvents     *bool            `json:"trackEvents,omitempty"     yaml:"trackEvents,omitempty"`
	Disable         *bool            `json:"disable,omitempty"         yaml:"disable,omitempty"`
	Version         *string          `json:"version,omitempty"         yaml:"version,omitempty"`
	Variations      map[string]any   `json:"variations,omitempty"      yaml:"variations,omitempty"`
	Targeting       []Rule           `json:"targeting,omitempty"       yaml:"targeting,omitempty"`
	BucketingKey    *string          `json:"bucketingKey,omitempty"    yaml:"bucketingKey,omitempty"`
	DefaultRule     *Rule            `json:"defaultRule,omitempty"     yaml:"defaultRule,omitempty"`
	Experimentation *Experimentation `json:"experimentation,omitempty" yaml:"experimentation,omitempty"`
	Metadata        map[string]any   `json:"metadata,omitempty"        yaml:"metadata,omitempty"`
}

// Experimentation limits a flag to a time window.
type Experimentation struct {
	Start *time.Time `json:"start,omitempty" yaml:"start,omitempty"`
	End   *time.Time `json:"end,omitempty"   yaml:"end,omitempty"`
}
