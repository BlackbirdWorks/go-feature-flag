package bootstrap

import (
	"fmt"
	"strconv"
	"time"

	"github.com/invopop/jsonschema"
	"gopkg.in/yaml.v3"
)

// Duration is a positive duration written as a string such as "60s" or "1m30s".
type Duration time.Duration

// UnmarshalYAML implements [yaml.Unmarshaler]. Only strings are accepted: a bare number is rejected.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return fmt.Errorf("%w: write it as a string such as \"60s\"", ErrInvalidDuration)
	}

	v, err := time.ParseDuration(node.Value)
	if err != nil || v <= 0 {
		return fmt.Errorf("%w: expected a positive duration such as \"60s\"", ErrInvalidDuration)
	}

	*d = Duration(v)

	return nil
}

// MarshalYAML implements [yaml.Marshaler].
func (d Duration) MarshalYAML() (any, error) { //nolint:unparam // yaml.Marshaler signature
	return d.String(), nil
}

// String gives whole seconds as "60s" and any other duration in the standard form.
func (d Duration) String() string {
	if d%Duration(time.Second) == 0 {
		return strconv.FormatInt(int64(time.Duration(d)/time.Second), 10) + "s"
	}

	return time.Duration(d).String()
}

// MarshalText implements [encoding.TextMarshaler].
func (d Duration) MarshalText() ([]byte, error) { //nolint:unparam // encoding.TextMarshaler signature
	return []byte(d.String()), nil
}

// JSONSchema describes the string form.
func (Duration) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        jsonString,
		Pattern:     `^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`,
		Description: "Positive duration string, for example 60s or 1m30s.",
	}
}
