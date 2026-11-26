package controlplane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const yamlIndent = 2

// MarshalManifest writes m as deterministic YAML. An empty APIVersion or Kind is filled in; any other value
// than the current one is an error. m is not changed and not validated.
func MarshalManifest(m *Manifest) ([]byte, error) {
	v, err := headedManifest(m)
	if err != nil {
		return nil, err
	}

	return marshalYAML(v)
}

// MarshalManifestJSON writes m as deterministic indented JSON.
func MarshalManifestJSON(m *Manifest) ([]byte, error) {
	v, err := headedManifest(m)
	if err != nil {
		return nil, err
	}

	return marshalJSON(v)
}

// MarshalFlagSet writes d as deterministic YAML. See MarshalManifest for the header rules.
func MarshalFlagSet(d *FlagSetDocument) ([]byte, error) {
	v, err := headedFlagSet(d)
	if err != nil {
		return nil, err
	}

	return marshalYAML(v)
}

// MarshalFlagSetJSON writes d as deterministic indented JSON.
func MarshalFlagSetJSON(d *FlagSetDocument) ([]byte, error) {
	v, err := headedFlagSet(d)
	if err != nil {
		return nil, err
	}

	return marshalJSON(v)
}

// MarshalFlags writes a flag file as deterministic YAML. The file has no header.
func MarshalFlags(f Flags) ([]byte, error) {
	if f == nil {
		return nil, ErrNilDocument
	}

	return marshalYAML(f)
}

// MarshalFlagsJSON writes a flag file as deterministic indented JSON.
func MarshalFlagsJSON(f Flags) ([]byte, error) {
	if f == nil {
		return nil, ErrNilDocument
	}

	return marshalJSON(f)
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

func marshalJSON(value any) ([]byte, error) {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", strings.Repeat(" ", yamlIndent))

	err := enc.Encode(value)
	if err != nil {
		return nil, fmt.Errorf("controlplane: encode json: %w", err)
	}

	return buf.Bytes(), nil
}

func marshalYAML(value any) ([]byte, error) {
	var root yaml.Node

	err := root.Encode(value)
	if err != nil {
		return nil, fmt.Errorf("controlplane: encode yaml: %w", err)
	}

	quoteAmbiguous(&root)

	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)

	err = enc.Encode(&root)
	if err != nil {
		return nil, fmt.Errorf("controlplane: encode yaml: %w", err)
	}

	err = enc.Close()
	if err != nil {
		return nil, fmt.Errorf("controlplane: encode yaml: %w", err)
	}

	return buf.Bytes(), nil
}

// quoteAmbiguous double quotes every plain string scalar that a YAML 1.1 or 1.2 reader could resolve to
// another type.
func quoteAmbiguous(node *yaml.Node) {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!merge" {
		// The encoder tags the string "<<" as the merge key; it is a plain string here.
		node.Tag = "!!str"
		node.Style = yaml.DoubleQuotedStyle
	}

	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" && node.Style == 0 && ambiguous(node.Value) {
		node.Style = yaml.DoubleQuotedStyle
	}

	for _, child := range node.Content {
		quoteAmbiguous(child)
	}
}

// ambiguous reports whether a plain scalar text could be read as a bool, null, number, timestamp or special
// token by a YAML 1.1 or 1.2 reader. It is deliberately wide: a quoted string is always safe.
func ambiguous(text string) bool {
	if text == "" {
		return true
	}

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

// orDefault returns the default for an empty header field: a struct that is not marshalled yet has none.
func orDefault(value, def string) string {
	if value == "" {
		return def
	}

	return value
}
