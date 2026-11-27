package bootstrap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
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
		reflect.TypeFor[SQSConnection](), reflect.TypeFor[RedshiftConnection](), reflect.TypeFor[FileConnection](),
	} {
		member := r.ReflectFromType(t)
		maps.Copy(root.Definitions, member.Definitions)
	}

	root.Title = "Semaphore relay proxy bootstrap file, version 2"

	restrictNamedMaps(root)
	setDurationDefaults(root)
	allowEmptyEnums(root)
	requireByKind(root)
	requireNonBlank(root)

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

// requireByKind adds the keys that the loader requires, and forbids the keys it does not allow, for a kind, as
// if/then conditions on the discriminator. The key lists of notifiers and credentials come from the validator.
func requireByKind(root *jsonschema.Schema) {
	exporters := map[string][]string{
		ExporterFile:          {"outputDir"},
		ExporterWebhook:       {keyEndpointURL},
		ExporterS3:            {keyConnection},
		ExporterKinesis:       {keyConnection},
		ExporterGoogleStorage: {"bucket"},
		ExporterSQS:           {keyConnection},
		ExporterKafka: {
			keyKafka,
		},
		ExporterPubSub:   {keyProjectID, "topic"},
		ExporterBigQuery: {keyProjectID, "datasetId"},
		ExporterRedshift: {keyConnection, "database"},
		ExporterAzure:    {"container", "accountName"},
		ExporterLog:      nil,
		ExporterOTel:     nil,
	}

	exporterDef := root.Definitions["Exporter"]

	for _, kind := range sortedKeys(exporters) {
		spec, _ := exporterKind(kind)
		addKindCondition(exporterDef, keyKind, kind, exporters[kind], spec.allowed, commonExporterKeys())
	}

	addRedshiftExclusions(exporterDef)

	notifierDef := root.Definitions["Notifier"]

	for _, kind := range []string{NotifierSlack, NotifierMicrosoftTeams, NotifierDiscord, NotifierWebhook} {
		allowed, required, _ := notifierKeys(kind)
		addKindCondition(notifierDef, keyKind, kind, required, allowed, []string{keyKind, keyMeta})
	}

	credentialsDef := root.Definitions["Credentials"]

	for _, kind := range []string{CredentialsStatic, CredentialsAssumeRole, CredentialsWebIdentity} {
		allowed, required, _ := credentialKeys(kind)
		addKindCondition(credentialsDef, keyType, kind, required, allowed, []string{keyType})
	}

	// an absent, empty or chain type means chain: none of the keys of the other types is allowed
	props := jsonschema.NewProperties()
	props.Set(
		keyType,
		&jsonschema.Schema{Enum: []any{CredentialsStatic, CredentialsAssumeRole, CredentialsWebIdentity}},
	)

	credentialsDef.AllOf = append(credentialsDef.AllOf, &jsonschema.Schema{
		If:   &jsonschema.Schema{Not: &jsonschema.Schema{Properties: props, Required: []string{keyType}}},
		Then: forbidden(credentialsDef, []string{keyType}),
	})
}

// addKindCondition adds: when the discriminator is the kind, the required keys are present and the keys that are
// neither allowed nor common are absent.
func addKindCondition(def *jsonschema.Schema, discriminator, kind string, required, allowed, common []string) {
	props := jsonschema.NewProperties()
	props.Set(discriminator, &jsonschema.Schema{Const: kind})

	then := forbidden(def, append(slices.Clone(allowed), common...))
	then.Required = required

	def.AllOf = append(def.AllOf, &jsonschema.Schema{
		If:   &jsonschema.Schema{Properties: props, Required: []string{discriminator}},
		Then: then,
	})
}

// forbidden is a schema that rejects every property of the definition that is not in the allowed list.
func forbidden(def *jsonschema.Schema, allowed []string) *jsonschema.Schema {
	props := jsonschema.NewProperties()

	for pair := def.Properties.Oldest(); pair != nil; pair = pair.Next() {
		if !slices.Contains(allowed, pair.Key) {
			props.Set(pair.Key, jsonschema.FalseSchema)
		}
	}

	return &jsonschema.Schema{Properties: props}
}

