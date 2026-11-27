package flagdef

import (
	"reflect"
	"strings"
)

// fieldAtPath returns the value at path of a typed object, read as the JSON form of the object names it: the
// elements of a map with string keys, the fields of a struct by the name of their json tag, or by their own name
// without a tag. A field without a name (json:"-"), an unexported field, and what is behind a nil pointer or a nil
// interface is missing. The fields of an embedded struct without a tag are promoted, as they are in JSON. The value
// is returned as a basic type (string, signed or unsigned integer, float), so an integer is exact; ok is false when
// the path leads to nothing or to a value that is not a scalar.
func fieldAtPath(root any, path []string) (any, bool) {
	current := reflect.ValueOf(root)

	for _, name := range path {
		current = indirect(current)
		if !current.IsValid() {
			return nil, false
		}

		switch current.Kind() { //nolint:exhaustive // only the objects that JSON names the members of
		case reflect.Map:
			current = mapMember(current, name)
		case reflect.Struct:
			current = structMember(current, name)
		default:
			return nil, false
		}
	}

	return scalar(indirect(current))
}

// indirect follows pointers and interfaces; it returns the zero Value for a nil one.
func indirect(value reflect.Value) reflect.Value {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return reflect.Value{}
		}

		value = value.Elem()
	}

	return value
}

// mapMember is the element of a map with string keys.
func mapMember(object reflect.Value, name string) reflect.Value {
	if object.Type().Key().Kind() != reflect.String {
		return reflect.Value{}
	}

	return object.MapIndex(reflect.ValueOf(name).Convert(object.Type().Key()))
}

// structMember is the field of a struct that the JSON form of the struct names name, or the zero Value.
func structMember(object reflect.Value, name string) reflect.Value {
	typ := object.Type()

	var embedded []reflect.Value

	for i := range typ.NumField() {
		field := typ.Field(i)
		jsonName, tagged, skip := jsonFieldName(&field)

		if skip {
			continue
		}

		if field.Anonymous && !tagged {
			if inner := indirect(object.Field(i)); inner.IsValid() && inner.Kind() == reflect.Struct {
				embedded = append(embedded, inner)
			}

			continue
		}

		if !field.IsExported() {
			continue
		}

		if jsonName == name {
			return object.Field(i)
		}
	}

	for _, inner := range embedded {
		if member := structMember(inner, name); member.IsValid() {
			return member
		}
	}

	return reflect.Value{}
}

// jsonFieldName is the name that encoding/json gives a field: the name of its tag, else its own. tagged is true when
// the tag names it; skip is true for a field that JSON leaves out (tagged "-", or unexported and not embedded).
func jsonFieldName(field *reflect.StructField) (string, bool, bool) {
	if !field.IsExported() && !field.Anonymous {
		return "", false, true
	}

	tag, hasTag := field.Tag.Lookup("json")
	if !hasTag {
		return field.Name, false, false
	}

	if tag == "-" {
		return "", false, true
	}

	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return field.Name, false, false
	}

	return name, true, false
}

// scalar converts a scalar value to the basic type of its kind: a named string type is a string, and so on.
func scalar(value reflect.Value) (any, bool) {
	if !value.IsValid() {
		return nil, false
	}

	switch value.Kind() { //nolint:exhaustive // only the scalars that make an identifier
	case reflect.String:
		return value.String(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return value.Uint(), true
	case reflect.Float32, reflect.Float64:
		return value.Float(), true
	default:
		return nil, false
	}
}
