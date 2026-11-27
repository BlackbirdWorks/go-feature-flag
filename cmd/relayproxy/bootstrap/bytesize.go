package bootstrap

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/dustin/go-humanize"
	"github.com/invopop/jsonschema"
	"gopkg.in/yaml.v3"
)

// byteSizePattern is the string form of a [ByteSize]: a number and an optional unit, such as "1MiB" or "64KiB".
const byteSizePattern = `^(\d+ ?[bB]?|\d+(\.\d+)? ?[kKmMgGtT]i?[bB]?)$`

// byteSizeRE is [byteSizePattern]: a fraction is only allowed with a unit above the byte.
var byteSizeRE = regexp.MustCompile(byteSizePattern)

// Byte units of the printed form of a [ByteSize].
const (
	kib = 1 << 10
	mib = 1 << 20
)

// ByteSize is a size in bytes written as a string such as "1MiB" or "64KiB". A bare number is rejected.
type ByteSize int64

// UnmarshalYAML implements [yaml.Unmarshaler]. Only strings are accepted. Zero is accepted and means the default of
// the setting, as in version 1.
func (b *ByteSize) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != tagStr {
		return fmt.Errorf("%w: write it as a string such as \"64KiB\"", ErrInvalidByteSize)
	}

	size, err := humanize.ParseBytes(node.Value)
	if err != nil || size > math.MaxInt64 || !byteSizeRE.MatchString(node.Value) {
		return fmt.Errorf(
			"%w: expected a size such as \"64KiB\", with a fraction only above the byte",
			ErrInvalidByteSize,
		)
	}

	if size == 0 && strings.ContainsAny(node.Value, "123456789") {
		return fmt.Errorf("%w: the size is below one byte", ErrInvalidByteSize)
	}

	*b = ByteSize(size)

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
		Type:    jsonString,
		Pattern: byteSizePattern,
		Description: "Size string, for example 64KiB or 1MiB. A fraction is allowed above the byte (1.5MiB), not of a " +
			"byte. Zero means the default of the setting.",
	}
}
