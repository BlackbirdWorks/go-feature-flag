package schemadoc

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Key is one key of a schema: its path, type and constraints.
type Key struct {
	// Path is the dotted key path. A list item is "[]" and the name of a map entry is "<name>".
	Path string
	// Type is the type of the value, for example "string", "duration" or "list of object".
	Type string
	// Default is the default of the schema, empty when there is none.
	Default string
	// Allowed lists the constraints of the value: enumeration, bounds, pattern.
	Allowed string
	// Description is the description of the schema, empty when there is none.
	Description string
	// OnlyFor lists the kinds (the values of kind or type) that accept the key; empty means all.
	OnlyFor []string
	// RequiredFor lists the kinds that require the key.
	RequiredFor []string
	// Required says that the parent object requires the key in every case.
	Required bool
}

type object = map[string]any

// leafType returns the type name of the definitions that are values with a format, not objects to descend into.
func leafType(name string) (string, bool) {
	switch name {
	case "Secret":
		return "secret", true
	case "Duration", "Delay":
		return "duration", true
	case "ByteSize":
		return "size", true
	default:
		return "", false
	}
}

type walker struct {
	defs object
	keys map[string]*Key
	// order keeps the first sight of a path.
	order []string
}

// Keys returns the keys of the schema, in the order of first sight.
func Keys(raw []byte) ([]Key, error) {
	var doc object

	err := json.Unmarshal(raw, &doc)
	if err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}

	defs, _ := doc["$defs"].(object)
	w := &walker{defs: defs, keys: map[string]*Key{}}

	w.node(doc, "", nil)

	out := make([]Key, 0, len(w.order))
	for _, path := range w.order {
		out = append(out, *w.keys[path])
	}

	return out, nil
}

// resolve follows $ref and merges the siblings. It returns the name of a leaf definition when the reference is one.
func (w *walker) resolve(node object) (object, string) {
	for range 8 {
		ref, found := node["$ref"].(string)
		if !found {
			return node, ""
		}

		name := ref[strings.LastIndex(ref, "/")+1:]
		target, _ := w.defs[name].(object)
		node = overlay(target, node)

		if leaf, isLeaf := leafType(name); isLeaf {
			return node, leaf
		}
	}

	return node, ""
}

// overlay copies target and sets the keys of node but $ref on it.
func overlay(target, node object) object {
	merged := object{}

	maps.Copy(merged, target)

	for key, value := range node {
		if key != "$ref" {
			merged[key] = value
		}
	}

	return merged
}

// node walks the object properties of a schema node: the keys below path.
func (w *walker) node(raw object, path string, kinds []string) {
	node, leaf := w.resolve(raw)
	if leaf != "" {
		return
	}

	for _, field := range []string{"oneOf", "anyOf"} {
		branches, _ := node[field].([]any)
		for _, branch := range branches {
			member, _ := branch.(object)
			resolved, _ := w.resolve(member)
			w.node(member, path, memberKinds(resolved))
		}
	}

	props, _ := node["properties"].(object)
	required := stringList(node["required"])
	discriminator, discKinds := discriminatorOf(node)
	forbidden, requiredBy := conditions(node, discriminator, discKinds)

	for _, name := range slices.Sorted(maps.Keys(props)) {
		childNode, _ := props[name].(object)
		childPath := join(path, name)
		key := w.emit(childPath, childNode)

		if slices.Contains(required, name) {
			key.Required = true
		}

		key.OnlyFor = mergeSorted(key.OnlyFor, kinds)

		if len(forbidden[name]) > 0 {
			key.OnlyFor = mergeSorted(key.OnlyFor, subtract(discKinds, forbidden[name]))
		}

		key.RequiredFor = mergeSorted(key.RequiredFor, requiredBy[name])

		w.descend(childNode, childPath)
	}
}

