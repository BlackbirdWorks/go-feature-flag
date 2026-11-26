package controlplane

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DecodeInfo reports what a decoder noticed besides the document.
type DecodeInfo struct {
	// UnknownFields lists the paths of fields that the structs of this package do not know, for example
	// "objects[2].extra", sorted by key within each mapping. Published documents are forward compatible:
	// unknown fields are reported, not fatal.
	UnknownFields []string
}

// UnmarshalManifest reads a manifest from YAML or JSON. It rejects another kind or an unsupported apiVersion
// whole (*KindError, *UnsupportedVersionError) and reports unknown fields in DecodeInfo. It does not validate;
// call ValidateManifest. The rules of every Unmarshal function:
//   - YAML and JSON go through one reader: field names are case sensitive, duplicate keys, non-string keys,
//     NaN, infinity, a second YAML document and trailing JSON data are a *DecodeError;
//   - an integer field rejects a fraction and an overflow; an integer value keeps its type (int, or uint64 above
//     the int64 range) and a float keeps float64, in both formats;
//   - anchors, aliases and merge keys are accepted on read (and never written);
//   - an error never contains the text of a value.
func UnmarshalManifest(data []byte) (*Manifest, DecodeInfo, error) {
	var manifest Manifest

	info, err := decodeDocument(data, DocumentKindManifest, &manifest)
	if err != nil {
		return nil, DecodeInfo{}, err
	}

	return &manifest, info, nil
}

// UnmarshalFlagSet reads a flag-set document from YAML or JSON. See UnmarshalManifest.
func UnmarshalFlagSet(data []byte) (*FlagSetDocument, DecodeInfo, error) {
	var doc FlagSetDocument

	info, err := decodeDocument(data, DocumentKindFlagSet, &doc)
	if err != nil {
		return nil, DecodeInfo{}, err
	}

	return &doc, info, nil
}

// UnmarshalFlags reads a flag file. format is "yaml" (or empty) or "json"; toml is not read by this package
// (ErrUnsupportedFormat). Input that is valid JSON is read as JSON whatever the format says, as JSON is YAML;
// format "json" requires valid JSON. A flag file has no header: any top-level map is flag format v1.
func UnmarshalFlags(data []byte, format string) (Flags, DecodeInfo, error) {
	requireJSON := false

	switch strings.ToLower(format) {
	case "", FormatYAML, "yml":
	case FormatJSON:
		requireJSON = true
	default:
		return nil, DecodeInfo{}, &formatError{format: format}
	}

	root, err := parse(data, requireJSON)
	if err != nil {
		return nil, DecodeInfo{}, err
	}

	flags := Flags{}

	info, err := decodeNode(root, &flags)
	if err != nil {
		return nil, DecodeInfo{}, err
	}

	return flags, info, nil
}

// formatError wraps ErrUnsupportedFormat with the format name.
type formatError struct{ format string }

func (e *formatError) Error() string {
	return ErrUnsupportedFormat.Error() + ": " + strconv.Quote(e.format)
}

func (*formatError) Is(target error) bool { return target == ErrUnsupportedFormat }

func decodeDocument(data []byte, wantKind string, out any) (DecodeInfo, error) {
	root, err := parse(data, false)
	if err != nil {
		return DecodeInfo{}, err
	}

	apiVersion, kind := headerOf(root)

	err = checkHeader(apiVersion, kind, wantKind)
	if err != nil {
		return DecodeInfo{}, err
	}

	return decodeNode(root, out)
}

// checkHeader gates a document on its kind and apiVersion.
func checkHeader(apiVersion, kind, wantKind string) error {
	if kind != wantKind {
		return &KindError{Got: kind, Want: wantKind}
	}

	if apiVersion != APIVersion {
		return &UnsupportedVersionError{Got: apiVersion, Supported: APIVersion}
	}

	return nil
}

// decodeNode checks the node against the type of out, collects the unknown fields and decodes.
func decodeNode(root *yaml.Node, out any) (DecodeInfo, error) {
	var info DecodeInfo

	if root == nil {
		return info, nil
	}

	walk := walker{}

	err := walk.walk("", root, reflect.TypeOf(out).Elem(), 0)
	if err != nil {
		return DecodeInfo{}, err
	}

	err = root.Decode(out)
	if err != nil {
		// The text of the library error can hold a value; keep only that decoding failed.
		return DecodeInfo{}, &DecodeError{Reason: "the document does not match the format", Line: root.Line}
	}

	info.UnknownFields = walk.unknown

	return info, nil
}