// setDurationDefaults sets the defaults of the Duration settings. A property that is a reference keeps no tag
// keyword, so they are set here.
func setDurationDefaults(root *jsonschema.Schema) {
	defaults := map[[2]string]time.Duration{
		{"Manifest", "pollInterval"}: DefaultPollInterval, {"Poll", "interval"}: DefaultPollInterval,
		{"Timeouts", "connect"}: DefaultConnectTimeout, {"Timeouts", "request"}: DefaultRequestTimeout,
		{"Retry", "maxBackoff"}: DefaultMaxBackoff, {"Exporter", "flushInterval"}: DefaultFlushInterval,
	}

	if prop, ok := root.Definitions["Manifest"].Properties.Get("pollInterval"); ok {
		prop.Description = "The loader also requires at least 1s; the schema cannot express that on a duration string."
	}

	for key, value := range defaults {
		if prop, ok := root.Definitions[key[0]].Properties.Get(key[1]); ok {
			prop.Default = Duration(value).String()
		}
	}
}

const kafkaProducerDef = "KafkaProducer"

// allowEmptyEnums lets the enumerated settings be written as an empty string: the loader treats an empty value as
// unset and applies the default. The discriminators (type, kind) stay strict, they have no default.
func allowEmptyEnums(root *jsonschema.Schema) {
	for _, key := range [][2]string{
		{"Listener", "role"}, {"Logging", "level"}, {"Logging", keyFormat}, {"Tracing", "sampler"},
		{"Credentials", keyType}, {defS3Connection, "requestChecksumCalculation"},
		{defS3Connection, "responseChecksumValidation"}, {"Exporter", keyFormat},
		{"KafkaSASL", "mechanism"}, {"KafkaGSSAPI", "authType"},
		{kafkaProducerDef, "requiredAcks"}, {kafkaProducerDef, "compression"}, {kafkaProducerDef, "partitioner"},
	} {
		if prop, ok := root.Definitions[key[0]].Properties.Get(key[1]); ok {
			prop.Enum = append(prop.Enum, "")
		}
	}
}

// setNonEmpty is a schema that requires the properties to be present and not empty. The loader treats an empty
// string as unset, so "set" means present and not empty.
func setNonEmpty(names ...string) *jsonschema.Schema {
	props := jsonschema.NewProperties()

	for _, name := range names {
		props.Set(name, &jsonschema.Schema{MinLength: new(uint64(1))})
	}

	return &jsonschema.Schema{Properties: props, Required: names}
}

// addRedshiftExclusions adds the loader rules of the redshift exporter that tie keys together: exactly one of
// clusterIdentifier and workgroupName, dbUser not with workgroupName, dbUser not with secretArn.
func addRedshiftExclusions(def *jsonschema.Schema) {
	props := jsonschema.NewProperties()
	props.Set(keyKind, &jsonschema.Schema{Const: ExporterRedshift})

	def.AllOf = append(def.AllOf, &jsonschema.Schema{
		If: &jsonschema.Schema{Properties: props, Required: []string{keyKind}},
		Then: &jsonschema.Schema{
			AnyOf: []*jsonschema.Schema{setNonEmpty("clusterIdentifier"), setNonEmpty("workgroupName")},
			AllOf: []*jsonschema.Schema{
				{Not: setNonEmpty("clusterIdentifier", "workgroupName")},
				{Not: setNonEmpty("dbUser", "workgroupName")},
				{Not: setNonEmpty("dbUser", "secretArn")},
			},
		},
	})
}

// requireNonBlank makes the settings that the loader requires (and refuses blank) required in the schema: the
// region of an S3 connection and the path of a file connection.
func requireNonBlank(root *jsonschema.Schema) {
	for _, req := range [][2]string{{defS3Connection, "region"}, {"FileConnection", keyPath}} {
		def := root.Definitions[req[0]]
		def.Required = append(def.Required, req[1])

		if prop, ok := def.Properties.Get(req[1]); ok {
			prop.Pattern = `\S`
		}
	}
}
