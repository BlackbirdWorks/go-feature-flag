package bootstrap

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/invopop/jsonschema"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmdhelpers/cmdlog"
)

// RedactedValue is what every secret prints instead of its content.
const RedactedValue = cmdlog.RedactedValue

type secretKind uint8

const (
	secretUnset secretKind = iota
	secretInline
	secretFromEnv
	secretFromFile
)

// Secret is a reference to a sensitive value, written in the file as {fromEnv: NAME} or {fromFile: PATH}.
// A plain scalar is an inline secret: the validation rejects it unless [LoadOptions.AllowInlineSecrets] is set.
// A Secret never prints its content: every formatting verb and every encoder gives [RedactedValue] for an inline
// value and the reference form for the others.
type Secret struct {
	value string
	kind  secretKind
}

// IsSet reports whether the secret has a value or a reference.
func (s Secret) IsSet() bool { return s.kind != secretUnset }

// IsZero reports whether the secret is unset, so that encoders omit it.
func (s Secret) IsZero() bool { return s.kind == secretUnset }

// IsInline reports whether the secret is a plain value written in the file.
func (s Secret) IsInline() bool { return s.kind == secretInline }

// UnmarshalYAML implements [yaml.Unmarshaler].
func (s *Secret) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Tag == nullTag {
			return nil
		}

		*s = Secret{kind: secretInline, value: node.Value}

		return nil
	case yaml.MappingNode:
		if len(node.Content) != 2 || node.Content[1].Kind != yaml.ScalarNode || node.Content[1].Value == "" {
			return fmt.Errorf(
				"%w: expected exactly one of fromEnv or fromFile with a non-empty value",
				ErrInvalidSecret,
			)
		}

		switch node.Content[0].Value {
		case "fromEnv":
			*s = Secret{kind: secretFromEnv, value: node.Content[1].Value}
		case "fromFile":
			*s = Secret{kind: secretFromFile, value: node.Content[1].Value}
		default:
			return fmt.Errorf("%w: expected exactly one of fromEnv or fromFile", ErrInvalidSecret)
		}

		return nil
	case yaml.DocumentNode, yaml.SequenceNode, yaml.AliasNode:
	}

	return fmt.Errorf("%w: expected a mapping with fromEnv or fromFile", ErrInvalidSecret)
}

// MarshalYAML implements [yaml.Marshaler]: the reference form, or the marker for an inline value.
func (s Secret) MarshalYAML() (any, error) { //nolint:unparam // yaml.Marshaler signature
	return s.ref(), nil
}

// MarshalJSON implements [json.Marshaler]: the reference form, or the marker for an inline value.
func (s Secret) MarshalJSON() ([]byte, error) {
	out, err := json.Marshal(s.ref())
	if err != nil {
		return nil, fmt.Errorf("marshal secret: %w", err)
	}

	return out, nil
}

// MarshalText implements [encoding.TextMarshaler].
func (Secret) MarshalText() ([]byte, error) { //nolint:unparam // encoding.TextMarshaler signature
	return []byte(RedactedValue), nil
}

// String implements [fmt.Stringer].
func (Secret) String() string { return RedactedValue }

// GoString implements [fmt.GoStringer].
func (Secret) GoString() string { return RedactedValue }

// Format implements [fmt.Formatter] so that no verb prints the content.
func (Secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(RedactedValue)) }

// LogValue implements [slog.LogValuer].
func (Secret) LogValue() slog.Value { return slog.StringValue(RedactedValue) }

// JSONSchema describes the reference form of a secret.
func (Secret) JSONSchema() *jsonschema.Schema { return secretSchema(false) }

// AdminKey is an entry of auth.adminKeys: a plain string (API keys are UUIDs set dynamically, not secrets in the
// sense of connection credentials) or a secret reference. It is redacted everywhere like a [Secret].
type AdminKey struct {
	Secret
}

