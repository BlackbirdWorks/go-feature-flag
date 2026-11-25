package config

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/dustin/go-humanize"
)

var errMustNotBeNegative = errors.New("must not be negative")

var (
	errInvalidSize = errors.New("invalid size")
)

const (
	// DefaultValidateFlagsMaxJSONBodySize is the default maximum size of a JSON body (1 MiB).
	DefaultValidateFlagsMaxJSONBodySize ByteSize = 1 << 20
	// DefaultValidateFlagsMaxYAMLBodySize is the default maximum size of a YAML body (64 KiB).
	DefaultValidateFlagsMaxYAMLBodySize ByteSize = 64 << 10
	// DefaultValidateFlagsMaxTOMLBodySize is the default maximum size of a TOML body (64 KiB).
	DefaultValidateFlagsMaxTOMLBodySize ByteSize = 64 << 10
)

// ByteSize is a size in bytes.
// In the configuration it can be a plain integer (bytes) or a human-readable string like "1MiB" or "64KiB".
type ByteSize int64

// UnmarshalText implements [encoding.TextUnmarshaler], it is used by the koanf decode hook to
// convert the strings (configuration file and environment variables) to a number of bytes.
func (b *ByteSize) UnmarshalText(text []byte) error {
	raw := strings.TrimSpace(string(text))
	if raw == "" {
		// an empty value (for example an empty environment variable) means unset.
		*b = 0

		return nil
	}

	if strings.HasPrefix(raw, "-") {
		return fmt.Errorf("%w %q: must not be negative", errInvalidSize, raw)
	}

	parsedUint, uintErr := strconv.ParseUint(raw, 10, 63)
	if uintErr == nil {
		*b = ByteSize(parsedUint)

		return nil
	}

	_, floatErr := strconv.ParseFloat(raw, 64)
	if floatErr == nil {
		// a number without unit that is not an integer (1.5, 1e3), do not truncate it silently.
		return fmt.Errorf("%w %q: a number of bytes must be an integer", errInvalidSize, raw)
	}

	parsed, err := humanize.ParseBytes(raw)
	if err != nil {
		return fmt.Errorf("invalid size %q: %w", raw, err)
	}

	if parsed > math.MaxInt64 {
		return fmt.Errorf("%w %q: too large", errInvalidSize, raw)
	}

	*b = ByteSize(parsed)

	return nil
}

// Endpoints is the configuration of the endpoints of the relay proxy.
type Endpoints struct {
	// ValidateFlags configures the POST /admin/v1/flags/validate endpoint.
	ValidateFlags ValidateFlagsEndpoint `koanf:"validateflags" mapstructure:"validateFlags"`
}

// ValidateFlagsEndpoint configures the body size limits of the validate flags endpoint.
// A value of 0 means that the default is used.
type ValidateFlagsEndpoint struct {
	// MaxJSONBodySize is the maximum size of a JSON body.
	// Default: 1 MiB
	MaxJSONBodySize ByteSize `koanf:"maxjsonbodysize" mapstructure:"maxJSONBodySize"`
	// MaxYAMLBodySize is the maximum size of a YAML body.
	// Default: 64 KiB
	MaxYAMLBodySize ByteSize `koanf:"maxyamlbodysize" mapstructure:"maxYAMLBodySize"`
	// MaxTOMLBodySize is the maximum size of a TOML body.
	// Default: 64 KiB
	MaxTOMLBodySize ByteSize `koanf:"maxtomlbodysize" mapstructure:"maxTOMLBodySize"`
}

// WithDefaults returns a copy where the unset (zero) limits are replaced by their default.
func (v ValidateFlagsEndpoint) WithDefaults() ValidateFlagsEndpoint {
	if v.MaxJSONBodySize == 0 {
		v.MaxJSONBodySize = DefaultValidateFlagsMaxJSONBodySize
	}

	if v.MaxYAMLBodySize == 0 {
		v.MaxYAMLBodySize = DefaultValidateFlagsMaxYAMLBodySize
	}

	if v.MaxTOMLBodySize == 0 {
		v.MaxTOMLBodySize = DefaultValidateFlagsMaxTOMLBodySize
	}

	return v
}

// IsValid validates the configuration of the endpoints.
func (e Endpoints) IsValid() error {
	return e.ValidateFlags.IsValid()
}

// IsValid validates the limits, negative values are rejected.
func (v ValidateFlagsEndpoint) IsValid() error {
	for _, limit := range []struct {
		name string
		size ByteSize
	}{
		{"endpoints.validateFlags.maxJSONBodySize", v.MaxJSONBodySize},
		{"endpoints.validateFlags.maxYAMLBodySize", v.MaxYAMLBodySize},
		{"endpoints.validateFlags.maxTOMLBodySize", v.MaxTOMLBodySize},
	} {
		if limit.size < 0 {
			return fmt.Errorf("%s %w", limit.name, errMustNotBeNegative)
		}
	}

	return nil
}
