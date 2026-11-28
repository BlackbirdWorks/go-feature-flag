package schemadoc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/blackbirdworks/semaphore/controlplane"
)

const modulePath = "github.com/blackbirdworks/semaphore"

// Control-plane schema files, in the order of the reference page.
const (
	ManifestSchemaFile = "manifest-schema.json"
	FlagSetSchemaFile  = "flagset-schema.json"
	SegmentsSchemaFile = "segments-schema.json"
)

// ControlPlaneSchemas returns the JSON Schemas of the control-plane documents, by file name. They are generated from
// the Go types of the controlplane package and the comments of their fields. root is the relative path from the
// working directory to the root of the repository.
func ControlPlaneSchemas(root string) (map[string][]byte, error) {
	reflector := &jsonschema.Reflector{FieldNameTag: "json"}
	// The comment map is keyed by the import path joined with the directory as given, so the base climbs as many
	// levels as the root does.
	base := modulePath + strings.Repeat("/_", strings.Count(filepath.ToSlash(root), ".."))

	err := reflector.AddGoComments(base, filepath.Join(root, "controlplane"))
	if err != nil {
		return nil, fmt.Errorf("read the comments of the controlplane package: %w", err)
	}

	out := map[string][]byte{}

	for file, spec := range map[string]struct {
		value any
		title string
	}{
		ManifestSchemaFile: {&controlplane.Manifest{}, "Semaphore manifest"},
		FlagSetSchemaFile:  {&controlplane.FlagSetDocument{}, "Semaphore flag-set document"},
		SegmentsSchemaFile: {&controlplane.SegmentsDocument{}, "Semaphore segments document"},
	} {
		schema := reflector.Reflect(spec.value)
		schema.Title = spec.title

		raw, marshalErr := json.Marshal(schema)
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal %s: %w", reflect.TypeOf(spec.value), marshalErr)
		}

		var indented bytes.Buffer

		err = json.Indent(&indented, raw, "", "    ")
		if err != nil {
			return nil, fmt.Errorf("indent %s: %w", file, err)
		}

		indented.WriteByte('\n')

		out[file] = indented.Bytes()
	}

	return out, nil
}

// Documents returns the schemas of the reference page: the bootstrap file and the control-plane documents.
func Documents(bootstrapSchema []byte, controlPlane map[string][]byte) []Document {
	return []Document{
		{
			Title:  "Bootstrap file",
			Intro:  "The file of the relay proxy, `version: 2`. File: `.schema/bootstrap-schema.json`.",
			Schema: bootstrapSchema,
		},
		{
			Title: "Manifest",
			Intro: "Document of kind `Manifest`, written last by `semaphore-cli publish`. " +
				"File: `.schema/manifest-schema.json`.",
			Schema: controlPlane[ManifestSchemaFile],
		},
		{
			Title:  "Flag-set document",
			Intro:  "Document of kind `FlagSet`. File: `.schema/flagset-schema.json`.",
			Schema: controlPlane[FlagSetSchemaFile],
		},
		{
			Title:  "Segments document",
			Intro:  "Document of kind `Segments`. File: `.schema/segments-schema.json`.",
			Schema: controlPlane[SegmentsSchemaFile],
		},
	}
}
