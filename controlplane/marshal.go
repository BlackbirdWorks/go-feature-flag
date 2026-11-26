package controlplane

import (
	"reflect"

	"gopkg.in/yaml.v3"
)

const yamlIndent = 2

// MarshalManifest writes m as deterministic YAML. An empty APIVersion or Kind is filled in; any other value
// than the current one is an error. m is not changed and not validated.
func MarshalManifest(m *Manifest) ([]byte, error) {
	doc, err := headedManifest(m)
	if err != nil {
		return nil, err
	}

	return marshal(doc, writeYAML)
}

// MarshalManifestJSON writes m as deterministic indented JSON: the same document, field order and number
// types as MarshalManifest.
func MarshalManifestJSON(m *Manifest) ([]byte, error) {
	doc, err := headedManifest(m)
	if err != nil {
		return nil, err
	}

	return marshal(doc, writeJSON)
}

// MarshalFlagSet writes d as deterministic YAML. See MarshalManifest for the header rules.
func MarshalFlagSet(d *FlagSetDocument) ([]byte, error) {
	doc, err := headedFlagSet(d)
	if err != nil {
		return nil, err
	}

	return marshal(doc, writeYAML)
}

// MarshalFlagSetJSON writes d as deterministic indented JSON.
func MarshalFlagSetJSON(d *FlagSetDocument) ([]byte, error) {
	doc, err := headedFlagSet(d)
	if err != nil {
		return nil, err
	}

	return marshal(doc, writeJSON)
}

// MarshalFlags writes a flag file as deterministic YAML. The file has no header.
func MarshalFlags(f Flags) ([]byte, error) {
	if f == nil {
		return nil, ErrNilDocument
	}

	return marshal(f, writeYAML)
}

// MarshalFlagsJSON writes a flag file as deterministic indented JSON.
func MarshalFlagsJSON(f Flags) ([]byte, error) {
	if f == nil {
		return nil, ErrNilDocument
	}

	return marshal(f, writeJSON)
}

func marshal(value any, write func(*yaml.Node) ([]byte, error)) ([]byte, error) {
	root, err := encoder{}.encode("", reflect.ValueOf(value))
	if err != nil {
		return nil, err
	}

	return write(root)
}

// headedManifest returns a copy of m with the header filled in.
func headedManifest(m *Manifest) (*Manifest, error) {
	if m == nil {
		return nil, ErrNilDocument
	}

	manifest := *m

	err := fillHeader(&manifest.APIVersion, &manifest.Kind, DocumentKindManifest)
	if err != nil {
		return nil, err
	}

	return &manifest, nil
}

// headedFlagSet returns a copy of d with the header filled in.
func headedFlagSet(d *FlagSetDocument) (*FlagSetDocument, error) {
	if d == nil {
		return nil, ErrNilDocument
	}

	doc := *d

	err := fillHeader(&doc.APIVersion, &doc.Kind, DocumentKindFlagSet)
	if err != nil {
		return nil, err
	}

	return &doc, nil
}

func fillHeader(apiVersion, kind *string, wantKind string) error {
	*apiVersion = orDefault(*apiVersion, APIVersion)
	*kind = orDefault(*kind, wantKind)

	return checkHeader(*apiVersion, *kind, wantKind)
}

// orDefault returns the default for an empty header field: a struct that is not marshalled yet has none.
func orDefault(value, def string) string {
	if value == "" {
		return def
	}

	return value
}
