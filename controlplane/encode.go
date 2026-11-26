package controlplane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	tagStr       = "!!str"
	tagInt       = "!!int"
	tagFloat     = "!!float"
	tagBool      = "!!bool"
	tagNull      = "!!null"
	tagTimestamp = "!!timestamp"
	tagMerge     = "!!merge"
	tagMap       = "!!map"
	tagSeq       = "!!seq"
)

// encoder turns Go values into a yaml.Node tree, which both the YAML and the JSON writers print. One tree, so
// both formats describe the same document: struct fields in declaration order, map keys sorted, nil and empty
// collections kept apart, floats written as floats, strings that need it double quoted.
type encoder struct{}

func (e encoder) encode(path string, value reflect.Value) (*yaml.Node, error) {
	if !value.IsValid() {
		return nullNode(), nil
	}

	switch value.Kind() { //nolint:exhaustive // the remaining kinds are scalars handled by encodeScalar
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return nullNode(), nil
		}

		return e.encode(path, value.Elem())
	case reflect.Struct:
		if when, ok := reflect.TypeAssert[time.Time](value); ok {
			return scalarNode(tagTimestamp, when.Format(time.RFC3339Nano), 0), nil
		}

		return e.encodeStruct(path, value)
	case reflect.Map:
		return e.encodeMap(path, value)
	case reflect.Slice, reflect.Array:
		return e.encodeSeq(path, value)
	default:
		return e.encodeScalar(path, value)
	}
}

func (e encoder) encodeStruct(path string, value reflect.Value) (*yaml.Node, error) {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}

	for i := range value.NumField() {
		field := value.Type().Field(i)
		name, opts, _ := strings.Cut(field.Tag.Get("yaml"), ",")

		if !field.IsExported() || name == "-" {
			continue
		}

		if name == "" {
			// A field without a yaml name would be dropped silently from the published document.
			return nil, fmt.Errorf("%w: %s: field %s of %s has no yaml name", ErrUnsupportedValue, pathOrRoot(path),
				field.Name, value.Type())
		}

		fieldValue := value.Field(i)
		if strings.Contains(opts, "omitempty") && omitted(fieldValue) {
			continue
		}

		child, err := e.encode(joinPath(path, name), fieldValue)
		if err != nil {
			return nil, err
		}

		node.Content = append(node.Content, e.keyNode(name), child)
	}

	return node, nil
}

// omitted reports whether an omitempty field is left out: nil pointers, maps and slices, and zero scalars. An
// empty but non-nil map or slice is written.
func omitted(value reflect.Value) bool {
	switch value.Kind() { //nolint:exhaustive // the remaining kinds are never omitted
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice:
		return value.IsNil()
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return value.IsZero()
	default:
		return false
	}
}

func (e encoder) encodeMap(path string, value reflect.Value) (*yaml.Node, error) {
	if value.Type().Key().Kind() != reflect.String {
		return nil, fmt.Errorf("%w: %s: map keys must be strings", ErrUnsupportedValue, pathOrRoot(path))
	}

	node := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}

	keys := make([]string, 0, value.Len())
	for _, key := range value.MapKeys() {
		keys = append(keys, key.String())
	}

	slices.Sort(keys)

	for _, key := range keys {
		child, err := e.encode(joinPath(path, key), value.MapIndex(reflect.ValueOf(key).Convert(value.Type().Key())))
		if err != nil {
			return nil, err
		}

		keyNode, err := e.checkedKey(path, key)
		if err != nil {
			return nil, err
		}

		node.Content = append(node.Content, keyNode, child)
	}

	return node, nil
}

func (e encoder) checkedKey(path, key string) (*yaml.Node, error) {
	if !utf8.ValidString(key) {
		return nil, fmt.Errorf("%w: %s: a map key is not valid UTF-8", ErrUnsupportedValue, pathOrRoot(path))
	}

	return e.keyNode(key), nil
}

func (encoder) keyNode(key string) *yaml.Node {
	return stringNode(key)
}

func (e encoder) encodeSeq(path string, value reflect.Value) (*yaml.Node, error) {
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: tagSeq}

	for i := range value.Len() {
		child, err := e.encode(path+"["+strconv.Itoa(i)+"]", value.Index(i))
		if err != nil {
			return nil, err
		}

		node.Content = append(node.Content, child)
	}

	return node, nil
}

func (encoder) encodeScalar(path string, value reflect.Value) (*yaml.Node, error) {
	if number, ok := reflect.TypeAssert[json.Number](value); ok {
		// A number read with json.Decoder.UseNumber stays a number, spelled as it is.
		node, err := jsonNumber(number.String())
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %q is not a JSON number in range", ErrUnsupportedValue, pathOrRoot(path),
				shorten(number.String()))
		}

		return node, nil
	}

	switch value.Kind() { //nolint:exhaustive // other kinds are rejected below
	case reflect.String:
		text := value.String()
		if !utf8.ValidString(text) {
			return nil, fmt.Errorf("%w: %s: a string is not valid UTF-8", ErrUnsupportedValue, pathOrRoot(path))
		}

		return stringNode(text), nil
	case reflect.Bool:
		return scalarNode(tagBool, strconv.FormatBool(value.Bool()), 0), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return scalarNode(tagInt, strconv.FormatInt(value.Int(), 10), 0), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return scalarNode(tagInt, strconv.FormatUint(value.Uint(), 10), 0), nil
	case reflect.Float32, reflect.Float64:
		bits := 64
		if value.Kind() == reflect.Float32 {
			bits = 32
		}

		number := value.Float()
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, fmt.Errorf("%w: %s: NaN and infinity cannot be written", ErrUnsupportedValue, pathOrRoot(path))
		}

		return scalarNode(tagFloat, floatText(number, bits), 0), nil
	default:
		return nil, fmt.Errorf("%w: %s: type %s", ErrUnsupportedValue, pathOrRoot(path), value.Type())
	}
}

