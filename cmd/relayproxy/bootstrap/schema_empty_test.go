package bootstrap_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// emptyValueCase writes an empty string for one enumerated setting. The loader treats an empty value as unset and
// applies the default, except for the discriminators, which have no default.
type emptyValueCase struct {
	// got reads the effective value from the loaded configuration, for the cases that are accepted
	got         func(*bootstrap.Config) string
	setting     string
	doc         string
	wantDefault string
	wantAccept  bool
}

// emptyValueCases is the one list of the enumerated settings: it is the source of the paired loader and schema
// cases, and of the completeness guard below. The defaults are the documented ones, written out here.
func emptyValueCases() []emptyValueCase {
	creds := func(cfg *bootstrap.Config) string { return cfg.Connections["a"].S3.Credentials.Type }

	return []emptyValueCase{
		{
			setting: "Listener.role", wantAccept: true, wantDefault: "api",
			doc: "server: {listeners: [{name: api, address: \":1\", role: \"\"}]}",
			got: func(cfg *bootstrap.Config) string { return cfg.Server.Listeners[0].Role },
		},
		{
			setting: "Logging.level", wantAccept: true, wantDefault: "info",
			doc: "observability: {logging: {level: \"\"}}",
			got: func(cfg *bootstrap.Config) string { return cfg.Observability.Logging.Level },
		},
		{
			setting: "Logging.format", wantAccept: true, wantDefault: "json",
			doc: "observability: {logging: {format: \"\"}}",
			got: func(cfg *bootstrap.Config) string { return cfg.Observability.Logging.Format },
		},
		{
			setting: "Tracing.sampler", wantAccept: true, wantDefault: "parentbased_always_off",
			doc: "observability: {tracing: {sampler: \"\"}}",
			got: func(cfg *bootstrap.Config) string { return cfg.Observability.Tracing.Sampler },
		},
		{
			setting: "Credentials.type", wantAccept: true, wantDefault: "chain",
			doc: "connections: {a: {type: s3, credentials: {type: \"\"}}}", got: creds,
		},
		{
			setting: "S3Connection.requestChecksumCalculation", wantAccept: true, wantDefault: "whenSupported",
			doc: "connections: {a: {type: s3, requestChecksumCalculation: \"\"}}",
			got: func(cfg *bootstrap.Config) string { return cfg.Connections["a"].S3.RequestChecksumCalculation },
		},
		{
			setting: "S3Connection.responseChecksumValidation", wantAccept: true, wantDefault: "whenSupported",
			doc: "connections: {a: {type: s3, responseChecksumValidation: \"\"}}",
			got: func(cfg *bootstrap.Config) string { return cfg.Connections["a"].S3.ResponseChecksumValidation },
		},
		{
			setting: "Exporter.format", wantAccept: true, wantDefault: "json",
			doc: "exporters: {a: {kind: file, outputDir: /tmp, format: \"\"}}",
			got: func(cfg *bootstrap.Config) string { return cfg.Exporters["a"].Format },
		},
		{
			setting: "KafkaSASL.mechanism", wantAccept: true, wantDefault: "",
			doc: kafkaDoc("net: {sasl: {mechanism: \"\"}}"),
			got: func(cfg *bootstrap.Config) string { return cfg.Exporters["a"].Kafka.Net.SASL.Mechanism },
		},
		{
			setting: "KafkaGSSAPI.authType", wantAccept: true, wantDefault: "",
			doc: kafkaDoc("net: {sasl: {gssapi: {authType: \"\"}}}"),
			got: func(cfg *bootstrap.Config) string { return cfg.Exporters["a"].Kafka.Net.SASL.GSSAPI.AuthType },
		},
		{
			setting: "KafkaProducer.requiredAcks", wantAccept: true, wantDefault: "",
			doc: kafkaDoc("producer: {requiredAcks: \"\"}"),
			got: func(cfg *bootstrap.Config) string { return cfg.Exporters["a"].Kafka.Producer.RequiredAcks },
		},
		{
			setting: "KafkaProducer.compression", wantAccept: true, wantDefault: "",
			doc: kafkaDoc("producer: {compression: \"\"}"),
			got: func(cfg *bootstrap.Config) string { return cfg.Exporters["a"].Kafka.Producer.Compression },
		},
		{
			setting: "KafkaProducer.partitioner", wantAccept: true, wantDefault: "",
			doc: kafkaDoc("producer: {partitioner: \"\"}"),
			got: func(cfg *bootstrap.Config) string { return cfg.Exporters["a"].Kafka.Producer.Partitioner },
		},
		{setting: "S3Connection.type", doc: "connections: {a: {type: \"\"}}"},
		{setting: "KinesisConnection.type", doc: "connections: {a: {type: \"\", stream: s}}"},
		{setting: "SQSConnection.type", doc: "connections: {a: {type: \"\", queueUrl: \"https://q.example/q\"}}"},
		{setting: "RedshiftConnection.type", doc: "connections: {a: {type: \"\", region: us-east-1}}"},
		{setting: "Config.version", doc: "version: \"\""},
		{setting: "Exporter.kind", doc: "exporters: {a: {kind: \"\"}}"},
		{setting: "Notifier.kind", doc: "notifiers: {a: {kind: \"\"}}"},
		{
			setting: "Listener.protocols[]",
			doc:     "server: {listeners: [{name: api, address: \":1\", protocols: [\"\"]}]}",
		},
	}
}

