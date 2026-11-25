package ffcontext

import (
	"encoding/json"
	"fmt"
	"maps"
)

var _ Context = (*EvaluationContext)(nil)

const anonymousAttribute = "anonymous"

// Context is the evaluation context interface used to evaluate the flags.
type Context interface {
	// GetKey return the unique targetingKey for the context.
	GetKey() string
	// IsAnonymous return if the context is about an anonymous user or not.
	IsAnonymous() bool
	// GetCustom return all the attributes properties added to the context.
	GetCustom() map[string]any
	// AddCustomAttribute allows to add a attributes attribute into the context.
	AddCustomAttribute(name string, value any)
	// ExtractGOFFProtectedFields extract the goff specific attributes from the evaluation context.
	ExtractGOFFProtectedFields() GoffContextSpecifics
}

// value is a type to define attributes.
type value map[string]any

// EvaluationContext contains specific attributes for your evaluation.
// Most of the time it is identifying a user browsing your site.
// The only mandatory property is the Key, which must a unique identifier.
// For authenticated users, this may be a username or e-mail address.
// For anonymous users, this could be an IP address or session ID.
//
// EvaluationContext fields are immutable and can be accessed only via getter methods.
// To construct an EvaluationContext, use either a simple constructor (NewEvaluationContext) or the builder pattern
// with NewEvaluationContextBuilder.
type EvaluationContext struct {
	attributes value
	// uniquely identifying the subject (end-user, or client service) of a flag evaluation
	targetingKey string
}

// NewEvaluationContext creates a new evaluation context identified by the given targetingKey.
func NewEvaluationContext(key string) EvaluationContext {
	return EvaluationContext{targetingKey: key, attributes: map[string]any{}}
}

// NewAnonymousEvaluationContext returns an anonymous evaluation context with the given key.
//
// Deprecated: NewAnonymousEvaluationContext is here for compatibility reason.
// Please use NewEvaluationContext instead and add a attributes attribute to know that it is an anonymous user.
//
// ctx := NewEvaluationContext("my-targetingKey")
// ctx.AddCustomAttribute("anonymous", true).
func NewAnonymousEvaluationContext(key string) EvaluationContext {
	return EvaluationContext{targetingKey: key, attributes: map[string]any{
		anonymousAttribute: true,
	}}
}

// MarshalJSON is a custom JSON marshaller for EvaluationContext.
// It will only marshal the targetingKey and the attributes of the context and avoid to expose the internal structure.
func (u EvaluationContext) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(&struct {
		Attributes   value  `json:"attributes"`
		TargetingKey string `json:"targetingKey"`
	}{
		TargetingKey: u.targetingKey,
		Attributes:   u.attributes,
	})
	if err != nil {
		return nil, fmt.Errorf("encoding the evaluation context: %w", err)
	}

	return data, nil
}

// GetKey return the unique targetingKey for the user.
func (u EvaluationContext) GetKey() string {
	return u.targetingKey
}

// IsAnonymous return if the user is anonymous or not.
func (u EvaluationContext) IsAnonymous() bool {
	anonymous := u.attributes[anonymousAttribute]
	switch v := anonymous.(type) {
	case bool:
		return v
	default:
		return false
	}
}

// GetCustom return all the attributes properties of a user.
func (u EvaluationContext) GetCustom() map[string]any {
	return u.attributes
}

// AddCustomAttribute allows to add a attributes attribute into the user.
func (u EvaluationContext) AddCustomAttribute(name string, value any) {
	if name != "" {
		u.attributes[name] = value
	}
}

// ToMap returns a new map containing the context attributes plus the targetingKey.
// The returned map is a shallow copy: the top-level map is independent from the context,
// so adding or removing keys does not affect it, but nested reference values (maps, slices,
// pointers) are shared and mutating them will also mutate the context.
func (u EvaluationContext) ToMap() map[string]any {
	resMap := make(map[string]any, len(u.attributes)+1)
	maps.Copy(resMap, u.attributes)
	resMap["targetingKey"] = u.targetingKey

	return resMap
}

// ReservedKey is the evaluation context attribute holding the Semaphore specific fields
// (currentDateTime, flagList, exporterMetadata).
const ReservedKey = "semaphore"

// LegacyReservedKey is the name that GO Feature Flag clients send for ReservedKey. It is still
// accepted, ReservedKey wins when both are set.
const LegacyReservedKey = "gofeatureflag"

// HasReservedKey tells if the attributes carry the reserved attribute, under any of its names.
func HasReservedKey(attributes map[string]any) bool {
	_, current := attributes[ReservedKey]
	_, legacyName := attributes[LegacyReservedKey]

	return current || legacyName
}

// ExtractGOFFProtectedFields extract the Semaphore specific attributes from the evaluation context.
// They are read from the "semaphore" attribute and, for the clients of Semaphore, from the
// legacy "gofeatureflag" attribute. When both are set, a field of "semaphore" wins.
func (u EvaluationContext) ExtractGOFFProtectedFields() GoffContextSpecifics {
	specifics := GoffContextSpecifics{}
	if v, ok := u.attributes[LegacyReservedKey]; ok {
		specifics = readSpecifics(v, specifics)
		specifics.UsedLegacyKey = true
	}

	if v, ok := u.attributes[ReservedKey]; ok {
		specifics = readSpecifics(v, specifics)
	}

	return specifics
}

// readSpecifics returns base with the fields found in v on top of it.
func readSpecifics(v any, base GoffContextSpecifics) GoffContextSpecifics {
	found := GoffContextSpecifics{}

	switch typed := v.(type) {
	case map[string]string:
		found.addCurrentDateTime(typed["currentDateTime"])
		found.addListFlags(typed["flagList"])
		found.addExporterMetadata(typed["exporterMetadata"])
	case map[string]any:
		found.addCurrentDateTime(typed["currentDateTime"])
		found.addListFlags(typed["flagList"])
		found.addExporterMetadata(typed["exporterMetadata"])
	case GoffContextSpecifics:
		found = typed
	}

	if found.CurrentDateTime != nil {
		base.CurrentDateTime = found.CurrentDateTime
	}

	if found.FlagList != nil {
		base.FlagList = found.FlagList
	}

	if found.ExporterMetadata != nil {
		base.ExporterMetadata = found.ExporterMetadata
	}

	return base
}
