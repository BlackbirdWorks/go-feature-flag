package bootstrap_test

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// TestCommittedSchemaIsCurrent fails when .schema/bootstrap-schema.json is stale. Regenerate it with
// go run ./cmd/jsonschema-generator --schema-location .schema/flag-schema.json
// --bootstrap-schema-location .schema/bootstrap-schema.json.
func TestCommittedSchemaIsCurrent(t *testing.T) {
	t.Parallel()

	got, err := bootstrap.JSONSchema()
	require.NoError(t, err)

	want, err := os.ReadFile("../../../.schema/bootstrap-schema.json")
	require.NoError(t, err)

	assert.Equal(t, string(want), string(got), "the committed schema is stale: run the jsonschema-generator")
}

// The description of contextEnrichment lists every kind of value that the loader accepts: a comma in the tag of the
// field must not cut it.
func TestSchemaDescribesTheContextEnrichmentValues(t *testing.T) {
	t.Parallel()

	raw, err := bootstrap.JSONSchema()
	require.NoError(t, err)

	var doc struct {
		Defs map[string]struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		} `json:"$defs"`
	}

	require.NoError(t, json.Unmarshal(raw, &doc))

	description := doc.Defs["Evaluation"].Properties["contextEnrichment"].Description
	kinds := []string{"strings", "integers", "finite numbers", "booleans", "lists", "mappings with string keys"}

	for _, kind := range kinds {
		assert.Contains(t, description, kind)
	}
}

func TestSchemaShape(t *testing.T) {
	t.Parallel()

	raw, err := bootstrap.JSONSchema()
	require.NoError(t, err)

	var doc struct {
		Defs map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
			OneOf      []json.RawMessage          `json:"oneOf"`
		} `json:"$defs"`
	}

	require.NoError(t, json.Unmarshal(raw, &doc))

	tests := []struct {
		name         string
		def          string
		wantProperty string
		wantRequired []string
		wantOneOf    int
	}{
		{name: "root has the version key", def: "Config", wantProperty: "version", wantRequired: []string{"version"}},
		{name: "control plane", def: defMaxManifest, wantProperty: "maxObjects"},
		{name: "connection is a union", def: "Connection", wantOneOf: 5},
		{name: "s3 member", def: "S3Connection", wantProperty: tcBucket, wantRequired: []string{typeKey, "region"}},
		{name: "secret reference", def: "Secret", wantProperty: "fromEnv"},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			def, ok := doc.Defs[tcase.def]
			require.True(t, ok)

			if tcase.wantProperty != "" {
				assert.Contains(t, def.Properties, tcase.wantProperty)
			}

			assert.Len(t, def.OneOf, tcase.wantOneOf)
			assert.Equal(t, tcase.wantRequired, def.Required)
		})
	}
}

func TestExampleFileValidatesAgainstTheSchema(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../.schema/bootstrap-schema.json")
	require.NoError(t, err)

	checker, root, err := newSchemaChecker(raw)
	require.NoError(t, err)

	example, err := os.ReadFile("testdata/bootstrap.yaml")
	require.NoError(t, err)

	var value any

	require.NoError(t, yaml.Unmarshal(example, &value))

	assert.Empty(t, checker.check("", root, value))
}

func TestSchemaRejectsWhatTheLoaderRejects(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../.schema/bootstrap-schema.json")
	require.NoError(t, err)

	checker, root, err := newSchemaChecker(raw)
	require.NoError(t, err)

	tests := []struct {
		name string
		doc  string
		want string
	}{
		{name: "unknown key", doc: "version: 2\nbogus: 1\n", want: ".bogus: not allowed"},
		{name: "version 1", doc: "version: 1\n", want: ".version: not in the enum"},
		{
			name: "role",
			doc:  "version: 2\nserver: {listeners: [{name: a, role: admin}]}\n",
			want: ".role: not in the enum",
		},
		{
			name: "protocol",
			doc:  "version: 2\nserver: {listeners: [{name: a, protocols: [h2]}]}\n",
			want: "protocols[0]: not in the enum",
		},
		{
			name: "log level",
			doc:  "version: 2\nobservability: {logging: {level: loud}}\n",
			want: ".level: not in the enum",
		},
		{
			name: "jitter above one",
			doc:  "version: 2\nconnections: {a: {type: s3, region: r, poll: {jitter: 2}}}\n",
			want: ".jitter: above the maximum",
		},
		{
			name: "attempts",
			doc:  "version: 2\nconnections: {a: {type: s3, region: r, retry: {maxAttempts: 0}}}\n",
			want: ".maxAttempts: below the minimum",
		},
		{
			name: "max objects",
			doc:  "version: 2\ncontrolPlane: {connection: a, manifest: {maxObjects: 0}}\n",
			want: ".maxObjects: below the minimum",
		},
		{
			name: "checksum",
			doc:  "version: 2\nconnections: {a: {type: s3, region: r, requestChecksumCalculation: never}}\n",
			want: "not in the enum",
		},
		{
			name: "connection type",
			doc:  "version: 2\nconnections: {a: {type: valkey}}\n",
			want: "matches 0 of the oneOf",
		},
		{name: "reserved name", doc: "version: 2\nconnections: {default: {type: s3, region: r}}\n", want: "{default}"},
		{name: "bad name", doc: "version: 2\nconnections: {Bad: {type: s3, region: r}}\n", want: "{Bad}"},
		{name: "exporter kind", doc: "version: 2\nexporters: {a: {kind: pigeon}}\n", want: ".kind: not in the enum"},
		{
			name: "exporter format",
			doc:  "version: 2\nexporters: {a: {kind: log, format: xml}}\n",
			want: ".format: not in the enum",
		},
		{
			name: "webhook exporter needs a URL",
			doc:  "version: 2\nexporters: {a: {kind: webhook}}\n",
			want: "missing endpointUrl",
		},
		{
			name: "slack notifier needs a URL",
			doc:  "version: 2\nnotifiers: {a: {kind: slack}}\n",
			want: "missing webhookUrl",
		},
		{
			name: "s3 connection without a region",
			doc:  "version: 2\nconnections: {a: {type: s3}}\n",
			want: "missing region",
		},
		{
			name: "s3 connection with a blank region",
			doc:  "version: 2\nconnections: {a: {type: s3, region: \"  \"}}\n",
			want: ".region: does not match the pattern",
		},
		{
			name: "file connection without a path",
			doc:  "version: 2\nconnections: {a: {type: file}}\n",
			want: "missing path",
		},
		{
			name: "file connection with a blank path",
			doc:  "version: 2\nconnections: {a: {type: file, path: \" \"}}\n",
			want: ".path: does not match the pattern",
		},
		{
			name: "manifest jitter of one",
			doc:  "version: 2\ncontrolPlane: {connection: a, manifest: {key: k, jitter: 1}}\n",
			want: ".jitter: at or above the exclusive maximum",
		},
		{
			name: "static credentials",
			doc:  "version: 2\nconnections: {a: {type: s3, region: r, credentials: {type: static}}}\n",
			want: "missing accessKeyId",
		},
		{
			name: "secret with two references",
			doc:  "version: 2\nauth: {adminKeys: [{fromEnv: A, fromFile: B}]}\n",
			want: "adminKeys[0]: matches 0",
		},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			var value any

			require.NoError(t, yaml.Unmarshal([]byte(tcase.doc), &value))
			assert.Contains(t, strings.Join(checker.check("", root, value), "\n"), tcase.want)
		})
	}
}