// parse reads one YAML or JSON document into a node tree. A nil root is an empty document.
func parse(data []byte, requireJSON bool) (*yaml.Node, error) {
	if json.Valid(data) {
		return jsonTree(data)
	}

	if requireJSON {
		return nil, &DecodeError{Reason: reasonInvalidJSON}
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))

	var doc yaml.Node

	err := dec.Decode(&doc)
	if errors.Is(err, io.EOF) {
		return nil, nil //nolint:nilnil // a nil root is an empty document
	}

	if err != nil {
		return nil, &DecodeError{Reason: "syntax error", Line: errorLine(err)}
	}

	var extra yaml.Node

	err = dec.Decode(&extra)
	if !errors.Is(err, io.EOF) {
		return nil, &DecodeError{Reason: "only one YAML document is allowed", Line: extra.Line}
	}

	return &doc, nil
}

// errorLine extracts the line number of a yaml.v3 syntax error ("yaml: line 3: ...") and nothing else.
func errorLine(err error) int {
	_, rest, found := strings.Cut(err.Error(), "line ")
	if !found {
		return 0
	}

	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}

	line, convErr := strconv.Atoi(rest[:end])
	if convErr != nil {
		return 0
	}

	return line
}

// headerOf reads the apiVersion and kind scalars of the top-level mapping.
func headerOf(root *yaml.Node) (string, string) {
	node := resolve(root)
	if node == nil || node.Kind != yaml.MappingNode {
		return "", ""
	}

	var apiVersion, kind string

	for i := 0; i+1 < len(node.Content); i += 2 {
		value := resolve(node.Content[i+1])
		if value == nil || value.Kind != yaml.ScalarNode {
			continue
		}

		switch node.Content[i].Value {
		case "apiVersion":
			apiVersion = value.Value
		case "kind":
			kind = value.Value
		}
	}

	return apiVersion, kind
}

// resolve follows documents and aliases to the node they stand for.
func resolve(node *yaml.Node) *yaml.Node {
	for node != nil && (node.Kind == yaml.DocumentNode || node.Kind == yaml.AliasNode) {
		if node.Kind == yaml.AliasNode {
			node = node.Alias

			continue
		}

		if len(node.Content) == 0 {
			return nil
		}

		node = node.Content[0]
	}

	return node
}

const (
	reasonInvalidJSON = "invalid JSON"

	maxDepth = 200
	maxSteps = 1_000_000
)

// walker checks a node tree against the Go types it will be decoded into. It is the place of every format rule
// of the readers, and it collects the unknown fields.
type walker struct {
	unknown []string
	steps   int
}

func (*walker) fail(path string, node *yaml.Node, reason string) error {
	return &DecodeError{Path: path, Reason: reason, Line: node.Line}
}

func (w *walker) walk(path string, node *yaml.Node, typ reflect.Type, depth int) error {
	w.steps++
	if w.steps > maxSteps || depth > maxDepth {
		return &DecodeError{Path: path, Reason: "the document is too deep or too large"}
	}

	node = resolve(node)
	if node == nil || node.Kind == yaml.ScalarNode && node.Tag == tagNull {
		return nil
	}

	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	if typ.PkgPath() == "time" && typ.Name() == "Time" {
		return w.walkTime(path, node)
	}

	switch typ.Kind() { //nolint:exhaustive // the other kinds are not part of the format
	case reflect.Interface:
		return w.walkAny(path, node, depth)
	case reflect.Struct, reflect.Map:
		return w.walkMapping(path, node, typ, depth)
	case reflect.Slice:
		return w.walkSlice(path, node, typ, depth)
	default:
		return w.walkScalar(path, node, typ)
	}
}

func (w *walker) walkTime(path string, node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return w.fail(path, node, "expected a timestamp")
	}

	if node.Tag == tagStr {
		_, err := time.Parse(time.RFC3339Nano, node.Value)
		if err != nil {
			return w.fail(path, node, "expected a timestamp")
		}

		node.Tag, node.Style = tagTimestamp, 0
	}

	if node.Tag != tagTimestamp {
		return w.fail(path, node, "expected a timestamp")
	}

	return nil
}

func (w *walker) walkSlice(path string, node *yaml.Node, typ reflect.Type, depth int) error {
	if node.Kind != yaml.SequenceNode {
		return w.fail(path, node, "expected a list")
	}

	for i, item := range node.Content {
		err := w.walk(path+"["+strconv.Itoa(i)+"]", item, typ.Elem(), depth+1)
		if err != nil {
			return err
		}
	}

	return nil
}

// entry is one key and value of a mapping, merge keys expanded.
type mapEntry struct {
	value *yaml.Node
	key   string
	line  int
}

