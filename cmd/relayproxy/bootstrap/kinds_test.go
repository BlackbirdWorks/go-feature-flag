package bootstrap_test

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

const kindConns = `version: 2
connections:
  s: {type: s3, bucket: b}
  nobucket: {type: s3}
  k: {type: kinesis}
  ks: {type: kinesis, stream: st}
  q: {type: sqs}
  qq: {type: sqs, queueUrl: "https://sqs.example/q"}
  w: {type: redshift}
`

func TestExporterKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		body     string
		wantErr  error
		wantPath string
	}{
		{name: "file", body: "kind: file\n    outputDir: /tmp/x"},
		{
			name: "file with format",
			body: "kind: file\n    outputDir: /tmp/x\n    format: csv\n    csvTemplate: '{{.Kind}}'",
		},
		{
			name:     "file with bad format",
			body:     "kind: file\n    outputDir: /tmp/x\n    format: xml",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.format",
		},
		{
			name: "webhook",
			body: "kind: webhook\n    endpointUrl: {fromEnv: U}\n    secret: {fromEnv: S}\n    headers: {A: {fromEnv: H}}",
		},
		{
			name:     "webhook without url",
			body:     "kind: webhook",
			wantErr:  bootstrap.ErrMissingValue,
			wantPath: "exporters.e.endpointUrl",
		},
		{
			name:     "webhook inline header",
			body:     "kind: webhook\n    endpointUrl: {fromEnv: U}\n    headers: {A: x}",
			wantErr:  bootstrap.ErrInlineSecret,
			wantPath: "exporters.e.headers.A",
		},
		{name: "log", body: "kind: log\n    logFormat: x"},
		{name: "s3", body: "kind: s3\n    connection: s\n    path: p/"},
		{
			name:     "s3 connection without bucket",
			body:     "kind: s3\n    connection: nobucket",
			wantErr:  bootstrap.ErrMissingValue,
			wantPath: "exporters.e.connection",
		},
		{
			name:     "s3 without connection",
			body:     "kind: s3",
			wantErr:  bootstrap.ErrMissingValue,
			wantPath: "exporters.e.connection",
		},
		{name: "kinesis stream on the exporter", body: "kind: kinesis\n    connection: k\n    streamName: x"},
		{name: "kinesis stream on the connection", body: "kind: kinesis\n    connection: ks"},
		{
			name:     "kinesis without stream",
			body:     "kind: kinesis\n    connection: k",
			wantErr:  bootstrap.ErrMissingValue,
			wantPath: "exporters.e.streamName",
		},
		{name: "googleStorage", body: "kind: googleStorage\n    bucket: b\n    googleCredentials: {fromFile: /c.json}"},
		{
			name:     "googleStorage without bucket",
			body:     "kind: googleStorage",
			wantErr:  bootstrap.ErrMissingValue,
			wantPath: "exporters.e.bucket",
		},
		{name: "sqs queue on the exporter", body: "kind: sqs\n    connection: q\n    queueUrl: https://sqs.example/q"},
		{name: "sqs queue on the connection", body: "kind: sqs\n    connection: qq"},
		{
			name:     "sqs without queue",
			body:     "kind: sqs\n    connection: q",
			wantErr:  bootstrap.ErrMissingValue,
			wantPath: "exporters.e.queueUrl",
		},
		{name: "kafka", body: "kind: kafka\n    kafka: {topic: t, addresses: [a:9092]}"},
		{
			name:     "kafka without topic",
			body:     "kind: kafka\n    kafka: {addresses: [a:9092]}",
			wantErr:  bootstrap.ErrMissingValue,
			wantPath: "exporters.e.kafka",
		},
		{name: "pubsub", body: "kind: pubsub\n    projectId: p\n    topic: t"},
		{
			name:     "pubsub without topic",
			body:     "kind: pubsub\n    projectId: p",
			wantErr:  bootstrap.ErrMissingValue,
			wantPath: "exporters.e.topic",
		},
		{name: "bigquery", body: "kind: bigquery\n    projectId: p\n    datasetId: d\n    tableName: t"},
		{
			name:     "bigquery without dataset",
			body:     "kind: bigquery\n    projectId: p",
			wantErr:  bootstrap.ErrMissingValue,
			wantPath: "exporters.e.datasetId",
		},
		{
			name: "redshift cluster",
			body: "kind: redshift\n    connection: w\n    database: d\n" +
				"    clusterIdentifier: c\n    dbUser: u\n    autoMigrate: true",
		},
		{
			name:     "redshift workgroup with dbUser",
			body:     "kind: redshift\n    connection: w\n    database: d\n    workgroupName: g\n    dbUser: u",
			wantErr:  bootstrap.ErrNotAllowed,
			wantPath: "exporters.e.dbUser",
		},
		{
			name: "redshift dbUser and secretArn",
			body: "kind: redshift\n    connection: w\n    database: d\n" +
				"    clusterIdentifier: c\n    dbUser: u\n    secretArn: a",
			wantErr:  bootstrap.ErrNotAllowed,
			wantPath: "exporters.e.dbUser",
		},
		{
			name:     "redshift bad table",
			body:     "kind: redshift\n    connection: w\n    database: d\n    clusterIdentifier: c\n    tableName: 'a;b'",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.tableName",
		},
		{
			name:     "redshift without database",
			body:     "kind: redshift\n    connection: w\n    clusterIdentifier: c",
			wantErr:  bootstrap.ErrMissingValue,
			wantPath: "exporters.e.database",
		},
		{
			name: "azure",
			body: "kind: azureBlobStorage\n    accountName: a\n    accountKey: {fromEnv: K}\n    container: c",
		},
		{
			name:     "azure without container",
			body:     "kind: azureBlobStorage\n    accountName: a",
			wantErr:  bootstrap.ErrMissingValue,
			wantPath: "exporters.e.container",
		},
		{
			name: "opentelemetry",
			body: "kind: opentelemetry\n    tracerName: t\n    flushInterval: 10s\n    maxEventInMemory: 5",
		},
		{name: "no kind", body: "meta: {a: b}", wantErr: bootstrap.ErrMissingValue, wantPath: "exporters.e.kind"},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			doc := kindConns + "exporters:\n  e:\n    " + tcase.body + "\n"

			cfg, err := bootstrap.Parse([]byte(doc), nil, options(nil, nil, false))
			if tcase.wantErr == nil {
				require.NoError(t, err)

				got := cfg.Exporters["e"]
				assert.Equal(t, bodyValue(tcase.body, "kind"), got.Kind)
				assert.Equal(t, bodyValue(tcase.body, "connection"), got.Connection)
				assert.Equal(t, cmp.Or(bodyValue(tcase.body, "flushInterval"), "60s"), got.FlushInterval.String())

				wantEvents := bootstrap.DefaultMaxEvents
				if explicit := bodyValue(tcase.body, "maxEventInMemory"); explicit != "" {
					wantEvents, _ = strconv.Atoi(explicit)
				}

				assert.Equal(t, wantEvents, *got.MaxEventInMemory)

				return
			}

			require.NotNil(t, findError(t, err, tcase.wantErr, tcase.wantPath))
		})
	}
}

func TestNotifierKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr error
		name    string
		body    string
	}{
		{name: "slack", body: "kind: slack\n    webhookUrl: {fromEnv: U}"},
		{name: "teams", body: "kind: microsoftteams\n    webhookUrl: {fromEnv: U}"},
		{name: "discord", body: "kind: discord\n    webhookUrl: {fromEnv: U}\n    meta: {a: b}"},
		{name: "webhook", body: "kind: webhook\n    endpointUrl: {fromEnv: U}\n    headers: {A: {fromEnv: H}}"},
		{
			name:    "slack inline",
			body:    "kind: slack\n    webhookUrl: https://hooks.example/x",
			wantErr: bootstrap.ErrInlineSecret,
		},
		{
			name:    "slack with endpointUrl",
			body:    "kind: slack\n    webhookUrl: {fromEnv: U}\n    endpointUrl: {fromEnv: U}",
			wantErr: bootstrap.ErrNotAllowed,
		},
		{name: "no kind", body: "meta: {a: b}", wantErr: bootstrap.ErrMissingValue},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			doc := "version: 2\nnotifiers:\n  n:\n    " + tcase.body + "\n"

			cfg, err := bootstrap.Parse([]byte(doc), nil, options(nil, nil, false))
			if tcase.wantErr == nil {
				require.NoError(t, err)
				assert.Equal(t, bodyValue(tcase.body, "kind"), cfg.Notifiers["n"].Kind)

				return
			}

			require.ErrorIs(t, err, tcase.wantErr)
		})
	}
}

func TestConnectionTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		body     string
		wantType string
	}{
		{name: "s3", body: "{type: s3, bucket: b}", wantType: bootstrap.TypeS3},
		{name: "kinesis", body: "{type: kinesis, stream: s}", wantType: bootstrap.TypeKinesis},
		{name: "sqs", body: "{type: sqs}", wantType: bootstrap.TypeSQS},
		{name: "redshift", body: "{type: redshift, region: eu-west-1}", wantType: bootstrap.TypeRedshift},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := bootstrap.Parse(
				[]byte("version: 2\nconnections:\n  c: "+tcase.body+"\n"),
				nil,
				options(nil, nil, false),
			)
			require.NoError(t, err)
			assert.Equal(t, tcase.wantType, cfg.Connections["c"].Type())

			asYAML, err := yaml.Marshal(cfg.Connections["c"])
			require.NoError(t, err)
			assert.Contains(t, string(asYAML), "type: "+tcase.wantType)
		})
	}

	assert.Empty(t, bootstrap.Connection{}.Type())
}

func TestAdminKeyChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		keys     string
		wantErr  error
		wantMsg  string
		wantKeys int
	}{
		{
			name:    "duplicate plain key",
			keys:    "[k1, k1]",
			wantErr: bootstrap.ErrDuplicateKey,
			wantMsg: "auth.adminKeys[1]",
		},
		{name: "duplicate reference", keys: "[{fromEnv: A}, {fromEnv: A}]", wantErr: bootstrap.ErrDuplicateKey},
		{name: "same text, different source", keys: "[{fromEnv: A}, {fromFile: A}]", wantKeys: 2},
		{name: "empty key", keys: "['']", wantErr: bootstrap.ErrMissingValue},
		{name: "null key", keys: "[~]", wantErr: bootstrap.ErrMissingValue},
		{name: "bad variable name", keys: "[{fromEnv: 'a b'}]", wantErr: bootstrap.ErrInvalidSecret},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			doc := fmt.Sprintf("version: 2\nauth:\n  adminKeys: %s\n", tcase.keys)

			cfg, err := bootstrap.Parse([]byte(doc), nil, options(nil, nil, false))
			if tcase.wantErr == nil {
				require.NoError(t, err)
				assert.Len(t, cfg.Auth.AdminKeys, tcase.wantKeys)

				return
			}

			require.ErrorIs(t, err, tcase.wantErr)
			assert.Contains(t, err.Error(), tcase.wantMsg)
		})
	}
}

func TestErrorFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *bootstrap.Error
		want string
	}{
		{
			name: "path and line",
			err:  &bootstrap.Error{Path: "a.b", Line: 3, Err: bootstrap.ErrUnknownKey, Detail: "x"},
			want: "a.b (line 3): unknown key: x",
		},
		{
			name: "path only",
			err:  &bootstrap.Error{Path: "a", Err: bootstrap.ErrMissingValue},
			want: "a: required value missing",
		},
		{name: "line only", err: &bootstrap.Error{Line: 2, Err: bootstrap.ErrSyntax}, want: "line 2: invalid YAML"},
		{name: "neither", err: &bootstrap.Error{Err: bootstrap.ErrSyntax, Detail: "d"}, want: "invalid YAML: d"},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tcase.want, tcase.err.Error())
		})
	}
}

func TestDurationString(t *testing.T) {
	t.Parallel()

	var durations struct {
		A bootstrap.Duration `yaml:"a"`
		B bootstrap.Duration `yaml:"b"`
	}

	require.NoError(t, yaml.Unmarshal([]byte("a: 1m\nb: 1500ms\n"), &durations))
	assert.Equal(t, "60s", durations.A.String())
	assert.Equal(t, "1.5s", durations.B.String())

	text, err := durations.A.MarshalText()
	require.NoError(t, err)
	assert.Equal(t, "60s", string(text))
}

func TestSecretRedaction(t *testing.T) {
	t.Parallel()

	const sentinel = "SENTINEL-inline"

	sec := secretOf(t, sentinel)

	text, err := sec.MarshalText()
	require.NoError(t, err)

	asJSON, err := sec.MarshalJSON()
	require.NoError(t, err)

	tests := []struct {
		name string
		got  string
	}{
		{name: "String", got: sec.String()},
		{name: "GoString", got: sec.GoString()},
		{name: "%v", got: sec.String()},
		{name: fmtPlus, got: fmt.Sprintf(fmtPlus, sec)},
		{name: fmtGo, got: fmt.Sprintf(fmtGo, sec)},
		{name: "MarshalText", got: string(text)},
		{name: "MarshalJSON", got: string(asJSON)},
		{name: "LogValue", got: sec.LogValue().String()},
		{name: "resolved GoString", got: bootstrap.ResolvedSecret{}.GoString()},
		{name: "resolved LogValue", got: bootstrap.ResolvedSecret{}.LogValue().String()},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			assert.Contains(t, tcase.got, bootstrap.RedactedValue)
			assert.NotContains(t, tcase.got, sentinel)
		})
	}
}

// bodyValue returns the value of a top-level "key: value" line of a test body, or "".
func bodyValue(body, key string) string {
	for line := range strings.SplitSeq(body, "\n") {
		if value, found := strings.CutPrefix(strings.TrimSpace(line), key+": "); found {
			return value
		}
	}

	return ""
}