// JSONSchema describes a plain string or a reference.
func (AdminKey) JSONSchema() *jsonschema.Schema { return secretSchema(true) }

func secretSchema(allowScalar bool) *jsonschema.Schema {
	props := jsonschema.NewProperties()
	props.Set("fromEnv", &jsonschema.Schema{Type: jsonString, MinLength: new(uint64(1))})
	props.Set("fromFile", &jsonschema.Schema{Type: jsonString, MinLength: new(uint64(1))})

	ref := &jsonschema.Schema{
		Type:                 "object",
		Properties:           props,
		AdditionalProperties: jsonschema.FalseSchema,
		MinProperties:        new(uint64(1)),
		MaxProperties:        new(uint64(1)),
	}
	if !allowScalar {
		return ref
	}

	return &jsonschema.Schema{OneOf: []*jsonschema.Schema{{Type: jsonString, MinLength: new(uint64(1))}, ref}}
}

// ResolvedSecret is the value of a [Secret] once read from the environment or a file. It prints the marker with
// every verb and encoder: the content is only reachable through [ResolvedSecret.Reveal].
type ResolvedSecret struct {
	value string
}

// Reveal returns the content. Pass it to the client that needs it and never log it.
func (r ResolvedSecret) Reveal() string { return r.value }

// String implements [fmt.Stringer].
func (ResolvedSecret) String() string { return RedactedValue }

// GoString implements [fmt.GoStringer].
func (ResolvedSecret) GoString() string { return RedactedValue }

// Format implements [fmt.Formatter] so that no verb prints the content.
func (ResolvedSecret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(RedactedValue)) }

// MarshalText implements [encoding.TextMarshaler].
func (ResolvedSecret) MarshalText() ([]byte, error) { //nolint:unparam // encoding.TextMarshaler signature
	return []byte(RedactedValue), nil
}

// MarshalJSON implements [json.Marshaler].
func (ResolvedSecret) MarshalJSON() ([]byte, error) {
	out, err := json.Marshal(RedactedValue)
	if err != nil {
		return nil, fmt.Errorf("marshal secret: %w", err)
	}

	return out, nil
}

// MarshalYAML implements [yaml.Marshaler].
func (ResolvedSecret) MarshalYAML() (any, error) { return RedactedValue, nil }

// LogValue implements [slog.LogValuer].
func (ResolvedSecret) LogValue() slog.Value { return slog.StringValue(RedactedValue) }

// Resolve reads the value of the secret through the injected environment and file readers.
func (s Secret) Resolve(opts LoadOptions) (ResolvedSecret, error) {
	switch s.kind {
	case secretInline:
		return ResolvedSecret{value: s.value}, nil
	case secretFromEnv:
		if opts.LookupEnv != nil {
			if v, ok := opts.LookupEnv(s.value); ok {
				return ResolvedSecret{value: v}, nil
			}
		}

		return ResolvedSecret{}, fmt.Errorf("%w: environment variable %s is not set", ErrSecretUnresolved, s.value)
	case secretFromFile:
		if opts.ReadFile == nil {
			return ResolvedSecret{}, fmt.Errorf("%w: %w", ErrSecretUnresolved, ErrNoReader)
		}

		b, err := opts.ReadFile(s.value)
		if err != nil {
			return ResolvedSecret{}, fmt.Errorf("%w: file %s: %w", ErrSecretUnresolved, s.value, err)
		}

		return ResolvedSecret{value: strings.TrimRight(string(b), "\r\n")}, nil
	case secretUnset:
	}

	return ResolvedSecret{}, fmt.Errorf("%w: not set", ErrSecretUnresolved)
}

// ref gives the printable form: the reference for fromEnv and fromFile, the marker otherwise.
func (s Secret) ref() any {
	switch s.kind {
	case secretFromEnv:
		return map[string]string{"fromEnv": s.value}
	case secretFromFile:
		return map[string]string{"fromFile": s.value}
	case secretUnset, secretInline:
	}

	return RedactedValue
}
