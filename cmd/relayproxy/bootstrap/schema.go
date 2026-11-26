package bootstrap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"time"

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

	restrictNamedMaps(root)
	setDurationDefaults(root)
	allowEmptyEnums(root)
	requireByKind(root)

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

// restrictNamedMaps limits the names of the connections, exporters and notifiers like the loader does.
func restrictNamedMaps(root *jsonschema.Schema) {
	config := root.Definitions["Config"]

	if name, ok := root.Definitions["Listener"].Properties.Get("name"); ok {
		name.Pattern = nameRE.String()
	}

	for _, key := range []string{"connections", "exporters", "notifiers"} {
		prop, ok := config.Properties.Get(key)
		if !ok {
			continue
		}

		prop.PropertyNames = &jsonschema.Schema{
			Pattern: nameRE.String(),
			Not:     &jsonschema.Schema{Const: "default"},
		}
	}
}

// requireByKind adds the keys that the loader requires for a kind, as if/then conditions on the discriminator.
func requireByKind(root *jsonschema.Schema) {
	exporters := map[string][]string{
		ExporterFile: {"outputDir"}, ExporterWebhook: {keyEndpointURL}, ExporterS3: {keyConnection},
		ExporterKinesis: {keyConnection}, ExporterGoogleStorage: {"bucket"}, ExporterSQS: {keyConnection},
		ExporterKafka: {keyKafka}, ExporterPubSub: {"projectId", "topic"}, ExporterBigQuery: {"projectId", "datasetId"},
		ExporterRedshift: {keyConnection, "database"}, ExporterAzure: {"container", "accountName"},
	}
	notifiers := map[string][]string{
		NotifierSlack: {keyWebhookURL}, NotifierMicrosoftTeams: {keyWebhookURL}, NotifierDiscord: {keyWebhookURL},
		NotifierWebhook: {keyEndpointURL},
	}
	credentials := map[string][]string{
		CredentialsStatic: {keyAccessKeyID, keySecretKey}, CredentialsAssumeRole: {keyRoleARN},
		CredentialsWebIdentity: {keyRoleARN, keyTokenFile},
	}

	addKindConditions(root.Definitions["Exporter"], "kind", exporters)
	addKindConditions(root.Definitions["Notifier"], "kind", notifiers)
	addKindConditions(root.Definitions["Credentials"], "type", credentials)
}

func addKindConditions(def *jsonschema.Schema, discriminator string, required map[string][]string) {
	for _, kind := range sortedKeys(required) {
		props := jsonschema.NewProperties()
		props.Set(discriminator, &jsonschema.Schema{Const: kind})

		def.AllOf = append(def.AllOf, &jsonschema.Schema{
			If:   &jsonschema.Schema{Properties: props, Required: []string{discriminator}},
			Then: &jsonschema.Schema{Required: required[kind]},
		})
	}
}

// setDurationDefaults sets the defaults of the Duration settings. A property that is a reference keeps no tag
// keyword, so they are set here.
func setDurationDefaults(root *jsonschema.Schema) {
	defaults := map[[2]string]time.Duration{
		{"Manifest", "pollInterval"}: DefaultPollInterval, {"Poll", "interval"}: DefaultPollInterval,
		{"Timeouts", "connect"}: DefaultConnectTimeout, {"Timeouts", "request"}: DefaultRequestTimeout,
		{"Retry", "maxBackoff"}: DefaultMaxBackoff, {"Exporter", "flushInterval"}: DefaultFlushInterval,
	}

	for key, value := range defaults {
		if prop, ok := root.Definitions[key[0]].Properties.Get(key[1]); ok {
			prop.Default = Duration(value).String()
		}
	}
}

// allowEmptyEnums lets the enumerated settings be written as an empty string: the loader treats an empty value as
// unset and applies the default. The discriminators (type, kind) stay strict, they have no default.
func allowEmptyEnums(root *jsonschema.Schema) {
	for _, key := range [][2]string{
		{"Listener", "role"}, {"Logging", "level"}, {"Logging", "format"}, {"Tracing", "sampler"},
		{"Credentials", "type"}, {"S3Connection", "requestChecksumCalculation"},
		{"S3Connection", "responseChecksumValidation"}, {"Exporter", "format"},
	} {
		if prop, ok := root.Definitions[key[0]].Properties.Get(key[1]); ok {
			prop.Enum = append(prop.Enum, "")
		}
	}
}
