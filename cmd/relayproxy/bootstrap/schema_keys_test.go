package bootstrap_test

import (
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
		"format": "csv", "queueUrl": "https://q.example/q", tcKafka: "{topic: t, addresses: [\"h:1\"]}",
		tcMeta: "{a: b}", tcHeaders: "{A: {fromEnv: X}}", "maxEventInMemory": "5", "flushInterval": "10s",
		"autoMigrate": "true", endpointURLKey: envRef, tcSecret: envRef, webhookURLKey: envRef,
		tcGooglecredentials: envRef, tcAccountkey: envRef, tcAccesskeyid: envRef,
		tcSecretaccesskey: envRef, tcSessiontoken: envRef, tcExternalid: envRef,
		tcTablename: "tbl",
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

// verdicts parses the document with the loader and checks it against the schema.
func verdicts(t *testing.T, checker *schemaChecker, root schemaObject, doc string) (bool, bool) {
	t.Helper()

	var value any

	require.NoError(t, yaml.Unmarshal([]byte(doc), &value))

	_, err := bootstrap.Parse([]byte(doc), nil, options(nil, nil, false))

	return err == nil, len(checker.check("", root, value)) == 0
}

// secretFields are the settings that are secrets: an empty value is not accepted for them.
func secretFields() []string {
	return []string{
		endpointURLKey, tcSecret, webhookURLKey, tcGooglecredentials, tcAccountkey, tcAccesskeyid, tcSecretaccesskey,
		tcSessiontoken, tcExternalid,
	}
}

// keysSpec is one table case: a base document that is valid, the keys documented as allowed for it besides the
// ones already in the base, and a builder of the document with one more key.
type keysSpec struct {
	build   func(extra string) string
	skip    []string
	allowed []string
}

// checkKeys writes every key of the universe, with a value and with an empty value, and asserts the verdict of
// the loader and of the schema against the documented rule: a key is accepted when it is allowed, and an empty
// value only when it is not a secret.
func checkKeys(t *testing.T, checker *schemaChecker, root schemaObject, universe []keyCase, spec keysSpec) {
	t.Helper()

	loader, schema := verdicts(t, checker, root, spec.build(""))
	require.True(t, loader && schema, "the base document is valid for both")

	var accepted, rejected int

	for _, key := range universe {
		if slices.Contains(spec.skip, key.name) {
			continue
		}

		allowed := slices.Contains(spec.allowed, key.name)

		wantValue := allowed
		wantEmpty := allowed && !slices.Contains(secretFields(), key.name)

		for _, variant := range []struct {
			value string
			want  bool
		}{{key.value, wantValue}, {key.empty, wantEmpty}} {
			if variant.value == "" {
				continue
			}

			doc := spec.build(key.name + ": " + variant.value)
			gotLoader, gotSchema := verdicts(t, checker, root, doc)

			assert.Equal(t, variant.want, gotLoader, "the loader on:\n%s", doc)
			assert.Equal(t, variant.want, gotSchema, "the schema on:\n%s", doc)

			if variant.want {
				accepted++
			} else {
				rejected++
			}
		}
	}

	// the loop must have tested something on both sides: a skipped universe would pass without checking
	assert.Positive(t, rejected, "keys that are not allowed were tested")

	if len(spec.allowed) > len(spec.skip) {
		assert.Positive(t, accepted, "keys that are allowed were tested")
	}
}

func TestKeysOfEveryExporterKind(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../.schema/bootstrap-schema.json")
	require.NoError(t, err)

	checker, root, err := newSchemaChecker(raw)
	require.NoError(t, err)

	common := []string{tcKind, tcMeta, "flushInterval", "maxEventInMemory", "eventType"}
	formats := []string{"filename", "csvTemplate", "format", "parquetCompressionCodec"}

	tests := []struct {
		kind    string
		base    string
		allowed []string
	}{
		{bootstrap.ExporterFile, "outputDir: /tmp", append([]string{"outputDir"}, formats...)},
		{bootstrap.ExporterWebhook, "endpointUrl: {fromEnv: U}", []string{endpointURLKey, tcSecret, tcHeaders}},
		{bootstrap.ExporterLog, "logFormat: x", []string{"logFormat"}},
		{bootstrap.ExporterS3, "connection: s", append([]string{tcConnection, tcPath}, formats...)},
		{bootstrap.ExporterKinesis, "connection: ks", []string{tcConnection, "streamArn", "streamName"}},
		{
			bootstrap.ExporterGoogleStorage,
			"bucket: b",
			append([]string{tcBucket, tcGooglecredentials, tcPath}, formats...),
		},
		{bootstrap.ExporterSQS, "connection: qq", []string{tcConnection, "queueUrl"}},
		{bootstrap.ExporterKafka, "kafka: {topic: t, addresses: [\"h:1\"]}", []string{tcKafka}},
		{bootstrap.ExporterPubSub, "projectId: p\n    topic: t", []string{"projectId", "topic", tcGooglecredentials}},
		{
			bootstrap.ExporterBigQuery,
			"projectId: p\n    datasetId: d",
			[]string{"projectId", "datasetId", tcTablename, tcGooglecredentials},
		},
		{
			bootstrap.ExporterRedshift, "connection: w\n    database: d\n    clusterIdentifier: c",
			[]string{tcConnection, "database", "clusterIdentifier", "dbUser", "secretArn", tcTablename, "autoMigrate"},
		},
		{
			bootstrap.ExporterAzure,
			"container: c\n    accountName: a",
			append([]string{"container", "accountName", tcAccountkey, tcPath}, formats...),
		},
		{bootstrap.ExporterOTel, "tracerName: t", []string{"tracerName"}},
	}

	universe := keyCases(t, reflect.TypeFor[bootstrap.Exporter]())

	for _, tcase := range tests {
		t.Run(tcase.kind, func(t *testing.T) {
			t.Parallel()

			prefix := kindConns + "exporters:\n  e:\n    kind: " + tcase.kind + "\n    " + tcase.base + "\n"

			// the Redshift cluster and workgroup exclusion is a loader rule that the schema does not have
			skip := []string{tcKind, "workgroupName"}

			for _, line := range strings.FieldsFunc(tcase.base, func(r rune) bool { return r == '\n' }) {
				name, _, _ := strings.Cut(strings.TrimSpace(line), ":")
				skip = append(skip, name)
			}

			checkKeys(t, checker, root, universe, keysSpec{
				build: func(extra string) string {
					if extra == "" {
						return prefix
					}

					return prefix + "    " + extra + "\n"
				},
				skip:    skip,
				allowed: append(slices.Clone(common), tcase.allowed...),
			})
		})
	}
}

func TestKeysOfEveryNotifierKind(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../.schema/bootstrap-schema.json")
	require.NoError(t, err)

	checker, root, err := newSchemaChecker(raw)
	require.NoError(t, err)

	tests := []struct {
		kind     string
		required string
		allowed  []string
	}{
		{bootstrap.NotifierSlack, webhookURLKey, []string{webhookURLKey}},
		{bootstrap.NotifierMicrosoftTeams, webhookURLKey, []string{webhookURLKey}},
		{bootstrap.NotifierDiscord, webhookURLKey, []string{webhookURLKey}},
		{bootstrap.NotifierWebhook, endpointURLKey, []string{endpointURLKey, tcSecret, tcHeaders}},
	}

	universe := keyCases(t, reflect.TypeFor[bootstrap.Notifier]())

	for _, tcase := range tests {
		t.Run(tcase.kind, func(t *testing.T) {
			t.Parallel()

			prefix := "version: 2\nnotifiers:\n  n:\n    kind: " + tcase.kind + "\n    " + tcase.required + ": {fromEnv: U}\n"

			checkKeys(t, checker, root, universe, keysSpec{
				build: func(extra string) string {
					if extra == "" {
						return prefix
					}

					return prefix + "    " + extra + "\n"
				},
				skip:    []string{tcKind, tcase.required},
				allowed: append([]string{tcKind, tcMeta}, tcase.allowed...),
			})
		})
	}
}

func TestKeysOfEveryCredentialsType(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../.schema/bootstrap-schema.json")
	require.NoError(t, err)

	checker, root, err := newSchemaChecker(raw)
	require.NoError(t, err)

	// typeLine is what is written for the type (nothing when it is absent), base the keys that make it valid
	tests := []struct {
		name     string
		typeLine string
		base     string
		skip     []string
		allowed  []string
	}{
		{"absent", "", "", nil, nil},
		{"empty", `type: ""`, "", nil, nil},
		{bootstrap.CredentialsChain, "type: chain", "", nil, nil},
		{
			bootstrap.CredentialsStatic, "type: static", "accessKeyId: {fromEnv: A}, secretAccessKey: {fromEnv: B}",
			[]string{tcAccesskeyid, tcSecretaccesskey}, []string{tcAccesskeyid, tcSecretaccesskey, tcSessiontoken},
		},
		{
			bootstrap.CredentialsAssumeRole, "type: assumeRole", "roleArn: arn", []string{tcRolearn},
			[]string{tcRolearn, "sessionName", tcExternalid},
		},
		{
			bootstrap.CredentialsWebIdentity, "type: webIdentity", "roleArn: arn, tokenFile: /t",
			[]string{tcRolearn, "tokenFile"}, []string{tcRolearn, "tokenFile", "sessionName"},
		},
	}

	universe := keyCases(t, reflect.TypeFor[bootstrap.Credentials]())

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			entries := slices.DeleteFunc(
				[]string{tcase.typeLine, tcase.base},
				func(entry string) bool { return entry == "" },
			)

			checkKeys(t, checker, root, universe, keysSpec{
				build: func(extra string) string {
					all := slices.Clone(entries)
					if extra != "" {
						all = append(all, extra)
					}

					return "version: 2\nconnections:\n  a: {type: s3, credentials: {" + strings.Join(all, ", ") + "}}\n"
				},
				skip:    append([]string{"type"}, tcase.skip...),
				allowed: tcase.allowed,
			})
		})
	}
}

