package flagdef

// Rollout groups the time-based rollout settings of a flag: an experimentation window, scheduled updates
// and a progressive rollout.
type Rollout struct {
	// Experimentation is your struct to configure an experimentation, it will allow you to configure a start date and
	// an end date for your flag.
	// When the experimentation is not running, the flag will serve the default value.
	Experimentation *ExperimentationRollout `json:"experimentation,omitempty" toml:"experimentation,omitempty" yaml:"experimentation,omitempty"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped

	// Scheduled is your struct to configure an update on some fields of your flag over time.
	// You can add several steps that update the flag, this is typically used if you want to gradually add more user
	// in your flag.
	Scheduled *[]ScheduledStep `json:"scheduled,omitempty" toml:"scheduled,omitempty" yaml:"scheduled,omitempty"`
}
