package exporter

import (
	"bytes"
	"encoding/json"
	"errors"
	"text/template"
	"time"

	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

var (
	errNoValueToConvert = errors.New("no value to convert, returning empty string")
)

type FeatureEventMetadata = map[string]any

// FeatureEvent represent an Event that we store in the data storage
//
//nolint:lll,govet // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped; fieldalignment: field order is the column order of the CSV and Parquet exports and the key order of the JSON export
type FeatureEvent struct {
	// Kind for a feature event is feature.
	// A feature event will only be generated if the trackEvents attribute of the flag is set to true.
	Kind string `example:"feature" json:"kind" parquet:"name=kind, type=BYTE_ARRAY, convertedtype=UTF8"`

	// ContextKind is the kind of context which generated an event. This will only be "anonymousUser" for events generated
	// on behalf of an anonymous user or the reserved word "user" for events generated on behalf of a non-anonymous user
	ContextKind string `example:"user" json:"contextKind,omitempty" parquet:"name=contextKind, type=BYTE_ARRAY, convertedtype=UTF8"`

	// UserKey The key of the user object used in a feature flag evaluation. Details for the user object used in a feature
	// flag evaluation as reported by the "feature" event are transmitted periodically with a separate index event.
	UserKey string `example:"94a25909-20d8-40cc-8500-fee99b569345" json:"userKey" parquet:"name=userKey, type=BYTE_ARRAY, convertedtype=UTF8"`

	// CreationDate When the feature flag was requested at Unix epoch time in milliseconds.
	CreationDate int64 `example:"1680246000011" json:"creationDate" parquet:"name=creationDate, type=INT64"`

	// Key of the feature flag requested.
	Key string `example:"my-feature-flag" json:"key" parquet:"name=key, type=BYTE_ARRAY, convertedtype=UTF8"`

	// Variation  of the flag requested. Flag variation values can be "True", "False", "Default" or "SdkDefault"
	// depending on which value was taken during flag evaluation. "SdkDefault" is used when an error is detected and the
	// default value passed during the call to your variation is used.
	Variation string `example:"admin-variation" json:"variation" parquet:"name=variation, type=BYTE_ARRAY, convertedtype=UTF8"`

	// Value of the feature flag returned by feature flag evaluation.
	Value any `json:"value" parquet:"name=value, type=BYTE_ARRAY, convertedtype=UTF8"`

	// Default value is set to true if feature flag evaluation failed, in which case the value returned was the default
	// value passed to variation. If the default field is omitted, it is assumed to be false.
	Default bool `example:"false" json:"default" parquet:"name=default, type=BOOLEAN"`

	// Version contains the version of the flag. If the field is omitted for the flag in the configuration file
	// the default version will be 0.
	Version string `example:"v1.0.0" json:"version" parquet:"name=version, type=BYTE_ARRAY, convertedtype=UTF8"`

	// Source indicates where the event was generated.
	// This is set to SERVER when the event was evaluated in the relay-proxy and PROVIDER_CACHE when it is evaluated from the cache.
	Source string `example:"SERVER" json:"source" parquet:"name=source, type=BYTE_ARRAY, convertedtype=UTF8"`

	// Metadata are static information added in the providers to give context about the events generated.
	Metadata FeatureEventMetadata `json:"metadata,omitempty" parquet:"name=metadata, type=MAP, keytype=BYTE_ARRAY, keyconvertedtype=UTF8, valuetype=BYTE_ARRAY, valueconvertedtype=UTF8"`
}

func NewFeatureEvent(
	ctx ffcontext.Context,
	flagKey string,
	value any,
	variation string,
	failed bool,
	version, source string,
	metadata FeatureEventMetadata,
) FeatureEvent {
	contextKind := "user"
	if ctx.IsAnonymous() {
		contextKind = "anonymousUser"
	}

	return FeatureEvent{
		Kind:         "feature",
		ContextKind:  contextKind,
		UserKey:      ctx.GetKey(),
		CreationDate: clock.Now().Unix(),
		Key:          flagKey,
		Variation:    variation,
		Value:        value,
		Default:      failed,
		Version:      version,
		Source:       source,
		Metadata:     metadata,
	}
}

// GetKey returns the key of the event.
func (f FeatureEvent) GetKey() string {
	return f.Key
}

// GetUserKey returns the user key of the event.
func (f FeatureEvent) GetUserKey() string {
	return f.UserKey
}

// GetCreationDate returns the creationDate of the event.
func (f FeatureEvent) GetCreationDate() int64 {
	return f.CreationDate
}

func (f FeatureEvent) FormatInCSV(csvTemplate *template.Template) ([]byte, error) {
	var buf bytes.Buffer
	err := csvTemplate.Execute(&buf, struct {
		FeatureEvent

		FormattedDate string
	}{
		FeatureEvent:  f,
		FormattedDate: time.Unix(f.GetCreationDate(), 0).Format(time.RFC3339),
	})
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (f FeatureEvent) FormatInJSON() ([]byte, error) {
	b, err := json.Marshal(f)
	b = append(b, []byte("\n")...)

	return b, err
}

// ConvertValueForParquet converts the value of the event to a string to be stored in a parquet file.
func (f FeatureEvent) ConvertValueForParquet() (string, error) {
	if f.Value == nil {
		return "", errNoValueToConvert
	}
	b, err := json.Marshal(f.Value)
	if err != nil {
		return "", err
	}

	return string(b), nil
}