// kafkaDoc is a document with a kafka exporter and the given extra settings of its kafka block.
func kafkaDoc(extra string) string {
	return "exporters: {a: {kind: kafka, kafka: {topic: t, addresses: [\"h:1\"], " + extra + "}}}"
}

func TestEmptyEnumValues(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../.schema/bootstrap-schema.json")
	require.NoError(t, err)

	checker, root, err := newSchemaChecker(raw)
	require.NoError(t, err)

	for _, tcase := range emptyValueCases() {
		t.Run(tcase.setting, func(t *testing.T) {
			t.Parallel()

			doc := "version: 2\n" + tcase.doc + "\n"
			if strings.HasPrefix(tcase.doc, "version:") {
				doc = tcase.doc + "\n"
			}

			var value any

			require.NoError(t, yaml.Unmarshal([]byte(doc), &value))

			cfg, parseErr := bootstrap.Parse([]byte(doc), nil, options(nil, nil, false))

			assert.Equal(t, tcase.wantAccept, parseErr == nil, "the loader: %v", parseErr)
			assert.Equal(t, tcase.wantAccept, len(checker.check("", root, value)) == 0, "the schema")

			if tcase.wantAccept {
				require.NoError(t, parseErr)
				assert.Equal(t, tcase.wantDefault, tcase.got(cfg), "the effective default")
			}
		})
	}
}

// TestEveryEnumSettingHasAnEmptyValueCase finds every enumerated property of the schema and checks that the list
// of cases covers it: a property whose enum accepts the empty string needs an accepted case, and any other
// needs a rejected one.
func TestEveryEnumSettingHasAnEmptyValueCase(t *testing.T) {
	t.Parallel()

	raw, err := bootstrap.JSONSchema()
	require.NoError(t, err)

	var doc struct {
		Defs map[string]struct {
			Properties map[string]struct {
				Items struct {
					Enum []any `json:"enum"`
				} `json:"items"`
				Enum []any `json:"enum"`
			} `json:"properties"`
		} `json:"$defs"`
	}

	require.NoError(t, json.Unmarshal(raw, &doc))

	acceptsEmpty, rejectsEmpty := map[string]bool{}, map[string]bool{}

	for def, body := range doc.Defs {
		for property, schema := range body.Properties {
			// the enum of a property, or the enum of the items of a list: "[]" marks the second
			for suffix, enum := range map[string][]any{"": schema.Enum, "[]": schema.Items.Enum} {
				if len(enum) == 0 {
					continue
				}

				key := fmt.Sprintf("%s.%s%s", def, property, suffix)
				if slices.Contains(enum, any("")) {
					acceptsEmpty[key] = true
				} else {
					rejectsEmpty[key] = true
				}
			}
		}
	}

	wantAccept, wantReject := map[string]bool{}, map[string]bool{}

	for _, tcase := range emptyValueCases() {
		if tcase.wantAccept {
			wantAccept[tcase.setting] = true
		} else {
			wantReject[tcase.setting] = true
		}
	}

	assert.ElementsMatch(t, slices.Sorted(maps.Keys(wantAccept)), slices.Sorted(maps.Keys(acceptsEmpty)))
	assert.ElementsMatch(t, slices.Sorted(maps.Keys(wantReject)), slices.Sorted(maps.Keys(rejectsEmpty)))
}
