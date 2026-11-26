package connection

import (
	"fmt"
	"io"
	"log/slog"
)

// RedactedValue is what a [Secret] prints instead of its content.
const RedactedValue = "[REDACTED]"

// Secret is secret material, already resolved to its value. Whatever the fmt verb (%v %+v %#v %s %q %d %x %c
// %U ...), the JSON or text encoder (as a value, a field, a map value or a map key), the YAML encoder or the
// slog handler, it prints [RedactedValue]. Use [Secret.Reveal] where the value is really needed.
//
// It is a struct with one unexported field, so the content cannot be reached by a conversion, and a Secret
// cannot be a JSON map key without going through [Secret.MarshalText].
//
// A Secret is not meant to be a map key. As a key it encodes to the redaction marker like everywhere else, so
// several secret keys give several JSON members with the same name, and a decoder keeps only one of them. That
// is deliberate: the value is never emitted, whatever it costs in entries.
//
// What cannot be covered: %p, reflection (including fmt's own printing of an unexported field holding a
// Secret), unsafe, and a debugger or a core dump.
type Secret struct {
	value string
}

// NewSecret returns the Secret holding value.
func NewSecret(value string) Secret { return Secret{value: value} }

// Reveal returns the secret value.
func (s Secret) Reveal() string { return s.value }

// IsZero reports whether the secret is empty.
func (s Secret) IsZero() bool { return s.value == "" }

// Format implements [fmt.Formatter]: every verb prints [RedactedValue].
func (Secret) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, RedactedValue) }

// String implements [fmt.Stringer].
func (Secret) String() string { return RedactedValue }

// GoString implements [fmt.GoStringer].
func (Secret) GoString() string { return RedactedValue }

// LogValue implements [slog.LogValuer].
func (Secret) LogValue() slog.Value { return slog.StringValue(RedactedValue) }

// MarshalJSON implements [json.Marshaler].
//
//nolint:unparam // json.Marshaler requires the error result
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + RedactedValue + `"`), nil }

// MarshalText implements [encoding.TextMarshaler], also used by JSON for map keys. Every secret key gets the
// same name, so a map keyed by secrets loses entries when decoded: a Secret is not meant to be a map key.
//
//nolint:unparam // encoding.TextMarshaler requires the error result
func (Secret) MarshalText() ([]byte, error) { return []byte(RedactedValue), nil }

// MarshalYAML implements the Marshaler interface of the YAML encoders.
func (Secret) MarshalYAML() (any, error) { return RedactedValue, nil }
