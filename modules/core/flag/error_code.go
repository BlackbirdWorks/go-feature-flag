package flag

// ErrorCode is an enum following the open-feature specs about error code.
type ErrorCode = string

const (
	// ErrorCodeProviderNotReady and the next codes are proposed in the open-feature specs.
	ErrorCodeProviderNotReady ErrorCode = "PROVIDER_NOT_READY"
	// ErrorCodeFlagNotFound is the error code returned when the flag does not exist.
	ErrorCodeFlagNotFound ErrorCode = "FLAG_NOT_FOUND"
	// ErrorCodeParseError is the error code returned when a flag cannot be parsed.
	ErrorCodeParseError ErrorCode = "PARSE_ERROR"
	// ErrorCodeTypeMismatch is the error code returned when a value does not have the expected type.
	ErrorCodeTypeMismatch ErrorCode = "TYPE_MISMATCH"
	// ErrorCodeGeneral is the error code returned for a general error.
	ErrorCodeGeneral ErrorCode = "GENERAL"
	// ErrorCodeInvalidContext is the error code returned when the evaluation context is invalid.
	ErrorCodeInvalidContext ErrorCode = "INVALID_CONTEXT"
	// ErrorCodeTargetingKeyMissing is the error code returned when the targeting key is missing.
	ErrorCodeTargetingKeyMissing ErrorCode = "TARGETING_KEY_MISSING"

	// Custom error code for Semaphore.

	// ErrorFlagConfiguration is returned when we were not able to use the flag because of a misconfiguration.
	ErrorFlagConfiguration ErrorCode = "FLAG_CONFIG"
)