// descend walks below a value: a map entry, a list item or an object.
func (w *walker) descend(raw object, path string) {
	node, leaf := w.resolve(raw)
	if leaf != "" {
		return
	}

	if extra, isObject := node["additionalProperties"].(object); isObject {
		value, _ := w.resolve(extra)
		if hasProperties(value) {
			w.node(extra, path+".<name>", nil)
		}
	}

	if items, isObject := node["items"].(object); isObject {
		w.node(items, path+"[]", nil)
	}

	if hasProperties(node) || hasBranches(node) {
		w.node(raw, path, nil)
	}
}

func hasProperties(node object) bool {
	props, _ := node["properties"].(object)

	return len(props) > 0 || hasBranches(node)
}

func hasBranches(node object) bool {
	_, one := node["oneOf"]
	_, either := node["anyOf"]

	return one || either
}

// emit records the key of a value, merging with an earlier sight of the same path.
func (w *walker) emit(path string, raw object) *Key {
	node, leaf := w.resolve(raw)
	entry := &Key{Path: path, Type: w.typeOf(node, leaf), Allowed: allowedOf(node, leaf), Default: defaultOf(node)}

	if leaf == "" {
		entry.Description, _ = node["description"].(string)
	}

	known, found := w.keys[path]
	if !found {
		w.keys[path] = entry
		w.order = append(w.order, path)

		return entry
	}

	known.Type = unionText(known.Type, entry.Type, " or ")
	known.Allowed = mergeAllowed(known.Allowed, entry.Allowed)

	if known.Default == "" {
		known.Default = entry.Default
	}

	if known.Description == "" {
		known.Description = entry.Description
	}

	return known
}

func (w *walker) typeOf(node object, leaf string) string {
	if leaf != "" {
		return leaf
	}

	var parts []string

	for _, field := range []string{"oneOf", "anyOf"} {
		branches, _ := node[field].([]any)
		for _, branch := range branches {
			member, _ := branch.(object)
			resolved, memberLeaf := w.resolve(member)
			parts = unionList(parts, []string{w.typeOf(resolved, memberLeaf)})
		}
	}

	if len(parts) > 0 {
		return strings.Join(parts, " or ")
	}

	kind, _ := node["type"].(string)

	switch kind {
	case "array":
		items, _ := node["items"].(object)
		resolved, itemLeaf := w.resolve(items)

		return "list of " + w.typeOf(resolved, itemLeaf)
	case "object":
		if extra, isObject := node["additionalProperties"].(object); isObject {
			resolved, valueLeaf := w.resolve(extra)

			return "map of " + w.typeOf(resolved, valueLeaf)
		}

		if len(properties(node)) == 0 {
			return "map"
		}

		return "object"
	case "":
		return "any"
	default:
		return kind
	}
}

func properties(node object) object {
	props, _ := node["properties"].(object)

	return props
}

func allowedOf(node object, leaf string) string {
	var parts []string

	if values, isList := node["enum"].([]any); isList {
		names := make([]string, 0, len(values))

		for _, value := range values {
			text := scalar(value)
			if text != "" {
				names = append(names, "`"+text+"`")
			}
		}

		if len(names) > 0 {
			parts = append(parts, "one of "+strings.Join(names, ", "))
		}
	}

	parts = append(parts, boundsOf(node)...)

	if unique, _ := node["uniqueItems"].(bool); unique {
		parts = append(parts, "no duplicate")
	}

	if format, _ := node["format"].(string); format != "" {
		parts = append(parts, "format "+format)
	}

	pattern, _ := node["pattern"].(string)
	if pattern != "" && leaf == "" && len(pattern) <= 70 && !strings.Contains(pattern, "(?") {
		parts = append(parts, "matches `"+pattern+"`")
	}

	if names, isObject := node["propertyNames"].(object); isObject {
		if namePattern, _ := names["pattern"].(string); namePattern != "" {
			parts = append(parts, "names match `"+namePattern+"`")
		}
	}

	return strings.Join(parts, "; ")
}

