package log

import "log/slog"

// RedactedValue is what a [Secret] prints instead of its content.
const RedactedValue = "[REDACTED]"

// Secret is a string that must never reach the logs (API key, token, connection string, ...).
// Whatever the handler or the formatting verb, it only ever prints [RedactedValue].
type Secret string

// LogValue implements [slog.LogValuer].
func (Secret) LogValue() slog.Value { return slog.StringValue(RedactedValue) }

// String implements [fmt.Stringer], so "%v" and "%s" never leak the secret.
func (Secret) String() string { return RedactedValue }

// GoString implements [fmt.GoStringer], so "%#v" never leaks the secret.
func (Secret) GoString() string { return RedactedValue }

// MarshalText implements [encoding.TextMarshaler], so JSON and YAML encoders never leak the secret.
func (Secret) MarshalText() ([]byte, error) { return []byte(RedactedValue), nil }

// SecretAttr is a log attribute holding a secret: only its redacted form is logged.
func SecretAttr(key, value string) slog.Attr {
	return slog.Any(key, Secret(value))
}

// Err is the attribute of an error, under the "error" key. A nil error gives an empty attribute, which the
// handlers skip.
func Err(err error) slog.Attr {
	if err == nil {
		return slog.Attr{}
	}

	return slog.Any(ErrorKey, err)
}
