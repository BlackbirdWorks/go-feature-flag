package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/invopop/jsonschema"
	"github.com/jessevdk/go-flags"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/internal/schemadoc"
	"github.com/blackbirdworks/semaphore/model/dto"
)

func main() {
	var opts struct {
		SchemaLocation          string `description:"Location where the schema will be saved."                                                           long:"schema-location"           required:"true"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped
		ControlPlaneSchemaDir   string `description:"Directory where the schemas of the control-plane documents will be saved, if given."                long:"control-plane-schema-dir"`                  //nolint:lll // struct tags can not be wrapped
		ReferenceLocation       string `description:"Location where the Markdown schema reference will be saved, if given. Needs the two options above." long:"reference-location"`                        //nolint:lll // struct tags can not be wrapped
		BootstrapSchemaLocation string `description:"Location where the bootstrap file schema will be saved, if given."                                  long:"bootstrap-schema-location"`                 //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped
	}

	_, err := flags.Parse(&opts)
	if err != nil {
		log.Fatal("impossible to parse command line parameters", err)
	}

	if opts.SchemaLocation == "" {
		log.Fatal("schema-location is required")
	}

	a := map[string]dto.DTO{}
	d := jsonschema.Reflect(a)

	jsonSchema, err := d.MarshalJSON()
	if err != nil {
		log.Fatal("impossible to parse jsonschema", err)
	}

	prettyJSONSchema, err := PrettyString(string(jsonSchema))
	if err != nil {
		log.Fatal("impossible to prettify jsonschema", err)
	}

	fmt.Println("Writing jsonschema to", opts.SchemaLocation) //nolint:forbidigo // command line output

	err = os.WriteFile(opts.SchemaLocation, prettyJSONSchema, 0o600)
	if err != nil {
		log.Fatalf("impossible to write jsonschema file to %s: %s", opts.SchemaLocation, err)
	}

	if opts.BootstrapSchemaLocation == "" {
		return
	}

	bootstrapSchema, err := bootstrap.JSONSchema()
	if err != nil {
		log.Fatal("impossible to generate the bootstrap jsonschema: ", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	logger.Info("Writing bootstrap jsonschema", slog.String("path", opts.BootstrapSchemaLocation))

	err = os.WriteFile(opts.BootstrapSchemaLocation, bootstrapSchema, 0o600)
	if err != nil {
		log.Fatalf("impossible to write jsonschema file to %s: %s", opts.BootstrapSchemaLocation, err)
	}

	if opts.ControlPlaneSchemaDir == "" {
		return
	}

	err = writeControlPlane(logger, opts.ControlPlaneSchemaDir, opts.ReferenceLocation, bootstrapSchema)
	if err != nil {
		log.Fatal(err)
	}
}

// PrettyString will prettify the JSON string.
func PrettyString(str string) ([]byte, error) {
	var prettyJSON bytes.Buffer

	err := json.Indent(&prettyJSON, []byte(str), "", "    ")
	if err != nil {
		return []byte(""), err
	}

	return prettyJSON.Bytes(), nil
}

// writeControlPlane writes the schemas of the control-plane documents into dir and, when referenceLocation is given,
// the Markdown schema reference.
func writeControlPlane(logger *slog.Logger, dir, referenceLocation string, bootstrapSchema []byte) error {
	controlPlane, err := schemadoc.ControlPlaneSchemas(".")
	if err != nil {
		return fmt.Errorf("generate the control-plane jsonschemas: %w", err)
	}

	for name, schema := range controlPlane {
		path := filepath.Join(dir, name)
		logger.Info("Writing control-plane jsonschema", slog.String("path", path))

		err = os.WriteFile(path, schema, 0o600)
		if err != nil {
			return fmt.Errorf("write jsonschema file to %s: %w", path, err)
		}
	}

	if referenceLocation == "" {
		return nil
	}

	reference, err := schemadoc.Render(schemadoc.Documents(bootstrapSchema, controlPlane))
	if err != nil {
		return fmt.Errorf("render the schema reference: %w", err)
	}

	logger.Info("Writing schema reference", slog.String("path", referenceLocation))

	err = os.WriteFile(referenceLocation, reference, 0o600)
	if err != nil {
		return fmt.Errorf("write the schema reference to %s: %w", referenceLocation, err)
	}

	return nil
}