func boundsOf(node object) []string {
	var parts []string

	for _, bound := range [][2]string{
		{"minimum", "at least "}, {"maximum", "at most "}, {"exclusiveMinimum", "more than "},
		{"exclusiveMaximum", "less than "}, {"minLength", "length at least "}, {"maxLength", "length at most "},
		{"minItems", "at least items: "}, {"maxItems", "at most items: "},
	} {
		if value, found := node[bound[0]]; found {
			parts = append(parts, bound[1]+scalar(value))
		}
	}

	return parts
}

func defaultOf(node object) string {
	value, found := node["default"]
	if !found {
		return ""
	}

	return scalar(value)
}

func scalar(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return ""
		}

		return string(raw)
	}
}

// discriminatorOf returns the property that picks the branch of the allOf conditions: kind or type.
func discriminatorOf(node object) (string, []string) {
	props := properties(node)

	for _, name := range []string{"kind", "type"} {
		prop, _ := props[name].(object)

		values := stringList(prop["enum"])
		if len(values) > 0 {
			return name, slices.DeleteFunc(values, func(v string) bool { return v == "" })
		}
	}

	return "", nil
}

// conditions reads the if/then pairs of allOf: the kinds that forbid a key and the kinds that require it.
func conditions(node object, discriminator string, all []string) (map[string][]string, map[string][]string) {
	forbidden, required := map[string][]string{}, map[string][]string{}
	branches, _ := node["allOf"].([]any)

	for _, branch := range branches {
		item, _ := branch.(object)
		cond, _ := item["if"].(object)
		then, _ := item["then"].(object)
		values := conditionValues(cond, discriminator, all)

		for name, rule := range properties(then) {
			if allowed, isBool := rule.(bool); isBool && !allowed {
				forbidden[name] = mergeSorted(forbidden[name], values)
			}
		}

		for _, name := range stringList(then["required"]) {
			required[name] = mergeSorted(required[name], values)
		}
	}

	return forbidden, required
}

// conditionValues returns the kinds an if clause selects: a const, or the kinds a "not enum" leaves out.
func conditionValues(cond object, discriminator string, all []string) []string {
	if inner, isNot := cond["not"].(object); isNot {
		listed := stringList(propertyOf(inner, discriminator)["enum"])

		return subtract(all, listed)
	}

	value, _ := propertyOf(cond, discriminator)["const"].(string)
	if value == "" {
		return nil
	}

	return []string{value}
}

func propertyOf(node object, name string) object {
	prop, _ := properties(node)[name].(object)

	return prop
}

// memberKinds are the type values of a oneOf member.
func memberKinds(member object) []string {
	return slices.DeleteFunc(stringList(propertyOf(member, "type")["enum"]), func(v string) bool { return v == "" })
}

func stringList(value any) []string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))

	for _, item := range items {
		if text, isString := item.(string); isString {
			out = append(out, text)
		}
	}

	return out
}

func join(path, name string) string {
	if path == "" {
		return name
	}

	return path + "." + name
}

func subtract(all, minus []string) []string {
	var out []string

	for _, value := range all {
		if !slices.Contains(minus, value) {
			out = append(out, value)
		}
	}

	return out
}

func mergeSorted(base, more []string) []string {
	out := slices.Clone(base)

	for _, value := range more {
		if !slices.Contains(out, value) {
			out = append(out, value)
		}
	}

	slices.Sort(out)

	return out
}

func unionList(base, more []string) []string {
	out := slices.Clone(base)

	for _, value := range more {
		if value != "" && !slices.Contains(out, value) {
			out = append(out, value)
		}
	}

	return out
}

func unionText(base, more, sep string) string {
	switch {
	case base == "" || base == more:
		return more
	case more == "" || strings.Contains(sep+base+sep, sep+more+sep):
		return base
	default:
		return base + sep + more
	}
}

// mergeAllowed joins two constraint texts; two enumerations become one.
func mergeAllowed(base, more string) string {
	const prefix = "one of "

	if strings.HasPrefix(base, prefix) && strings.HasPrefix(more, prefix) && !strings.Contains(base, ";") &&
		!strings.Contains(more, ";") {
		return base + ", " + strings.TrimPrefix(more, prefix)
	}

	return unionText(base, more, "; ")
}