// entries lists the entries of a mapping, rejecting non-string and duplicate keys.
func (w *walker) entries(path string, node *yaml.Node) ([]mapEntry, error) {
	var list []mapEntry

	explicit := map[string]struct{}{}

	var merged []mapEntry

	for i := 0; i+1 < len(node.Content); i += 2 {
		key := resolve(node.Content[i])

		if key == nil || key.Kind != yaml.ScalarNode || key.Tag != tagStr && key.Tag != tagMerge {
			return nil, w.fail(path, node.Content[i], "keys must be strings")
		}

		if key.Tag == tagMerge {
			more, err := w.mergeEntries(path, node.Content[i+1])
			if err != nil {
				return nil, err
			}

			merged = append(merged, more...)

			continue
		}

		if _, dup := explicit[key.Value]; dup {
			return nil, w.fail(joinPath(path, key.Value), key, "duplicate key")
		}

		explicit[key.Value] = struct{}{}
		list = append(list, mapEntry{key: key.Value, value: node.Content[i+1], line: key.Line})
	}

	for _, entry := range merged {
		if _, overridden := explicit[entry.key]; !overridden {
			explicit[entry.key] = struct{}{}
			list = append(list, entry)
		}
	}

	slices.SortStableFunc(list, func(a, b mapEntry) int { return strings.Compare(a.key, b.key) })

	return list, nil
}

// mergeEntries expands the value of a "<<" merge key: a mapping or a list of mappings.
func (w *walker) mergeEntries(path string, value *yaml.Node) ([]mapEntry, error) {
	target := resolve(value)
	if target == nil {
		return nil, nil
	}

	switch target.Kind { //nolint:exhaustive // only mappings and lists of mappings can be merged
	case yaml.MappingNode:
		w.steps++

		return w.entries(path, target)
	case yaml.SequenceNode:
		var all []mapEntry

		for _, item := range target.Content {
			more, err := w.mergeEntries(path, item)
			if err != nil {
				return nil, err
			}

			all = append(all, more...)
		}

		return all, nil
	default:
		return nil, w.fail(path, target, "a merge key needs a mapping")
	}
}

