package bootstrap

import (
	"fmt"
	"strconv"
	"time"

	"github.com/invopop/jsonschema"
	"gopkg.in/yaml.v3"
)

// delayPattern is the string form of a [Delay]: like [durationPattern], and zero is allowed.
const delayPattern = `^\+?(([0-9]+(\.[0-9]*)?|\.[0-9]+)(ns|us|µs|μs|ms|s|m|h))+$`

// Delay is a duration written as a string such as "10s", where zero ("0s") is allowed and means no delay.
type Delay time.Duration

// UnmarshalYAML implements [yaml.Unmarshaler]. Only strings are accepted, and a negative value is rejected.
func (d *Delay) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != tagStr {
		return fmt.Errorf("%w: write it as a string such as \"10s\"", ErrInvalidDuration)
	}

	v, err := time.ParseDuration(node.Value)
	if err != nil || v < 0 {
		return fmt.Errorf("%w: expected a duration such as \"10s\" (\"0s\" for none)", ErrInvalidDuration)
	}

	*d = Delay(v)

	return nil
}

// String gives whole seconds as "10s" and any other duration in the standard form.
func (d Delay) String() string {
	if d%Delay(time.Second) == 0 {
		return strconv.FormatInt(int64(time.Duration(d)/time.Second), 10) + "s"
	}

	return time.Duration(d).String()
}

// MarshalYAML implements [yaml.Marshaler].
func (d Delay) MarshalYAML() (any, error) { //nolint:unparam // yaml.Marshaler signature
	return d.String(), nil
}

// MarshalText implements [encoding.TextMarshaler].
func (d Delay) MarshalText() ([]byte, error) { //nolint:unparam // encoding.TextMarshaler signature
	return []byte(d.String()), nil
}

// JSONSchema describes the string form.
func (Delay) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        jsonString,
		Pattern:     delayPattern,
		Description: "Duration string, for example 10s or 1m30s. Zero (0s) means no delay; negative durations are rejected.",
	}
}

// The paths of the shutdown settings in the messages of the loader.
const (
	pathDrainDelay = "server.shutdown.drainDelay"
	pathTimeout    = "server.shutdown.timeout"
)

// Shutdown is how the relay stops: it fails the readiness, waits the drain delay while it keeps serving, then stops
// the listeners and the core within the timeout.
type Shutdown struct {
	// DrainDelay is how long the relay keeps serving after the readiness answered 503, so that load balancers stop
	// sending traffic. Zero, or unset, means no delay.
	DrainDelay *Delay `json:"drainDelay,omitempty" yaml:"drainDelay,omitempty"`
	// Timeout bounds the stop that follows the drain delay (default 15s). When it ends the relay stops hard.
	Timeout *Duration `json:"timeout,omitempty" yaml:"timeout,omitempty"`
}