// floatText formats a float so that every reader sees a float: an integral value keeps a ".0".
func floatText(number float64, bits int) string {
	text := strconv.FormatFloat(number, 'g', -1, bits)
	if !strings.ContainsAny(text, ".eE") {
		text += ".0"
	}

	return text
}

func pathOrRoot(path string) string {
	if path == "" {
		return "document"
	}

	return path
}

func nullNode() *yaml.Node { return scalarNode(tagNull, "null", 0) }

func scalarNode(tag, value string, style yaml.Style) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value, Style: style}
}

// stringNode is a string scalar, double quoted when a plain scalar could be read back as something else or
// could be altered by a YAML emitter or reader.
func stringNode(text string) *yaml.Node {
	style := yaml.Style(0)
	if needsQuotes(text) {
		style = yaml.DoubleQuotedStyle
	}

	return scalarNode(tagStr, text, style)
}

// needsQuotes reports whether a string must be double quoted: it is empty, could resolve to a bool, null, number
// or timestamp under YAML 1.1 or 1.2, has an edge space, or has a control, separator or other unprintable
// character (a newline, a tab, ...). A quoted string is always safe, so the test is wide.
func needsQuotes(text string) bool {
	if text == "" || ambiguous(text) {
		return true
	}

	first, _ := utf8.DecodeRuneInString(text)
	last, _ := utf8.DecodeLastRuneInString(text)

	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		return true
	}

	for _, char := range text {
		if !unicode.IsPrint(char) || char == 0x2028 || char == 0x2029 || char == 0xFEFF {
			return true
		}
	}

	return false
}

// ambiguous reports whether a plain scalar could be read as a bool, null, number, timestamp or special token.
func ambiguous(text string) bool {
	switch strings.ToLower(text) {
	case "y", "yes", "n", "no", "on", "off", "true", "false", "null", "~", "<<", "=",
		".nan", ".inf", "+.inf", "-.inf":
		return true
	}

	first := text[0]
	if first >= '0' && first <= '9' {
		return true
	}

	if (first == '+' || first == '-' || first == '.') && len(text) > 1 {
		second := text[1]

		return second >= '0' && second <= '9' || second == '.'
	}

	return false
}

func writeYAML(root *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)

	err := enc.Encode(root)
	if err != nil {
		return nil, fmt.Errorf("controlplane: encode yaml: %w", err)
	}

	err = enc.Close()
	if err != nil {
		return nil, fmt.Errorf("controlplane: encode yaml: %w", err)
	}

	return buf.Bytes(), nil
}

// writeJSON prints the tree as indented JSON. Integers stay digits and floats keep their ".0".
func writeJSON(root *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer

	err := jsonNode(&buf, root, 0)
	if err != nil {
		return nil, err
	}

	buf.WriteByte('\n')

	return buf.Bytes(), nil
}

func jsonNode(buf *bytes.Buffer, node *yaml.Node, depth int) error {
	switch node.Kind { //nolint:exhaustive // documents and aliases are never built by the encoder
	case yaml.MappingNode:
		return jsonMap(buf, node, depth)
	case yaml.SequenceNode:
		if len(node.Content) == 0 {
			buf.WriteString("[]")

			return nil
		}

		buf.WriteString("[\n")

		for i, child := range node.Content {
			buf.WriteString(strings.Repeat(" ", yamlIndent*(depth+1)))

			err := jsonNode(buf, child, depth+1)
			if err != nil {
				return err
			}

			if i < len(node.Content)-1 {
				buf.WriteByte(',')
			}

			buf.WriteByte('\n')
		}

		buf.WriteString(strings.Repeat(" ", yamlIndent*depth) + "]")

		return nil
	default:
		return jsonScalar(buf, node)
	}
}

func jsonMap(buf *bytes.Buffer, node *yaml.Node, depth int) error {
	if len(node.Content) == 0 {
		buf.WriteString("{}")

		return nil
	}

	buf.WriteString("{\n")

	for i := 0; i < len(node.Content); i += 2 {
		buf.WriteString(strings.Repeat(" ", yamlIndent*(depth+1)))

		err := jsonString(buf, node.Content[i].Value)
		if err != nil {
			return err
		}

		buf.WriteString(": ")

		err = jsonNode(buf, node.Content[i+1], depth+1)
		if err != nil {
			return err
		}

		if i+2 < len(node.Content) {
			buf.WriteByte(',')
		}

		buf.WriteByte('\n')
	}

	buf.WriteString(strings.Repeat(" ", yamlIndent*depth) + "}")

	return nil
}

func jsonScalar(buf *bytes.Buffer, node *yaml.Node) error {
	switch node.Tag {
	case tagInt, tagFloat, tagBool, tagNull:
		buf.WriteString(node.Value)

		return nil
	default: // strings and timestamps
		return jsonString(buf, node.Value)
	}
}

func jsonString(buf *bytes.Buffer, text string) error {
	var out bytes.Buffer

	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)

	err := enc.Encode(text)
	if err != nil {
		return fmt.Errorf("controlplane: encode json: %w", err)
	}

	buf.Write(bytes.TrimRight(out.Bytes(), "\n"))

	return nil
}
