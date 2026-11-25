package dto

import (
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

// DTO is representing all the fields we can have in a flag.
// This DTO supports all flag formats and convert them into an InternalFlag using a converter.
type DTO struct {
	// TrackEvents is false if you don't want to export the data in your data exporter.
	// Default value is true
	TrackEvents *bool `json:"trackEvents,omitempty" toml:"trackEvents,omitempty" yaml:"trackEvents,omitempty"`

	// Disable is true if the flag is disabled.
	Disable *bool `json:"disable,omitempty" toml:"disable,omitempty" yaml:"disable,omitempty"`

	// Version (optional) This field contains the version of the flag.
	// The version is manually managed when you configure your flags and, it is used to display the information
	// in the notifications and data collection.
	Version *string `json:"version,omitempty" toml:"version,omitempty" yaml:"version,omitempty"`

	// Converter (optional) is the name of converter to use, if no converter specified we try to determine
	// which converter to use based on the fields we receive for the flag
	Converter *string `json:"converter,omitempty" toml:"converter,omitempty" yaml:"converter,omitempty"`

	// Variations are all the variations available for this flag. The minimum is 2 variations and, we don't have any max
	// limit except if the variationValue is a bool, the max is 2.
	Variations *map[string]*any `json:"variations,omitempty" jsonschema:"required,title=variations,description=All the variations available for this flag. You need at least 2 variations and it is a key value pair. All the variations should have the same type." toml:"variations,omitempty" yaml:"variations,omitempty"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped

	// Rules is the list of Rule for this flag.
	// This an optional field.
	Rules *[]flag.Rule `json:"targeting,omitempty" jsonschema:"title=targeting,description=List of rule to target a subset of the users based on the evaluation context." toml:"targeting,omitempty" yaml:"targeting,omitempty"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped

	// BucketingKey defines a source for a dynamic targeting key
	BucketingKey *string `json:"bucketingKey,omitempty" toml:"bucketingKey,omitempty" yaml:"bucketingKey,omitempty"`

	// DefaultRule is the rule applied after checking that any other rules
	// matched the user.
	DefaultRule *flag.Rule `json:"defaultRule,omitempty" jsonschema:"required,title=defaultRule,description=How do we evaluate the flag if the user is not part of any of the targeting rule." toml:"defaultRule,omitempty" yaml:"defaultRule,omitempty"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped

	// Scheduled is your struct to configure an update on some fields of your flag over time.
	// You can add several steps that updates the flag, this is typically used if you want to gradually add more user
	// in your flag.
	Scheduled *[]flag.ScheduledStep `json:"scheduledRollout,omitempty" jsonschema:"title=scheduledRollout,description=Configure an update on some fields of your flag over time." toml:"scheduledRollout,omitempty" yaml:"scheduledRollout,omitempty"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped

	// Experimentation is your struct to configure an experimentation.
	// It will allow you to configure a start date and an end date for your flag.
	// When the experimentation is not running, the flag will serve the default value.
	Experimentation *ExperimentationDto `json:"experimentation,omitempty" jsonschema:"title=experimentation,description=Configure an experimentation. It will allow you to configure a start date and an end date for your flag." toml:"experimentation,omitempty" yaml:"experimentation,omitempty"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped

	// Metadata is a field containing information about your flag such as an issue tracker link, a description, etc ...
	Metadata *map[string]any `json:"metadata,omitempty" jsonschema:"title=metadata,description=A field containing information about your flag such as an issue tracker link a description etc..." toml:"metadata,omitempty" yaml:"metadata,omitempty"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped
}

// Convert is converting the DTO into a flag.InternalFlag.
func (d *DTO) Convert() flag.InternalFlag {
	var empty DTO
	if d == nil || empty == *d {
		return flag.InternalFlag{}
	}

	return ConvertDtoToInternalFlag(*d)
}
