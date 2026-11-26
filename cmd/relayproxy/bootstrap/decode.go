package bootstrap

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// maxDepth bounds the nesting the loader follows, so a hostile document cannot exhaust the stack.
const maxDepth = 32

// positions maps the dotted path of a key to the line where it is written.
type positions map[string]int

// lineOf returns the line of the path, or of its closest parent that is in the file.
func (p positions) lineOf(path string) int {
	for path != "" {
		if line, ok := p[path]; ok {
			return line
		}

		cut := strings.LastIndexAny(path, ".[")
		if cut < 0 {
			return 0
		}

		path = path[:cut]
	}

	return 0
}

// walker checks a YAML node tree against the Go types of the schema and records the position of every key.
// It reports the unknown keys, duplicate keys, wrong shapes and bad scalar values with their path and line.
type walker struct {
	pos   positions
	errs  []error
	depth int
}

func (w *walker) fail(path string, line int, err error) {
	if _, ok := errors.AsType[*Error](err); ok {
		w.errs = append(w.errs, err)

		return
	}

	w.errs = append(w.errs, &Error{Path: path, Line: line, Err: err})
}

func (w *walker) failf(path string, line int, sentinel error, format string, args ...any) {
	w.errs = append(w.errs, &Error{Path: path, Line: line, Err: sentinel, Detail: fmt.Sprintf(format, args...)})
}

// fieldInfo is a struct field addressed by its YAML name.
type fieldInfo struct {
	typ reflect.Type
}

// yamlFields lists the YAML-addressable fields of a struct type, inlined structs included.
func yamlFields(t reflect.Type, into map[string]fieldInfo) {
	for field := range t.Fields() {
		tag := field.Tag.Get("yaml")
		name, opts, _ := strings.Cut(tag, ",")

		switch {
		case name == "-":
			continue
		case strings.Contains(opts, "inline"):
			yamlFields(field.Type, into)
		case name == "":
			into[strings.ToLower(field.Name)] = fieldInfo{typ: field.Type}
		default:
			into[name] = fieldInfo{typ: field.Type}
		}
	}
}

func (w *walker) walk(path string, node *yaml.Node, t reflect.Type) {
	if node.Kind == yaml.AliasNode {
		node = node.Alias
	}

	w.depth++
	defer func() { w.depth-- }()

	if w.depth > maxDepth {
		w.failf(path, node.Line, ErrSyntax, "nesting too deep")

		return
	}

	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	if node.Kind == yaml.ScalarNode && node.Tag == nullTag {
		return
	}

	if t == reflect.TypeFor[Connection]() {
		w.walkConnection(path, node)

		return
	}

	if reflect.PointerTo(t).Implements(reflect.TypeFor[yaml.Unmarshaler]()) {
		w.leaf(path, node, t)

		return
	}

	switch {
	case t.Kind() == reflect.Struct:
		w.walkStruct(path, node, t)
	case t.Kind() == reflect.Map:
		w.walkMap(path, node, t)
	case t.Kind() == reflect.Slice:
		w.walkSlice(path, node, t)
	default:
		w.leaf(path, node, t)
	}
}

// leaf decodes a scalar, or a self-decoding type, to check it. The message never contains the value.
func (w *walker) leaf(path string, node *yaml.Node, t reflect.Type) {
	err := node.Decode(reflect.New(t).Interface())
	if err == nil {
		return
	}

	if reflect.PointerTo(t).Implements(reflect.TypeFor[yaml.Unmarshaler]()) && !isYAMLTypeError(err) {
		w.fail(path, node.Line, err)

		return
	}

	w.failf(path, node.Line, ErrInvalidValue, "expected %s", kindName(t))
}

func isYAMLTypeError(err error) bool {
	var te *yaml.TypeError

	return errors.As(err, &te)
}

func kindName(t reflect.Type) string {
	switch {
	case t.Kind() == reflect.Bool:
		return "a boolean"
	case t.Kind() == reflect.Int || t.Kind() == reflect.Int64:
		return "an integer"
	case t.Kind() == reflect.Float64:
		return "a number"
	case t.Kind() == reflect.String:
		return "a string"
	default:
		return t.Kind().String()
	}
}

func join(path, key string) string {
	if path == "" {
		return key
	}

	return path + "." + key
}

