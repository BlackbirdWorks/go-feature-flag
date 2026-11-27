package bootstrap

import (
	"fmt"
	"math"
	"strconv"

	"github.com/dustin/go-humanize"
	"github.com/invopop/jsonschema"
	"gopkg.in/yaml.v3"
)

// byteSizePattern is the string form of a [ByteSize]: a number and an optional unit, such as "1MiB" or "64KiB".
const byteSizePattern = `^[0-9]+(\.[0-9]+)? ?([kKmMgG]i?[bB]?|[bB])?$`

// Byte units of the printed form of a [ByteSize].
const (
	kib = 1 << 10
	mib = 1 << 20
)

// ByteSize is a size in bytes written as a string such as "1MiB" or "64KiB". A bare number is rejected.
type ByteSize int64

// UnmarshalYAML implements [yaml.Unmarshaler]. Only strings are accepted, and zero is rejected.
func (b *ByteSize) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != tagStr {
		return fmt.Errorf("%w: write it as a string such as \"64KiB\"", ErrInvalidByteSize)
	}

	v, err := humanize.ParseBytes(node.Value)
	if err != nil || v == 0 || v > math.MaxInt64 {
		return fmt.Errorf("%w: expected a positive size such as \"64KiB\"", ErrInvalidByteSize)
	}

	*b = ByteSize(v) //nolint:gosec // bounded above

	return nil
}

// String gives the size in the largest of MiB and KiB that holds it exactly, else in bytes.
func (b ByteSize) String() string {
	switch {
	case b%mib == 0:
		return strconv.FormatInt(int64(b/mib), 10) + "MiB"
	case b%kib == 0:
		return strconv.FormatInt(int64(b/kib), 10) + "KiB"
	}

	return strconv.FormatInt(int64(b), 10) + "B"
}

// MarshalYAML implements [yaml.Marshaler].
func (b ByteSize) MarshalYAML() (any, error) { //nolint:unparam // yaml.Marshaler signature
	return b.String(), nil
}

// MarshalText implements [encoding.TextMarshaler].
func (b ByteSize) MarshalText() ([]byte, error) { //nolint:unparam // encoding.TextMarshaler signature
	return []byte(b.String()), nil
}

// JSONSchema describes the string form.
func (ByteSize) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        jsonString,
		Pattern:     byteSizePattern,
		Description: "Size string, for example 64KiB or 1MiB. It must be greater than zero.",
	}
}
