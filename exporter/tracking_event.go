package exporter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"text/template"
	"time"
)

// TrackingEventDetails holds the details attached to a tracking event.
type TrackingEventDetails = map[string]any

// TrackingEvent represent an Event that we store in the data storage
//
//nolint:lll,govet // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped; fieldalignment: field order is the column order of the CSV and Parquet exports and the key order of the JSON export
type TrackingEvent struct {
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

	// Key of the event.
	Key string `example:"my-feature-flag" json:"key" parquet:"name=key, type=BYTE_ARRAY, convertedtype=UTF8"`

	// EvaluationContext contains the evaluation context used for the tracking
	EvaluationContext map[string]any `json:"evaluationContext" parquet:"name=evaluationContext, type=MAP, keytype=BYTE_ARRAY, keyconvertedtype=UTF8, valuetype=BYTE_ARRAY, valueconvertedtype=UTF8"`

	// TrackingDetails contains the details of the tracking event
	TrackingDetails TrackingEventDetails `json:"trackingEventDetails" parquet:"name=trackingEventDetails, type=MAP, keytype=BYTE_ARRAY, keyconvertedtype=UTF8, valuetype=BYTE_ARRAY, valueconvertedtype=UTF8"`
}

// GetKey returns the key of the tracking event.
func (f TrackingEvent) GetKey() string {
	return f.Key
}

// GetUserKey returns the user key of the event.
func (f TrackingEvent) GetUserKey() string {
	return f.UserKey
}

// GetCreationDate returns the creationDate of the event.
func (f TrackingEvent) GetCreationDate() int64 {
	return f.CreationDate
}

// FormatInCSV returns the tracking event formatted as a CSV line using csvTemplate.
func (f TrackingEvent) FormatInCSV(csvTemplate *template.Template) ([]byte, error) {
	var buf bytes.Buffer

	err := csvTemplate.Execute(&buf, struct {
		TrackingEvent

		FormattedDate string
	}{
		TrackingEvent: f,
		FormattedDate: time.Unix(f.GetCreationDate(), 0).Format(time.RFC3339),
	})
	if err != nil {
		return nil, fmt.Errorf("format tracking event CSV: %w", err)
	}

	return buf.Bytes(), nil
}

// FormatInJSON returns the tracking event formatted as JSON.
func (f TrackingEvent) FormatInJSON() ([]byte, error) {
	b, err := json.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("marshal tracking event JSON: %w", err)
	}

	b = append(b, []byte("\n")...)

	return b, nil
}
