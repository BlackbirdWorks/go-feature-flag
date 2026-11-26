package bootstrap_test

import (
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

const envRef = "{fromEnv: X}"

// yamlKeys lists the yaml keys of a struct, with a sample value and an empty value of the type of each.
type keyCase struct {
	name   string
	value  string
	empty  string
	hasEmp bool
}

func keyCases(t *testing.T, typ reflect.Type) []keyCase {
	t.Helper()

	samples := map[string]string{
		"format": "csv", "queueUrl": "https://q.example/q", "kafka": "{topic: t, addresses: [\"h:1\"]}",
		"meta": "{a: b}", "headers": "{A: {fromEnv: X}}", "maxEventInMemory": "5", "flushInterval": "10s",
		"autoMigrate": "true", endpointURLKey: envRef, "secret": envRef, webhookURLKey: envRef,
		"googleCredentials": envRef, "accountKey": envRef, "accessKeyId": envRef,
		"secretAccessKey": envRef, "sessionToken": envRef, "externalId": envRef,
		"tableName": "tbl",
	}

	var out []keyCase

	for field := range typ.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}

		sample, found := samples[name]
		if !found {
			sample = "x"
		}

		entry := keyCase{name: name, value: sample}

		switch {
		case field.Type.Kind() == reflect.String:
			entry.empty, entry.hasEmp = `""`, true
		case field.Type == reflect.TypeFor[bootstrap.Secret]():
			entry.empty, entry.hasEmp = `""`, true
		case field.Type.Kind() == reflect.Map:
			entry.empty, entry.hasEmp = "{}", true
		}

		out = append(out, entry)
	}

	return out
}

// agreeOn parses the document with the loader and checks it against the schema, and asserts they agree.
func agreeOn(t *testing.T, checker *schemaChecker, root schemaObject, doc string) bool {
	t.Helper()

	var value any

	require.NoError(t, yaml.Unmarshal([]byte(doc), &value))

	_, err := bootstrap.Parse([]byte(doc), nil, options(nil, nil, false))
	violations := checker.check("", root, value)

	assert.Equal(t, err == nil, len(violations) == 0, "loader error: %v\nschema: %v\n%s", err, violations, doc)

	return err == nil
}

func TestSchemaAndLoaderAgreeOnKeysOfEveryKind(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../.schema/bootstrap-schema.json")
	require.NoError(t, err)

	checker, root, err := newSchemaChecker(raw)
	require.NoError(t, err)

	exporters := map[string]string{
		bootstrap.ExporterFile:          "outputDir: /tmp",
		bootstrap.ExporterWebhook:       "endpointUrl: {fromEnv: U}",
		bootstrap.ExporterLog:           "logFormat: x",
		bootstrap.ExporterS3:            "connection: s",
		bootstrap.ExporterKinesis:       "connection: ks",
		bootstrap.ExporterGoogleStorage: "bucket: b",
		bootstrap.ExporterSQS:           "connection: qq",
		bootstrap.ExporterKafka:         "kafka: {topic: t, addresses: [\"h:1\"]}",
		bootstrap.ExporterPubSub:        "projectId: p\n    topic: t",
		bootstrap.ExporterBigQuery:      "projectId: p\n    datasetId: d",
		bootstrap.ExporterRedshift:      "connection: w\n    database: d\n    clusterIdentifier: c",
		bootstrap.ExporterAzure:         "container: c\n    accountName: a",
		bootstrap.ExporterOTel:          "tracerName: t",
	}

	keys := keyCases(t, reflect.TypeFor[bootstrap.Exporter]())

	for _, kind := range slices.Sorted(maps.Keys(exporters)) {
		t.Run("exporter "+kind, func(t *testing.T) {
			t.Parallel()

			base := kindConns + "exporters:\n  e:\n    kind: " + kind + "\n    " + exporters[kind] + "\n"
			require.True(t, agreeOn(t, checker, root, base), "the base document is valid")

			rejected := 0

			for _, key := range keys {
				// the exclusive pairs of Redshift (cluster or workgroup) are loader rules, not schema rules
				if key.name == "kind" || strings.Contains(exporters[kind], key.name+":") ||
					(kind == bootstrap.ExporterRedshift && key.name == "workgroupName") {
					continue
				}

				for _, value := range []string{key.value, key.empty} {
					if value == "" {
						continue
					}

					if !agreeOn(t, checker, root, base+"    "+key.name+": "+value+"\n") {
						rejected++
					}
				}
			}

			assert.Positive(t, rejected, "some keys are not allowed for the kind")
		})
	}
}