func (w *walker) walkConnection(path string, node *yaml.Node) {
	if node.Kind != yaml.MappingNode {
		w.failf(path, node.Line, ErrSyntax, "expected a mapping")

		return
	}

	typeNode := valueNode(node, "type")
	if typeNode == nil || typeNode.Value == "" {
		w.failf(path+".type", node.Line, ErrMissingValue, "every connection has a type")

		return
	}

	variant, err := connectionVariant(typeNode.Value)
	if err != nil {
		w.fail(path+".type", typeNode.Line, err)

		return
	}

	w.walkStruct(path, node, variant)
}

func valueNode(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}

	return nil
}

func (w *walker) walkStruct(path string, node *yaml.Node, t reflect.Type) {
	if node.Kind != yaml.MappingNode {
		w.failf(path, node.Line, ErrSyntax, "expected a mapping")

		return
	}

	fields := map[string]fieldInfo{}
	yamlFields(t, fields)

	seen := map[string]bool{}

	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode, valNode := node.Content[i], node.Content[i+1]
		child := join(path, keyNode.Value)
		w.pos[child] = keyNode.Line

		if seen[keyNode.Value] {
			w.failf(child, keyNode.Line, ErrDuplicateName, "key written twice")

			continue
		}

		seen[keyNode.Value] = true

		info, ok := fields[keyNode.Value]
		if !ok {
			if path == "" && keyNode.Value == "flagSets" {
				w.failf(
					child,
					keyNode.Line,
					ErrReservedKey,
					"flag sets arrive from the manifest, not from the bootstrap file",
				)
			} else {
				w.failf(child, keyNode.Line, ErrUnknownKey, "%q", keyNode.Value)
			}

			continue
		}

		w.walk(child, valNode, info.typ)
	}
}

func (w *walker) walkMap(path string, node *yaml.Node, t reflect.Type) {
	if node.Kind != yaml.MappingNode {
		w.failf(path, node.Line, ErrSyntax, "expected a mapping")

		return
	}

	seen := map[string]bool{}

	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode, valNode := node.Content[i], node.Content[i+1]
		child := join(path, keyNode.Value)
		w.pos[child] = keyNode.Line

		if seen[keyNode.Value] {
			w.failf(child, keyNode.Line, ErrDuplicateName, "name written twice")

			continue
		}

		seen[keyNode.Value] = true

		w.walk(child, valNode, t.Elem())
	}
}

func (w *walker) walkSlice(path string, node *yaml.Node, t reflect.Type) {
	if node.Kind != yaml.SequenceNode {
		w.failf(path, node.Line, ErrSyntax, "expected a list")

		return
	}

	for i, item := range node.Content {
		child := path + "[" + strconv.Itoa(i) + "]"
		w.pos[child] = item.Line

		if item.Kind == yaml.ScalarNode && item.Tag == nullTag {
			w.failf(child, item.Line, ErrMissingValue, "empty list entry")

			continue
		}

		w.walk(child, item, t.Elem())
	}
}

// decodeStrict parses the document, checks the version gate, walks it against the schema and decodes it.
func decodeStrict(data []byte) (*Config, positions, error) {
	var doc yaml.Node

	err := yaml.Unmarshal(data, &doc)
	if err != nil {
		return nil, nil, &Error{Err: ErrSyntax, Detail: strings.TrimPrefix(err.Error(), "yaml: ")}
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, nil, &Error{Path: "version", Err: ErrVersionRequired}
	}

	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil, &Error{Err: ErrSyntax, Line: root.Line, Detail: "the file must be a mapping"}
	}

	version := valueNode(root, "version")
	if version == nil || version.Kind != yaml.ScalarNode || version.Tag != "!!int" || version.Value != "2" {
		line := root.Line
		if version != nil {
			line = version.Line
		}

		return nil, nil, &Error{Path: "version", Line: line, Err: ErrVersionRequired}
	}

	w := &walker{pos: positions{}}
	w.walk("", root, reflect.TypeFor[Config]())

	if len(w.errs) > 0 {
		return nil, nil, errors.Join(w.errs...)
	}

	var cfg Config

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	err = dec.Decode(&cfg)
	if err != nil {
		return nil, nil, &Error{Err: ErrSyntax, Detail: strings.TrimPrefix(err.Error(), "yaml: ")}
	}

	return &cfg, w.pos, nil
}
