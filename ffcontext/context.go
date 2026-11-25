package ffcontext

// This file is a wrapper around the core context package to avoid any breaking changes
// when moving the logic to the new core package.

import corectx "github.com/blackbirdworks/semaphore/modules/core/ffcontext"

// Context is the evaluation context of a flag evaluation.
type Context = corectx.Context

// EvaluationContext is the evaluation context with the attributes used by the rules.
type EvaluationContext = corectx.EvaluationContext

// EvaluationContextBuilder builds an EvaluationContext.
type EvaluationContextBuilder = corectx.EvaluationContextBuilder

// GoffContextSpecifics holds the context fields specific to GO Feature Flag.
type GoffContextSpecifics = corectx.GoffContextSpecifics

// NewEvaluationContext creates a new evaluation context identified by the given targetingKey.
func NewEvaluationContext(key string) EvaluationContext {
	return corectx.NewEvaluationContext(key)
}

// NewAnonymousEvaluationContext returns an anonymous evaluation context with the given key.
//
// Deprecated: NewAnonymousEvaluationContext is here for compatibility reason.
// Please use NewEvaluationContext instead and add a attributes attribute to know that it is an anonymous user.
//
// ctx := NewEvaluationContext("my-targetingKey")
// ctx.AddCustomAttribute("anonymous", true).
func NewAnonymousEvaluationContext(key string) EvaluationContext {
	return corectx.NewAnonymousEvaluationContext(key)
}

// NewEvaluationContextBuilder constructs a new EvaluationContextBuilder, specifying the user targetingKey.
//
// For authenticated users, the targetingKey may be a username or e-mail address. For anonymous users,
// this could be an IP address or session ID.
func NewEvaluationContextBuilder(key string) *EvaluationContextBuilder {
	return corectx.NewEvaluationContextBuilder(key)
}

// ReservedKey is the evaluation context attribute holding the Semaphore specific fields.
// LegacyReservedKey is the name used by GO Feature Flag, still accepted.
const (
	ReservedKey       = corectx.ReservedKey
	LegacyReservedKey = corectx.LegacyReservedKey
)

// HasReservedKey tells if the attributes carry the reserved attribute, under any of its names.
func HasReservedKey(attributes map[string]any) bool {
	return corectx.HasReservedKey(attributes)
}
