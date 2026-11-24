package ffcontext

import (
	"encoding/json"
	"maps"

	"github.com/blackbirdworks/semaphore/modules/core/legacy"
)

var _ Context = (*EvaluationContext)(nil)

const anonymousAttribute = "anonymous"

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

// NewEvaluationContext creates a new evaluation context identified by the given targetingKey.
func NewEvaluationContext(key string) EvaluationContext {
	return EvaluationContext{targetingKey: key, attributes: map[string]any{}}
}

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
	// uniquely identifying the subject (end-user, or client service) of a flag evaluation
	targetingKey string
	attributes   value
}

// MarshalJSON is a custom JSON marshaller for EvaluationContext.
// It will only marshal the targetingKey and the attributes of the context and avoid to expose the internal structure.
func (u EvaluationContext) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		TargetingKey string `json:"targetingKey"`
		Attributes   value  `json:"attributes"`
	}{
		TargetingKey: u.targetingKey,
		Attributes:   u.attributes,
	})
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
		legacy.Default.Warn(legacy.KindEvaluationContext, "the evaluation context attribute \""+
			LegacyReservedKey+"\" is a legacy name, it is still accepted but \""+ReservedKey+"\" is preferred")
		specifics = readSpecifics(v, specifics)
	}
	if v, ok := u.attributes[ReservedKey]; ok {
		specifics = readSpecifics(v, specifics)
	}
	return specifics
}

// readSpecifics returns base with the fields found in v on top of it.
func readSpecifics(v any, base GoffContextSpecifics) GoffContextSpecifics {
	found := GoffContextSpecifics{}
	switch v := v.(type) {
	case map[string]string:
		found.addCurrentDateTime(v["currentDateTime"])
		found.addListFlags(v["flagList"])
		found.addExporterMetadata(v["exporterMetadata"])
	case map[string]any:
		found.addCurrentDateTime(v["currentDateTime"])
		found.addListFlags(v["flagList"])
		found.addExporterMetadata(v["exporterMetadata"])
	case GoffContextSpecifics:
		found = v
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
