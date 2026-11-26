package bootstrap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"

	"github.com/invopop/jsonschema"
)

// JSONSchema returns the JSON Schema of the bootstrap file, generated from the Go types, indented with 4 spaces
// like the other schema of the repository.
func JSONSchema() ([]byte, error) {
	r := &jsonschema.Reflector{
		FieldNameTag: "json",
	}

	root := r.Reflect(&Config{})

	for _, t := range []reflect.Type{
		reflect.TypeFor[S3Connection](), reflect.TypeFor[KinesisConnection](),
		reflect.TypeFor[SQSConnection](), reflect.TypeFor[RedshiftConnection](),
	} {
		member := r.ReflectFromType(t)
		maps.Copy(root.Definitions, member.Definitions)
	}

	root.Title = "Semaphore relay proxy bootstrap file, version 2"

	raw, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("marshal bootstrap schema: %w", err)
	}

	var out bytes.Buffer

	err = json.Indent(&out, raw, "", "    ")
	if err != nil {
		return nil, fmt.Errorf("indent bootstrap schema: %w", err)
	}

	out.WriteByte('\n')

	return out.Bytes(), nil
}
