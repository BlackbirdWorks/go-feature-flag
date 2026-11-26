package connection

import "log/slog"

// RedactedValue is what a [Secret] prints instead of its content.
const RedactedValue = "[REDACTED]"

// Secret is secret material, already resolved to its value. Whatever the verb, the encoder or the log
// handler, it prints [RedactedValue]. Use [Secret.Reveal] where the value is really needed.
type Secret string

// Reveal returns the secret value.
func (s Secret) Reveal() string { return string(s) }

// String implements [fmt.Stringer], so %s, %v and %+v never leak the value.
func (Secret) String() string { return RedactedValue }

// GoString implements [fmt.GoStringer], so %#v never leaks the value.
func (Secret) GoString() string { return RedactedValue }

// LogValue implements [slog.LogValuer].
func (Secret) LogValue() slog.Value { return slog.StringValue(RedactedValue) }

// MarshalJSON implements [json.Marshaler].
//
//nolint:unparam // json.Marshaler requires the error result
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + RedactedValue + `"`), nil }

// MarshalText implements [encoding.TextMarshaler], so YAML and other text encoders never leak the value.
//
//nolint:unparam // encoding.TextMarshaler requires the error result
func (Secret) MarshalText() ([]byte, error) { return []byte(RedactedValue), nil }
