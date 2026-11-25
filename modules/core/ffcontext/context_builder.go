package ffcontext

// EvaluationContextBuilder is a builder to create an EvaluationContext.
type EvaluationContextBuilder struct {
	custom value
	// Key is the only mandatory attribute
	key string
}

// NewEvaluationContextBuilder constructs a new EvaluationContextBuilder, specifying the user targetingKey.
//
// For authenticated users, the targetingKey may be a username or e-mail address. For anonymous users,
// this could be an IP address or session ID.
func NewEvaluationContextBuilder(key string) *EvaluationContextBuilder {
	return &EvaluationContextBuilder{
		key:    key,
		custom: map[string]any{},
	}
}

// Deprecated: Anonymous is to flag the context for an anonymous context or not.
// This function is here for compatibility reason, please consider using AddCustom("anonymous", true)
// instead of using this function.
func (u *EvaluationContextBuilder) Anonymous(anonymous bool) *EvaluationContextBuilder {
	u.custom[anonymousAttribute] = anonymous

	return u
}

// AddCustom allows you to add an attributes attribute to the EvaluationContext.
func (u *EvaluationContextBuilder) AddCustom(
	key string,
	value any,
) *EvaluationContextBuilder {
	u.custom[key] = value

	return u
}

// Build is creating the EvaluationContext.
func (u *EvaluationContextBuilder) Build() EvaluationContext {
	return EvaluationContext{
		targetingKey: u.key,
		attributes:   u.custom,
	}
}
