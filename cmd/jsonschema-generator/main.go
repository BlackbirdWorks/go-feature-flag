package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/invopop/jsonschema"
	"github.com/jessevdk/go-flags"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/model/dto"
)

func main() {
	var opts struct {
		SchemaLocation          string `description:"Location where the schema will be saved."                          long:"schema-location"           required:"true"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped
		BootstrapSchemaLocation string `description:"Location where the bootstrap file schema will be saved, if given." long:"bootstrap-schema-location"`                 //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped
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

	log.Println("Writing bootstrap jsonschema to", opts.BootstrapSchemaLocation)

	err = os.WriteFile(opts.BootstrapSchemaLocation, bootstrapSchema, 0o600)
	if err != nil {
		log.Fatalf("impossible to write jsonschema file to %s: %s", opts.BootstrapSchemaLocation, err)
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
