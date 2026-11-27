package bootstrap_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// schemaChecker is a small JSON Schema validator for the keywords the bootstrap schema uses. The repository has no
// JSON Schema validation dependency and none is added for a test. Patterns that need a lookahead are skipped:
// the regexp package of the standard library does not support them.
type schemaChecker struct {
	defs map[string]any
}

// schemaObject is a decoded JSON object.
type schemaObject = map[string]any

func newSchemaChecker(raw []byte) (*schemaChecker, schemaObject, error) {
	var doc schemaObject

	err := json.Unmarshal(raw, &doc)
	if err != nil {
		return nil, nil, fmt.Errorf("decode schema: %w", err)
	}

	defs, _ := doc["$defs"].(schemaObject)

	return &schemaChecker{defs: defs}, doc, nil
}

func (c *schemaChecker) resolve(schema schemaObject) schemaObject {
	ref, found := schema["$ref"].(string)
	if !found {
		return schema
	}

	target, _ := c.defs[strings.TrimPrefix(ref, "#/$defs/")].(schemaObject)

	merged := schemaObject{}
	maps.Copy(merged, target)

	for key, value := range schema {
		if key != "$ref" {
			merged[key] = value
		}
	}

	return merged
}

// check returns the violations of the value against the schema.
func (c *schemaChecker) check(path string, schema schemaObject, value any) []string {
	schema = c.resolve(schema)

	rules := []func(string, schemaObject, any) []string{
		checkType, checkValue, c.checkNot, c.checkAllOf, c.checkAnyOf, c.checkOneOf, c.checkObject, c.checkItems,
	}

	out := make([]string, 0, len(rules))

	for _, rule := range rules {
		out = append(out, rule(path, schema, value)...)
	}

	return out
}

func (c *schemaChecker) checkNot(path string, schema schemaObject, value any) []string {
	not, found := schema["not"].(schemaObject)
	if found && len(c.check(path, not, value)) == 0 {
		return []string{path + ": matches the not schema"}
	}

	return nil
}

func (c *schemaChecker) checkAllOf(path string, schema schemaObject, value any) []string {
	var out []string

	all, _ := schema["allOf"].([]any)
	for _, item := range all {
		condition, _ := item.(schemaObject)
		ifSchema, hasIf := condition["if"].(schemaObject)
		thenSchema, hasThen := condition["then"].(schemaObject)

		switch {
		case !hasIf:
			// a plain allOf member
			out = append(out, c.check(path, condition, value)...)
		case hasThen && len(c.check(path, ifSchema, value)) == 0:
			out = append(out, c.check(path, thenSchema, value)...)
		}
	}

	return out
}

func (c *schemaChecker) checkAnyOf(path string, schema schemaObject, value any) []string {
	alternatives, found := schema["anyOf"].([]any)
	if !found {
		return nil
	}

	for _, item := range alternatives {
		alternative, _ := item.(schemaObject)
		if len(c.check(path, alternative, value)) == 0 {
			return nil
		}
	}

	return []string{path + ": matches none of the anyOf alternatives"}
}

func (c *schemaChecker) checkOneOf(path string, schema schemaObject, value any) []string {
	alternatives, found := schema["oneOf"].([]any)
	if !found {
		return nil
	}

	matches := 0

	var nested []string

	for _, item := range alternatives {
		alternative, _ := item.(schemaObject)

		violations := c.check(path, alternative, value)
		if len(violations) == 0 {
			matches++
		}

		nested = append(nested, violations...)
	}

	switch {
	case matches == 0:
		return append([]string{path + ": matches 0 of the oneOf alternatives"}, nested...)
	case matches > 1:
		return []string{path + ": matches several oneOf alternatives"}
	}

	return nil
}

func (c *schemaChecker) checkItems(path string, schema schemaObject, value any) []string {
	list, isList := value.([]any)
	items, hasItems := schema["items"].(schemaObject)

	if !isList || !hasItems {
		return nil
	}

	var out []string

	for i, item := range list {
		out = append(out, c.check(fmt.Sprintf("%s[%d]", path, i), items, item)...)

		if unique, _ := schema["uniqueItems"].(bool); unique && slices.IndexFunc(list, func(other any) bool {
			return reflect.DeepEqual(other, item)
		}) != i {
			out = append(out, fmt.Sprintf("%s[%d]: duplicate item", path, i))
		}
	}

	return out
}