func TestSchemaAndLoaderAgreeOnKeysOfEveryNotifier(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../.schema/bootstrap-schema.json")
	require.NoError(t, err)

	checker, root, err := newSchemaChecker(raw)
	require.NoError(t, err)

	notifiers := map[string]string{
		bootstrap.NotifierSlack: webhookURLKey, bootstrap.NotifierMicrosoftTeams: webhookURLKey,
		bootstrap.NotifierDiscord: webhookURLKey, bootstrap.NotifierWebhook: endpointURLKey,
	}

	keys := keyCases(t, reflect.TypeFor[bootstrap.Notifier]())

	for kind, required := range notifiers {
		t.Run("notifier "+kind, func(t *testing.T) {
			t.Parallel()

			base := "version: 2\nnotifiers:\n  n:\n    kind: " + kind + "\n    " + required + ": {fromEnv: U}\n"
			require.True(t, agreeOn(t, checker, root, base))

			rejected := 0

			for _, key := range keys {
				if key.name == "kind" || key.name == required {
					continue
				}

				for _, value := range []string{key.value, key.empty} {
					if value != "" && !agreeOn(t, checker, root, base+"    "+key.name+": "+value+"\n") {
						rejected++
					}
				}
			}

			assert.Positive(t, rejected)
		})
	}
}

func TestSchemaAndLoaderAgreeOnKeysOfEveryCredentialsType(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../.schema/bootstrap-schema.json")
	require.NoError(t, err)

	checker, root, err := newSchemaChecker(raw)
	require.NoError(t, err)

	// the type written (empty text means the key is absent) and the keys that make the base valid
	types := map[string]struct{ typeLine, base string }{
		"absent":                         {"", ""},
		"empty":                          {`type: ""`, ""},
		bootstrap.CredentialsChain:       {"type: chain", ""},
		bootstrap.CredentialsStatic:      {"type: static", "accessKeyId: {fromEnv: A}, secretAccessKey: {fromEnv: B}"},
		bootstrap.CredentialsAssumeRole:  {"type: assumeRole", "roleArn: arn"},
		bootstrap.CredentialsWebIdentity: {"type: webIdentity", "roleArn: arn, tokenFile: /t"},
	}

	keys := keyCases(t, reflect.TypeFor[bootstrap.Credentials]())

	for name, spec := range types {
		t.Run("credentials "+name, func(t *testing.T) {
			t.Parallel()

			entries := slices.DeleteFunc([]string{spec.typeLine, spec.base}, func(s string) bool { return s == "" })

			build := func(extra string) string {
				all := slices.Clone(entries)
				if extra != "" {
					all = append(all, extra)
				}

				return "version: 2\nconnections:\n  a: {type: s3, credentials: {" + strings.Join(all, ", ") + "}}\n"
			}

			require.True(t, agreeOn(t, checker, root, build("")), "the base document is valid")

			rejected := 0

			for _, key := range keys {
				if key.name == typeKey || strings.Contains(spec.base, key.name+":") {
					continue
				}

				for _, value := range []string{key.value, key.empty} {
					if value != "" && !agreeOn(t, checker, root, build(key.name+": "+value)) {
						rejected++
					}
				}
			}

			assert.Positive(t, rejected)
		})
	}
}