// TestRedshiftExclusions covers the rules that tie the keys of the redshift exporter together. The expected
// verdict is written out from the documentation: exactly one of clusterIdentifier and workgroupName, dbUser not
// with workgroupName, dbUser not with secretArn, and an empty value is the same as an absent key.
func TestRedshiftExclusions(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../.schema/bootstrap-schema.json")
	require.NoError(t, err)

	checker, root, err := newSchemaChecker(raw)
	require.NoError(t, err)

	tests := []struct {
		name       string
		keys       string
		wantAccept bool
	}{
		{"cluster only", "clusterIdentifier: c", true},
		{"workgroup only", "workgroupName: g", true},
		{"both", "clusterIdentifier: c\n    workgroupName: g", false},
		{"neither", "tableName: t", false},
		{"empty cluster and a workgroup", "clusterIdentifier: \"\"\n    workgroupName: g", true},
		{"a cluster and an empty workgroup", "clusterIdentifier: c\n    workgroupName: \"\"", true},
		{"both empty", "clusterIdentifier: \"\"\n    workgroupName: \"\"", false},
		{"cluster with a user", "clusterIdentifier: c\n    dbUser: u", true},
		{"workgroup with a user", "workgroupName: g\n    dbUser: u", false},
		{"workgroup with an empty user", "workgroupName: g\n    dbUser: \"\"", true},
		{"user and secret ARN", "clusterIdentifier: c\n    dbUser: u\n    secretArn: a", false},
		{"secret ARN with a workgroup", "workgroupName: g\n    secretArn: a", true},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			doc := kindConns + "exporters:\n  e:\n    kind: redshift\n    connection: w\n    database: d\n    " +
				tcase.keys + "\n"

			loader, schema := verdicts(t, checker, root, doc)
			assert.Equal(t, tcase.wantAccept, loader, "the loader on:\n%s", doc)
			assert.Equal(t, tcase.wantAccept, schema, "the schema on:\n%s", doc)
		})
	}
}
