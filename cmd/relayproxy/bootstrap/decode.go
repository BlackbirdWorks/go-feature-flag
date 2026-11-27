package bootstrap

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
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
	case t.Kind() == reflect.Interface:
		w.walkAny(path, node, t)
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
	selfDecoding := reflect.PointerTo(t).Implements(reflect.TypeFor[yaml.Unmarshaler]())

	if !selfDecoding && !tagMatches(t, node) {
		w.failf(path, node.Line, ErrInvalidValue, "expected %s", kindName(t))

		return
	}

	err := node.Decode(reflect.New(t).Interface())
	if err == nil {
		return
	}

	if selfDecoding && !isYAMLTypeError(err) {
		w.fail(path, node.Line, err)

		return
	}

	w.failf(path, node.Line, ErrInvalidValue, "expected %s", kindName(t))
}

// tagMatches reports whether the scalar has the YAML tag of the Go kind: no coercion of 123 to a string, 3.0 to an
// integer, or an explicit !!binary to a string.
func tagMatches(t reflect.Type, node *yaml.Node) bool {
	if node.Kind != yaml.ScalarNode {
		return false
	}

	switch {
	case t.Kind() == reflect.Bool:
		return node.Tag == "!!bool"
	case t.Kind() == reflect.Int || t.Kind() == reflect.Int64:
		return node.Tag == tagInt
	case t.Kind() == reflect.Float64:
		return node.Tag == tagInt || node.Tag == "!!float"
	case t.Kind() == reflect.String:
		return node.Tag == tagStr
	default:
		return true
	}
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

	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "<<" {
			w.failf(join(path, "<<"), node.Content[i].Line, ErrUnknownKey, "merge keys are not supported")

			return
		}
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
		info, ok := fields[keyNode.Value]

		if path == authPath && !ok {
			// a mis-indented admin key must not be echoed: path and line only
			w.failf(path, keyNode.Line, ErrUnknownKey, "unknown key under auth")

			continue
		}

		w.pos[child] = keyNode.Line

		if seen[keyNode.Value] {
			w.failf(child, keyNode.Line, ErrDuplicateName, "key written twice")

			continue
		}

		seen[keyNode.Value] = true

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

// walkAny walks a value of a free-form setting (the context enrichment): scalars, mappings with string keys and lists,
// at any depth up to maxDepth. Keys are still checked for duplicates.
func (w *walker) walkAny(path string, node *yaml.Node, t reflect.Type) {
	switch node.Kind { //nolint:exhaustive // scalars need no check, aliases were resolved by walk
	case yaml.MappingNode:
		seen := map[string]bool{}

		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode, valNode := node.Content[i], node.Content[i+1]
			child := join(path, keyNode.Value)
			w.pos[child] = keyNode.Line

			switch {
			case keyNode.Kind != yaml.ScalarNode || keyNode.Tag != tagStr:
				w.failf(child, keyNode.Line, ErrInvalidValue, "expected a string key")
			case seen[keyNode.Value]:
				w.failf(child, keyNode.Line, ErrDuplicateName, "name written twice")
			default:
				seen[keyNode.Value] = true

				w.walk(child, valNode, t)
			}
		}
	case yaml.SequenceNode:
		for i, item := range node.Content {
			child := path + "[" + strconv.Itoa(i) + "]"
			w.pos[child] = item.Line

			w.walk(child, item, t)
		}
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

		if valNode.Kind == yaml.ScalarNode && valNode.Tag == nullTag && t.Elem() == reflect.TypeFor[Secret]() {
			w.failf(child, valNode.Line, ErrMissingValue, "empty secret")

			continue
		}

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

// quotedRE matches a quoted piece of a YAML library message, which can hold text of the file.
var quotedRE = regexp.MustCompile("['\"`][^'\"`]*['\"`]")

// syntaxDetail is the message of the YAML library without the quoted pieces of the file it may echo.
func syntaxDetail(err error) string {
	return quotedRE.ReplaceAllString(strings.TrimPrefix(err.Error(), "yaml: "), "'...'")
}

// decodeStrict parses the document, checks the version gate, walks it against the schema and decodes it.
func decodeStrict(data []byte) (*Config, positions, error) {
	var doc yaml.Node

	docs := yaml.NewDecoder(bytes.NewReader(data))

	err := docs.Decode(&doc)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, &Error{Err: ErrSyntax, Detail: syntaxDetail(err)}
	}

	root, err := rootMapping(&doc)
	if err != nil {
		return nil, nil, err
	}

	var extra yaml.Node

	err = docs.Decode(&extra)
	if !errors.Is(err, io.EOF) {
		return nil, nil, &Error{Err: ErrSyntax, Line: extra.Line, Detail: "multiple documents: the file holds one"}
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
		return nil, nil, &Error{Err: ErrSyntax, Detail: syntaxDetail(err)}
	}

	return &cfg, w.pos, nil
}

// rootMapping returns the root mapping of the document after the version gate.
func rootMapping(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, &Error{Path: "version", Err: ErrVersionRequired}
	}

	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, &Error{Err: ErrSyntax, Line: root.Line, Detail: "the file must be a mapping"}
	}

	version := valueNode(root, "version")
	if version == nil || version.Kind != yaml.ScalarNode || version.Tag != tagInt || version.Value != "2" {
		line := root.Line
		if version != nil {
			line = version.Line
		}

		return nil, &Error{Path: "version", Line: line, Err: ErrVersionRequired}
	}

	return root, nil
}