const (
	typeInteger = "integer"
	typeNumber  = "number"
)

func checkType(path string, schema schemaObject, value any) []string {
	want, found := schema["type"].(string)
	if !found {
		return nil
	}

	got := jsonType(value)
	if got == want || (want == typeNumber && got == typeInteger) {
		return nil
	}

	return []string{fmt.Sprintf("%s: want %s, got %s", path, want, got)}
}

func jsonType(value any) string {
	switch typed := value.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case int:
		return typeInteger
	case float64:
		if typed == float64(int(typed)) {
			return typeInteger
		}

		return typeNumber
	case schemaObject:
		return "object"
	case []any:
		return "array"
	}

	return "other"
}

// checkValue checks enum, const, minimum, maximum and pattern.
func checkValue(path string, schema schemaObject, value any) []string {
	var out []string

	enum, hasEnum := schema["enum"].([]any)
	if hasEnum && !slices.ContainsFunc(enum, func(item any) bool { return sameValue(item, value) }) {
		out = append(out, path+": not in the enum")
	}

	text, isText := value.(string)
	if minLength, found := schema["minLength"].(float64); found && isText && float64(len(text)) < minLength {
		out = append(out, path+": shorter than the minimum length")
	}

	if want, hasConst := schema["const"]; hasConst && !sameValue(want, value) {
		out = append(out, path+": not the const")
	}

	out = append(out, checkRange(path, schema, value)...)

	pattern, hasPattern := schema["pattern"].(string)

	if isText && hasPattern && !strings.Contains(pattern, "(?=") && !regexp.MustCompile(pattern).MatchString(text) {
		out = append(out, path+": does not match the pattern")
	}

	return out
}

func checkRange(path string, schema schemaObject, value any) []string {
	number, isNumber := toFloat(value)
	if !isNumber {
		return nil
	}

	var out []string

	if minimum, found := schema["minimum"].(float64); found && number < minimum {
		out = append(out, path+": below the minimum")
	}

	if maximum, found := schema["maximum"].(float64); found && number > maximum {
		out = append(out, path+": above the maximum")
	}

	if exclusive, found := schema["exclusiveMaximum"].(float64); found && number >= exclusive {
		out = append(out, path+": at or above the exclusive maximum")
	}

	return out
}

func toFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case float64:
		return typed, true
	}

	return 0, false
}

// sameValue compares two decoded values, with the numbers of YAML and of JSON as equal.
func sameValue(left, right any) bool {
	leftNumber, leftIsNumber := toFloat(left)
	rightNumber, rightIsNumber := toFloat(right)

	if leftIsNumber && rightIsNumber {
		return leftNumber == rightNumber
	}

	return reflect.DeepEqual(left, right)
}

func (c *schemaChecker) checkObject(path string, schema schemaObject, value any) []string {
	obj, isObject := value.(schemaObject)
	if !isObject {
		return nil
	}

	var out []string

	required, _ := schema["required"].([]any)
	for _, item := range required {
		name, _ := item.(string)
		if _, present := obj[name]; !present {
			out = append(out, path+": missing "+name)
		}
	}

	if names, found := schema["propertyNames"].(schemaObject); found {
		for name := range obj {
			out = append(out, c.check(path+"{"+name+"}", names, name)...)
		}
	}

	out = append(out, checkPropertyCount(path, schema, len(obj))...)

	props, _ := schema["properties"].(schemaObject)

	for name, item := range obj {
		out = append(out, c.checkProperty(path+"."+name, schema, props[name], item)...)
	}

	return out
}

func checkPropertyCount(path string, schema schemaObject, count int) []string {
	var out []string

	if limit, found := schema["minProperties"].(float64); found && float64(count) < limit {
		out = append(out, path+": too few properties")
	}

	if limit, found := schema["maxProperties"].(float64); found && float64(count) > limit {
		out = append(out, path+": too many properties")
	}

	return out
}

func (c *schemaChecker) checkProperty(path string, schema schemaObject, known, value any) []string {
	if allowed, isBool := known.(bool); isBool && !allowed {
		return []string{path + ": not allowed"}
	}

	if sub, found := known.(schemaObject); found {
		return c.check(path, sub, value)
	}

	if extra, found := schema["additionalProperties"].(schemaObject); found {
		return c.check(path, extra, value)
	}

	if allowed, found := schema["additionalProperties"].(bool); found && !allowed {
		return []string{path + ": not allowed"}
	}

	return nil
}