func (w *walker) walkMapping(path string, node *yaml.Node, typ reflect.Type, depth int) error {
	if node.Kind != yaml.MappingNode {
		return w.fail(path, node, "expected a mapping")
	}

	list, err := w.entries(path, node)
	if err != nil {
		return err
	}

	var fields map[string]reflect.Type

	if typ.Kind() == reflect.Struct {
		fields = make(map[string]reflect.Type, typ.NumField())

		for field := range typ.Fields() {
			if name, _, _ := strings.Cut(field.Tag.Get("yaml"), ","); name != "" && name != "-" {
				fields[name] = field.Type
			}
		}
	}

	for _, item := range list {
		child := joinPath(path, item.key)

		if typ.Kind() == reflect.Map {
			err = w.walk(child, item.value, typ.Elem(), depth+1)
		} else if fieldType, known := fields[item.key]; known {
			err = w.walk(child, item.value, fieldType, depth+1)
		} else {
			w.unknown = append(w.unknown, child)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func (w *walker) walkScalar(path string, node *yaml.Node, typ reflect.Type) error {
	if node.Kind != yaml.ScalarNode {
		return w.fail(path, node, "expected a scalar value")
	}

	switch typ.Kind() { //nolint:exhaustive // the other kinds are not part of the format
	case reflect.String:
		return nil
	case reflect.Bool:
		if node.Tag != tagBool {
			return w.fail(path, node, "expected a boolean")
		}

		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return w.walkInteger(path, node, typ, true)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return w.walkInteger(path, node, typ, false)
	case reflect.Float32, reflect.Float64:
		if node.Tag != tagInt && node.Tag != tagFloat {
			return w.fail(path, node, "expected a number")
		}

		return w.checkNumber(path, node)
	default:
		return w.fail(path, node, "unsupported type")
	}
}

// walkInteger checks a scalar against an integer type: no fraction, no overflow.
func (w *walker) walkInteger(path string, node *yaml.Node, typ reflect.Type, signed bool) error {
	if node.Tag == tagFloat && isDigits(node.Value) {
		return w.fail(path, node, "integer out of range") // yaml reads an integer above 64 bits as a float
	}

	if node.Tag != tagInt {
		if signed {
			return w.fail(path, node, "expected an integer")
		}

		return w.fail(path, node, "expected an unsigned integer")
	}

	text := strings.ReplaceAll(node.Value, "_", "")

	var err error
	if signed {
		_, err = strconv.ParseInt(text, 0, typ.Bits())
	} else {
		_, err = strconv.ParseUint(text, 0, typ.Bits())
	}

	if err != nil {
		return w.fail(path, node, "integer out of range")
	}

	return nil
}

// checkNumber rejects NaN, infinity and an integer too large for any integer type.
func (w *walker) checkNumber(path string, node *yaml.Node) error {
	if node.Tag != tagFloat {
		return nil
	}

	switch strings.ToLower(strings.TrimLeft(node.Value, "+-")) {
	case ".nan", ".inf":
		return w.fail(path, node, "NaN and infinity are not allowed")
	}

	if isDigits(node.Value) {
		return w.fail(path, node, "integer out of range")
	}

	return nil
}

// isDigits reports whether a number is spelled as an integer (no fraction, no exponent).
func isDigits(text string) bool {
	return strings.Trim(text, "+-0123456789_") == ""
}

// walkAny checks a value of a free-form position (variation values, metadata): any scalar, list or mapping.
func (w *walker) walkAny(path string, node *yaml.Node, depth int) error {
	switch node.Kind { //nolint:exhaustive // documents and aliases are resolved before
	case yaml.ScalarNode:
		switch node.Tag {
		case tagStr, tagBool, tagTimestamp, tagInt, tagNull:
			return nil
		case tagFloat:
			return w.checkNumber(path, node)
		default:
			return w.fail(path, node, "unsupported value type")
		}
	case yaml.SequenceNode:
		for i, item := range node.Content {
			err := w.walk(path+"["+strconv.Itoa(i)+"]", item, reflect.TypeFor[any](), depth+1)
			if err != nil {
				return err
			}
		}

		return nil
	case yaml.MappingNode:
		list, err := w.entries(path, node)
		if err != nil {
			return err
		}

		for _, item := range list {
			err = w.walk(joinPath(path, item.key), item.value, reflect.TypeFor[any](), depth+1)
			if err != nil {
				return err
			}
		}

		return nil
	default:
		return w.fail(path, node, "unsupported value")
	}
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}

	return path + "." + key
}

// jsonTree reads valid JSON into a node tree, so that JSON and YAML share the walker and the decoder. Strings
// are double quoted nodes (always strings); numbers are ints or floats by their spelling.
func jsonTree(data []byte) (*yaml.Node, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	node, err := buildJSON(dec, 0)
	if err != nil {
		return nil, err
	}

	_, err = dec.Token()
	if !errors.Is(err, io.EOF) {
		return nil, &DecodeError{Reason: "unexpected data after the JSON value"}
	}

	return node, nil
}

func buildJSON(dec *json.Decoder, depth int) (*yaml.Node, error) {
	if depth > maxDepth {
		return nil, &DecodeError{Reason: "the document is too deep or too large"}
	}

	tok, err := dec.Token()
	if err != nil {
		return nil, &DecodeError{Reason: reasonInvalidJSON}
	}

	switch value := tok.(type) {
	case json.Delim:
		if value == '{' {
			return buildJSONContainer(dec, depth, yaml.MappingNode, tagMap)
		}

		return buildJSONContainer(dec, depth, yaml.SequenceNode, tagSeq)
	case string:
		return scalarNode(tagStr, value, yaml.DoubleQuotedStyle), nil
	case json.Number:
		return jsonNumber(value.String())
	case bool:
		return scalarNode(tagBool, strconv.FormatBool(value), 0), nil
	default:
		return nullNode(), nil
	}
}

func buildJSONContainer(dec *json.Decoder, depth int, kind yaml.Kind, tag string) (*yaml.Node, error) {
	node := &yaml.Node{Kind: kind, Tag: tag}

	for dec.More() {
		if kind == yaml.MappingNode {
			tok, err := dec.Token()

			name, ok := tok.(string)
			if err != nil || !ok {
				return nil, &DecodeError{Reason: reasonInvalidJSON}
			}

			node.Content = append(node.Content, scalarNode(tagStr, name, yaml.DoubleQuotedStyle))
		}

		child, err := buildJSON(dec, depth+1)
		if err != nil {
			return nil, err
		}

		node.Content = append(node.Content, child)
	}

	_, err := dec.Token() // the closing delimiter
	if err != nil {
		return nil, &DecodeError{Reason: reasonInvalidJSON}
	}

	return node, nil
}

// jsonNumber classifies a JSON number by its spelling: no fraction or exponent is an integer.
func jsonNumber(text string) (*yaml.Node, error) {
	if !strings.ContainsAny(text, ".eE") {
		_, intErr := strconv.ParseInt(text, 10, 64)
		_, uintErr := strconv.ParseUint(text, 10, 64)

		if intErr != nil && uintErr != nil {
			return nil, &DecodeError{Reason: "integer out of range"}
		}

		return scalarNode(tagInt, text, 0), nil
	}

	number, err := strconv.ParseFloat(text, 64)
	if err != nil || number-number != 0 {
		return nil, &DecodeError{Reason: "number out of range"}
	}

	return scalarNode(tagFloat, text, 0), nil
}
