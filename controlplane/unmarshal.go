package controlplane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DecodeInfo reports what a decoder noticed besides the document.
type DecodeInfo struct {
	// UnknownFields lists the paths of fields that the structs of this package do not know, for example
	// "objects[2].extra". Published documents are forward compatible: unknown fields are reported, not fatal.
	UnknownFields []string
}

// UnmarshalManifest reads a manifest from YAML or JSON. It rejects another kind or an unsupported apiVersion
// whole (ErrWrongKind, ErrUnsupportedAPIVersion) and reports unknown fields in DecodeInfo. It does not
// validate; call ValidateManifest.
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
// (ErrUnsupportedFormat). A flag file has no header: any top-level map is flag format v1.
func UnmarshalFlags(data []byte, format string) (Flags, DecodeInfo, error) {
	var asJSON bool

	switch strings.ToLower(format) {
	case "", FormatYAML, "yml":
	case FormatJSON:
		asJSON = true
	default:
		return nil, DecodeInfo{}, fmt.Errorf("%w: %q", ErrUnsupportedFormat, format)
	}

	src, err := parse(data, asJSON)
	if err != nil {
		return nil, DecodeInfo{}, err
	}

	flags := Flags{}

	info, err := src.into(&flags)
	if err != nil {
		return nil, DecodeInfo{}, err
	}

	if flags == nil {
		flags = Flags{}
	}

	return flags, info, nil
}

type header struct {
	APIVersion string `json:"apiVersion" yaml:"apiVersion"`
	Kind       string `json:"kind"       yaml:"kind"`
}

func decodeDocument(data []byte, wantKind string, out any) (DecodeInfo, error) {
	src, err := parse(data, looksLikeJSON(data))
	if err != nil {
		return DecodeInfo{}, err
	}

	var head header

	err = src.decode(&head)
	if err != nil {
		return DecodeInfo{}, err
	}

	err = checkHeader(head.APIVersion, head.Kind, wantKind)
	if err != nil {
		return DecodeInfo{}, err
	}

	return src.into(out)
}

// source is a document parsed once (YAML) or kept as bytes (JSON) and decoded several times.
type source struct {
	raw  []byte
	node yaml.Node
	json bool
}

func parse(data []byte, asJSON bool) (*source, error) {
	src := &source{json: asJSON, raw: data}
	if asJSON {
		if !json.Valid(data) {
			return nil, fmt.Errorf("%w: invalid JSON", ErrDecode)
		}

		return src, nil
	}

	err := yaml.Unmarshal(data, &src.node)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecode, err)
	}

	return src, nil
}

func (src *source) decode(out any) error {
	var err error

	switch {
	case src.json:
		err = json.Unmarshal(src.raw, out)
	case src.node.Kind == 0:
		// An empty YAML document decodes to the zero value.
	default:
		err = src.node.Decode(out)
	}

	if err != nil {
		return fmt.Errorf("%w: %w", ErrDecode, err)
	}

	return nil
}

// into decodes the document into out and collects the unknown fields.
func (src *source) into(out any) (DecodeInfo, error) {
	err := src.decode(out)
	if err != nil {
		return DecodeInfo{}, err
	}

	var generic any

	err = src.decode(&generic)
	if err != nil {
		return DecodeInfo{}, err
	}

	var unknown []string

	collectUnknown("", generic, reflect.TypeOf(out).Elem(), &unknown)

	return DecodeInfo{UnknownFields: unknown}, nil
}

func looksLikeJSON(data []byte) bool {
	trimmed := bytes.TrimLeft(data, " \t\r\n")

	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
}

// checkHeader gates a document on its kind and apiVersion.
func checkHeader(apiVersion, kind, wantKind string) error {
	if kind != wantKind {
		return fmt.Errorf("%w: got %q, want %q", ErrWrongKind, kind, wantKind)
	}

	if apiVersion != APIVersion {
		return fmt.Errorf("%w: got %q, supported %q", ErrUnsupportedAPIVersion, apiVersion, APIVersion)
	}

	return nil
}

// isTime reports whether typ is [time.Time], which is a leaf of the document.
func isTime(typ reflect.Type) bool {
	return typ.PkgPath() == "time" && typ.Name() == "Time"
}

// collectUnknown walks a generic decoded value next to the type it was decoded into and appends the paths of
// map keys that no struct field names.
func collectUnknown(path string, value any, typ reflect.Type, out *[]string) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	switch typ.Kind() { //nolint:exhaustive // the other kinds are leaves
	case reflect.Struct:
		if !isTime(typ) {
			collectStruct(path, value, typ, out)
		}
	case reflect.Map:
		entries, ok := stringMap(value)
		if !ok {
			return
		}

		for _, key := range sortedKeys(entries) {
			collectUnknown(joinPath(path, key), entries[key], typ.Elem(), out)
		}
	case reflect.Slice:
		items, ok := value.([]any)
		if !ok {
			return
		}

		for i, item := range items {
			collectUnknown(path+"["+strconv.Itoa(i)+"]", item, typ.Elem(), out)
		}
	}
}

func collectStruct(path string, value any, typ reflect.Type, out *[]string) {
	entries, ok := stringMap(value)
	if !ok {
		return
	}

	fields := make(map[string]reflect.Type, typ.NumField())

	for field := range typ.Fields() {
		if name, _, _ := strings.Cut(field.Tag.Get("yaml"), ","); name != "" && name != "-" {
			fields[name] = field.Type
		}
	}

	for _, key := range sortedKeys(entries) {
		fieldType, known := fields[key]
		if !known {
			*out = append(*out, joinPath(path, key))

			continue
		}

		collectUnknown(joinPath(path, key), entries[key], fieldType, out)
	}
}

func stringMap(value any) (map[string]any, bool) {
	switch entries := value.(type) {
	case map[string]any:
		return entries, true
	case map[any]any:
		converted := make(map[string]any, len(entries))
		for key, entry := range entries {
			converted[fmt.Sprint(key)] = entry
		}

		return converted, true
	}

	return nil, false
}

func sortedKeys(entries map[string]any) []string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}

	return path + "." + key
}