func TestSchemaAcceptsFilesThatRelyOnOverrides(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../.schema/bootstrap-schema.json")
	require.NoError(t, err)

	checker, root, err := newSchemaChecker(raw)
	require.NoError(t, err)

	// the manifest key comes from SEMAPHORE_MANIFEST_KEY and the listener address from SEMAPHORE_LISTEN
	file := "version: 2\nconnections: {control: {type: s3, region: r, bucket: b}}\ncontrolPlane: {connection: control}\n" +
		"server: {listeners: [{name: api}]}\n"

	var value any

	require.NoError(t, yaml.Unmarshal([]byte(file), &value))
	assert.Empty(t, checker.check("", root, value))

	env := envMap{bootstrap.EnvManifestKey: "m.json", bootstrap.EnvListen: ":8080"}
	_, err = bootstrap.Parse([]byte(file), nil, options(env, nil, false))
	require.NoError(t, err)
}

func TestSchemaDefaults(t *testing.T) {
	t.Parallel()

	raw, err := bootstrap.JSONSchema()
	require.NoError(t, err)

	var doc struct {
		Defs map[string]struct {
			Properties map[string]struct {
				Default any `json:"default"`
			} `json:"properties"`
		} `json:"$defs"`
	}

	require.NoError(t, json.Unmarshal(raw, &doc))

	tests := []struct {
		want     any
		def      string
		property string
	}{
		{def: "Poll", property: "interval", want: tc60S},
		{def: "Poll", property: "jitter", want: 0.1},
		{def: defMaxManifest, property: "maxObjects", want: float64(20000)},
		{def: defMaxManifest, property: "pollInterval", want: tc60S},
		{def: "Retry", property: "maxAttempts", want: float64(3)},
		{def: "Timeouts", property: "connect", want: "10s"},
		{def: "Logging", property: "level", want: "info"},
		{def: "Metrics", property: "enabled", want: true},
		{def: "Credentials", property: typeKey, want: "chain"},
	}

	for _, tcase := range tests {
		t.Run(tcase.def+"."+tcase.property, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tcase.want, doc.Defs[tcase.def].Properties[tcase.property].Default)
		})
	}
}

func TestSchemaDurationPattern(t *testing.T) {
	t.Parallel()

	raw, err := bootstrap.JSONSchema()
	require.NoError(t, err)

	var doc struct {
		Defs map[string]struct {
			Pattern string `json:"pattern"`
		} `json:"$defs"`
	}

	require.NoError(t, json.Unmarshal(raw, &doc))

	// the pattern is written with a lookahead for "at least one non-zero digit", which the regexp package does
	// not run: split it into the lookahead and the rest
	pattern := doc.Defs["Duration"].Pattern
	require.Contains(t, pattern, "(?=.*[1-9])")

	rest := regexp.MustCompile(strings.Replace(pattern, "(?=.*[1-9])", "", 1))
	nonZero := regexp.MustCompile("[1-9]")

	tests := []struct {
		value      string
		wantAccept bool
	}{
		{tc60S, true}, {"1m30s", true}, {"1.5s", true}, {".5s", true}, {"1h0m", true}, {"0s10s", true}, {"+5s", true},
		{"0s", false}, {"0.0s", false}, {"00s", false}, {"-5s", false}, {"5", false}, {"", false}, {"10x", false},
		{"1d", false},
	}

	for _, tcase := range tests {
		t.Run(tcase.value, func(t *testing.T) {
			t.Parallel()

			inSchema := rest.MatchString(tcase.value) && nonZero.MatchString(tcase.value)

			doc := "version: 2\nconnections:\n  a: {type: s3, region: r, poll: {interval: \"" + tcase.value + "\"}}\n"
			_, parseErr := bootstrap.Parse([]byte(doc), nil, options(nil, nil, false))

			assert.Equal(t, tcase.wantAccept, inSchema, "the schema pattern")
			assert.Equal(t, tcase.wantAccept, parseErr == nil, "the loader")
		})
	}
}
