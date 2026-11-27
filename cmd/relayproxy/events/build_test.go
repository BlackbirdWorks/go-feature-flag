package events_test

import (
	"context"
	"fmt"
	"maps"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/exporter/azureexporter"
	"github.com/blackbirdworks/semaphore/exporter/bigqueryexporter"
	"github.com/blackbirdworks/semaphore/exporter/fileexporter"
	"github.com/blackbirdworks/semaphore/exporter/gcstorageexporter"
	"github.com/blackbirdworks/semaphore/exporter/kafkaexporter"
	"github.com/blackbirdworks/semaphore/exporter/kinesisexporter"
	"github.com/blackbirdworks/semaphore/exporter/logsexporter"
	"github.com/blackbirdworks/semaphore/exporter/opentelemetryexporter"
	"github.com/blackbirdworks/semaphore/exporter/pubsubexporterv2"
	"github.com/blackbirdworks/semaphore/exporter/redshiftexporter"
	"github.com/blackbirdworks/semaphore/exporter/s3exporterv2"
	"github.com/blackbirdworks/semaphore/exporter/sqsexporter"
	"github.com/blackbirdworks/semaphore/exporter/webhookexporter"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/notifier/discordnotifier"
	"github.com/blackbirdworks/semaphore/notifier/microsoftteamsnotifier"
	"github.com/blackbirdworks/semaphore/notifier/slacknotifier"
	"github.com/blackbirdworks/semaphore/notifier/webhooknotifier"
)

const connections = `version: 2
connections:
  s3c: {type: s3, region: us-east-1, bucket: acme-events, endpoint: "http://127.0.0.1:4566", pathStyle: true,
        credentials: {type: static, accessKeyId: {fromEnv: AK}, secretAccessKey: {fromEnv: SK}}}
  s3nobucket: {type: s3, region: us-east-1}
  kin: {type: kinesis, region: eu-west-1, stream: conn-stream, endpoint: "http://127.0.0.1:4567",
        credentials: {type: static, accessKeyId: {fromEnv: AK}, secretAccessKey: {fromEnv: SK}}}
  kinnostream: {type: kinesis, region: eu-west-1}
  sqsc: {type: sqs, region: us-west-2, queueUrl: "http://sqs.local/queue/conn"}
  sqsnoqueue: {type: sqs, region: us-west-2}
  rs: {type: redshift, region: us-east-2}
`

var buildEnv = map[string]string{ //nolint:gochecknoglobals // a read-only fixture shared by the tests
	"AK": "AKIDEXAMPLE", "SK": "sk-example", envURL: hookURL, envSecret: secretValue,
	"HDR": headerValue, "CREDS": `{"type":"service_account"}`, "KEY": "azure-key",
}

// parse returns the configuration made of the connections above and the exporters and notifiers of body.
func parse(tb testing.TB, body string) (*bootstrap.Config, bootstrap.LoadOptions) {
	tb.Helper()

	return parseDoc(tb, connections+body, buildEnv)
}

func TestBuilder_ExporterKinds(t *testing.T) {
	t.Parallel()

	defaults := config.DefaultExporter()

	format := bootstrap.DefaultExportFormat // the loader fills it in: the exporters read the format case-insensitively

	tests := []struct {
		want     exporter.Exporter
		name     string
		body     string
		wantKind string
	}{
		{
			name: "file with defaults", wantKind: bootstrap.ExporterFile,
			body: "exporters:\n  e: {kind: file, outputDir: /var/events}\n",
			want: &fileexporter.Exporter{
				Format: format, OutputDir: "/var/events", Filename: defaults.FileName, CsvTemplate: defaults.CsvFormat,
				ParquetCompressionCodec: defaults.ParquetCompressionCodec,
			},
		},
		{
			name:     "file with settings",
			wantKind: bootstrap.ExporterFile,
			body: "exporters:\n  e: {kind: file, outputDir: /o, format: csv, filename: f.csv, csvTemplate: '{{.Key}}',\n" +
				"        parquetCompressionCodec: GZIP}\n",
			want: &fileexporter.Exporter{
				Format:                  "csv",
				OutputDir:               "/o",
				Filename:                "f.csv",
				CsvTemplate:             "{{.Key}}",
				ParquetCompressionCodec: "GZIP",
			},
		},
		{
			name:     kindWebhook,
			wantKind: bootstrap.ExporterWebhook,
			body: "exporters:\n  e: {kind: webhook, endpointUrl: {fromEnv: URL}, secret: {fromEnv: SECRET},\n" +
				"        headers: {X-A: {fromEnv: HDR}}, meta: {env: prod}}\n",
			want: &webhookexporter.Exporter{
				EndpointURL: hookURL, Secret: secretValue, Meta: map[string]string{"env": "prod"},
				Headers: map[string][]string{"X-A": {headerValue}},
			},
		},
		{
			name: "log with defaults", wantKind: bootstrap.ExporterLog,
			body: "exporters:\n  e: {kind: log}\n",
			want: &logsexporter.Exporter{LogFormat: defaults.LogFormat},
		},
		{
			name: "log with a format", wantKind: bootstrap.ExporterLog,
			body: "exporters:\n  e: {kind: log, logFormat: '{{.Key}}'}\n",
			want: &logsexporter.Exporter{LogFormat: "{{.Key}}"},
		},
		{
			name: "google storage", wantKind: bootstrap.ExporterGoogleStorage,
			body: "exporters:\n  e: {kind: googleStorage, bucket: b, path: p}\n",
			want: &gcstorageexporter.Exporter{
				Bucket: "b", Format: format, Path: "p", Filename: defaults.FileName, CsvTemplate: defaults.CsvFormat,
				ParquetCompressionCodec: defaults.ParquetCompressionCodec,
			},
		},
		{
			name: "kafka", wantKind: bootstrap.ExporterKafka,
			body: "exporters:\n  e: {kind: kafka, kafka: {topic: t, addresses: ['a:9092', 'b:9092']}}\n",
			want: &kafkaexporter.Exporter{
				Format:   defaults.Format,
				Settings: kafkaexporter.Settings{Topic: "t", Addresses: []string{"a:9092", "b:9092"}},
			},
		},
		{
			name: "pubsub", wantKind: bootstrap.ExporterPubSub,
			body: "exporters:\n  e: {kind: pubsub, projectId: p, topic: t}\n",
			want: &pubsubexporterv2.Exporter{ProjectID: "p", Topic: "t"},
		},
		{
			name:     "bigquery",
			wantKind: bootstrap.ExporterBigQuery,
			body: "exporters:\n  e: {kind: bigquery, projectId: p, datasetId: ds, tableName: tb,\n" +
				"        googleCredentials: {fromEnv: CREDS}}\n",
			want: &bigqueryexporter.Exporter{
				ProjectID: "p", DatasetID: "ds", TableName: "tb", GoogleCredentials: []byte(buildEnv["CREDS"]),
			},
		},
		{
			name:     "azure",
			wantKind: bootstrap.ExporterAzure,
			body: "exporters:\n  e: {kind: azureBlobStorage, container: c, accountName: acc,\n" +
				"        accountKey: {fromEnv: KEY}, path: p}\n",
			want: &azureexporter.Exporter{
				Container:               "c",
				AccountName:             "acc",
				AccountKey:              "azure-key",
				Format:                  format,
				Path:                    "p",
				Filename:                defaults.FileName,
				CsvTemplate:             defaults.CsvFormat,
				ParquetCompressionCodec: defaults.ParquetCompressionCodec,
			},
		},
		{
			name: "opentelemetry", wantKind: bootstrap.ExporterOTel,
			body: "exporters:\n  e: {kind: opentelemetry, tracerName: tr}\n",
			want: &opentelemetryexporter.Exporter{TracerName: "tr"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, opts := parse(t, tt.body)
			built, err := events.Builder{Secrets: opts}.Exporters(context.Background(), events.SpecsFrom(cfg))
			require.NoError(t, err)
			require.Len(t, built, 1)

			assert.Equal(t, "e", built[0].Name)
			assert.Equal(t, tt.wantKind, built[0].Kind)
			assert.Equal(t, tt.want, built[0].Inner.Unseal())
			assert.Equal(
				t,
				defaults.FlushInterval,
				built[0].Config.FlushInterval,
				"the default flush interval of version 1",
			)
			assert.Equal(t, defaults.MaxEventInMemory, built[0].Config.MaxEventInMemory, "the default of version 1")
			assert.NotEmpty(t, built[0].Identity)
		})
	}
}

func TestBuilder_FlushSettings(t *testing.T) {
	t.Parallel()

	cfg, opts := parse(t, "exporters:\n  e: {kind: log, flushInterval: 5s, maxEventInMemory: 7}\n")
	built, err := events.Builder{Secrets: opts}.Exporters(context.Background(), events.SpecsFrom(cfg))
	require.NoError(t, err)

	assert.Equal(t, 5*time.Second, built[0].Config.FlushInterval)
	assert.Equal(t, int64(7), built[0].Config.MaxEventInMemory)
}

func TestBuilder_GoogleCredentialsBecomeAClientOption(t *testing.T) {
	t.Parallel()

	body := "exporters:\n  gcs: {kind: googleStorage, bucket: b, googleCredentials: {fromEnv: CREDS}}\n" +
		"  ps: {kind: pubsub, projectId: p, topic: t, googleCredentials: {fromEnv: CREDS}}\n"
	cfg, opts := parse(t, body)

	built, err := events.Builder{Secrets: opts}.Exporters(context.Background(), events.SpecsFrom(cfg))
	require.NoError(t, err)
	require.Len(t, built, 2)

	assert.Len(
		t,
		inner[*gcstorageexporter.Exporter](t, &built[0]).Options,
		1,
	)
	assert.Len(
		t,
		inner[*pubsubexporterv2.Exporter](t, &built[1]).Options,
		1,
	)
}

func TestBuilder_AWSExportersTakeTheirClientFromTheFactory(t *testing.T) {
	t.Parallel()

	body := `exporters:
  s3-a: {kind: s3, connection: s3c, path: a/b}
  s3-b: {kind: s3, connection: s3c, format: csv}
  kin-a: {kind: kinesis, connection: kin}
  kin-b: {kind: kinesis, connection: kin, streamName: own-stream}
  kin-arn: {kind: kinesis, connection: kin, streamArn: "arn:aws:kinesis:eu-west-1:1:stream/x"}
  sqs-a: {kind: sqs, connection: sqsc}
  sqs-b: {kind: sqs, connection: sqsc, queueUrl: "http://sqs.local/queue/own"}
  rs-a: {kind: redshift, connection: rs, database: db, clusterIdentifier: cl, tableName: tb, autoMigrate: true}
`
	cfg, opts := parse(t, body)
	fac := connectionFactory(t, cfg, opts)

	built, err := events.Builder{Factory: fac, Secrets: opts}.Exporters(context.Background(), events.SpecsFrom(cfg))
	require.NoError(t, err)
	require.Len(t, built, 8)

	byName := map[string]*events.ExporterDef{}
	for i := range built {
		byName[built[i].Name] = &built[i]
	}

	s3Client, err := fac.S3(context.Background(), "s3c")
	require.NoError(t, err)

	for _, name := range []string{"s3-a", "s3-b"} {
		exp := inner[*s3exporterv2.Exporter](t, byName[name])
		assert.Same(t, exp.S3Client, s3Client, "%s uses the one client of the connection", name)
		assert.Equal(t, "acme-events", exp.Bucket, "the bucket is the one of the connection")
		assert.Nil(t, exp.AwsConfig, "no configuration is loaded from the environment")
	}

	assert.Equal(t, "a/b", inner[*s3exporterv2.Exporter](t, byName["s3-a"]).S3Path)
	assert.Equal(t, "csv", inner[*s3exporterv2.Exporter](t, byName["s3-b"]).Format)

	for name, wantStream := range map[string]string{"kin-a": "conn-stream", "kin-b": "own-stream"} {
		exp := inner[*kinesisexporter.Exporter](t, byName[name])
		require.NotNil(t, exp.Settings.StreamName)
		assert.Equal(t, wantStream, *exp.Settings.StreamName)
		require.NotNil(t, exp.AwsConfig)
		assert.Equal(t, "eu-west-1", exp.AwsConfig.Region)
		require.NotNil(t, exp.AwsConfig.BaseEndpoint)
		assert.Equal(t, "http://127.0.0.1:4567", *exp.AwsConfig.BaseEndpoint, "the endpoint of the connection")
	}

	arn := inner[*kinesisexporter.Exporter](t, byName["kin-arn"])
	require.NotNil(t, arn.Settings.StreamArn)
	assert.Nil(t, arn.Settings.StreamName, "an ARN of the exporter wins over the stream of the connection")

	assert.Equal(
		t,
		"http://sqs.local/queue/conn",
		inner[*sqsexporter.Exporter](t, byName["sqs-a"]).QueueURL,
	)
	assert.Equal(
		t,
		"http://sqs.local/queue/own",
		inner[*sqsexporter.Exporter](t, byName["sqs-b"]).QueueURL,
	)
	assert.Equal(
		t,
		"us-west-2",
		inner[*sqsexporter.Exporter](t, byName["sqs-a"]).AwsConfig.Region,
	)

	redshift := inner[*redshiftexporter.Exporter](t, byName["rs-a"])
	assert.Equal(t, "cl", redshift.ClusterIdentifier)
	assert.True(t, redshift.AutoMigrate)
	assert.Equal(t, "us-east-2", redshift.AwsConfig.Region)

	// 8 exporters on 4 connections: one configuration (and one S3 client) per connection
	assert.Equal(t, map[string]int{"s3c": 1, "kin": 1, connSQS: 1, "rs": 1}, fac.Builds())
}

func TestBuilder_ExporterErrors(t *testing.T) {
	t.Parallel()

	type exps = map[string]bootstrap.Exporter

	tests := []struct {
		wantErr   error
		exporters exps
		factory   func(tb testing.TB, cfg *bootstrap.Config, opts bootstrap.LoadOptions) *connection.Factory
		secrets   func(opts bootstrap.LoadOptions) bootstrap.LoadOptions
		name      string
		body      string
		wantName  string
		wantIndex int
	}{
		{
			name: "unknown kind", wantErr: events.ErrUnknownKind, wantIndex: 1, wantName: nameBad,
			exporters: exps{"a": {Kind: bootstrap.ExporterLog}, nameBad: {Kind: kindNope}},
		},
		{
			name:      "file without outputDir",
			wantErr:   events.ErrInvalidSettings,
			wantIndex: 2,
			wantName:  nameBad,
			exporters: exps{
				"a":     {Kind: bootstrap.ExporterLog},
				"b":     {Kind: bootstrap.ExporterLog},
				nameBad: {Kind: bootstrap.ExporterFile},
			},
		},
		{
			name: "tracking events are not carried yet", wantErr: events.ErrUnsupportedEventType, wantName: nameBad,
			exporters: exps{nameBad: {Kind: bootstrap.ExporterLog, EventType: "tracking"}},
		},
		{
			name: "webhook without endpoint", wantErr: events.ErrInvalidSettings, wantName: nameBad,
			exporters: exps{nameBad: {Kind: bootstrap.ExporterWebhook}},
		},
		{
			name:     "secret that does not resolve",
			wantErr:  events.ErrUnresolvedSecret,
			wantName: nameBad,
			secrets:  noSecrets,
			body:     "exporters:\n  bad: {kind: webhook, endpointUrl: {fromEnv: URL}}\n",
		},
		{
			name: "google storage without bucket", wantErr: events.ErrInvalidSettings, wantName: nameBad,
			exporters: exps{nameBad: {Kind: bootstrap.ExporterGoogleStorage}},
		},
		{
			name: "pubsub without topic", wantErr: events.ErrInvalidSettings, wantName: nameBad,
			exporters: exps{nameBad: {Kind: bootstrap.ExporterPubSub, ProjectID: "p"}},
		},
		{
			name: "bigquery without dataset", wantErr: events.ErrInvalidSettings, wantName: nameBad,
			exporters: exps{nameBad: {Kind: bootstrap.ExporterBigQuery, ProjectID: "p"}},
		},
		{
			name: "azure without container", wantErr: events.ErrInvalidSettings, wantName: nameBad,
			exporters: exps{nameBad: {Kind: bootstrap.ExporterAzure, AccountName: "a"}},
		},
		{
			name: "kafka without addresses", wantErr: events.ErrInvalidSettings, wantName: nameBad,
			exporters: exps{nameBad: {Kind: bootstrap.ExporterKafka, Kafka: &bootstrap.KafkaSettings{Topic: "t"}}},
		},
		{
			name:      "s3 without a connection",
			wantErr:   events.ErrInvalidSettings,
			wantName:  nameBad,
			factory:   plainFactory,
			exporters: exps{nameBad: {Kind: bootstrap.ExporterS3}},
		},
		{
			name:      "s3 with an unknown connection",
			wantErr:   events.ErrUnknownConnection,
			wantName:  nameBad,
			factory:   plainFactory,
			exporters: exps{nameBad: {Kind: bootstrap.ExporterS3, Connection: kindNope}},
		},
		{
			name:      "s3 on a kinesis connection",
			wantErr:   events.ErrWrongConnectionKind,
			wantName:  nameBad,
			factory:   plainFactory,
			exporters: exps{nameBad: {Kind: bootstrap.ExporterS3, Connection: "kin"}},
		},
		{
			name:      "kinesis on an s3 connection",
			wantErr:   events.ErrWrongConnectionKind,
			wantName:  nameBad,
			factory:   plainFactory,
			exporters: exps{nameBad: {Kind: bootstrap.ExporterKinesis, Connection: "s3c"}},
		},
		{
			name:      "sqs on a redshift connection",
			wantErr:   events.ErrWrongConnectionKind,
			wantName:  nameBad,
			factory:   plainFactory,
			exporters: exps{nameBad: {Kind: bootstrap.ExporterSQS, Connection: "rs"}},
		},
		{
			name:      "redshift on an sqs connection",
			wantErr:   events.ErrWrongConnectionKind,
			wantName:  nameBad,
			factory:   plainFactory,
			exporters: exps{nameBad: {Kind: bootstrap.ExporterRedshift, Connection: connSQS}},
		},
		{
			name:      "s3 connection without a bucket",
			wantErr:   events.ErrInvalidSettings,
			wantName:  nameBad,
			factory:   plainFactory,
			exporters: exps{nameBad: {Kind: bootstrap.ExporterS3, Connection: "s3nobucket"}},
		},
		{
			name:      "kinesis without a stream",
			wantErr:   events.ErrInvalidSettings,
			wantName:  nameBad,
			factory:   plainFactory,
			exporters: exps{nameBad: {Kind: bootstrap.ExporterKinesis, Connection: "kinnostream"}},
		},
		{
			name: "sqs without a queue", wantErr: events.ErrInvalidSettings, wantName: nameBad, factory: plainFactory,
			exporters: exps{nameBad: {Kind: bootstrap.ExporterSQS, Connection: "sqsnoqueue"}},
		},
		{
			name:      "redshift without a database",
			wantErr:   events.ErrInvalidSettings,
			wantName:  nameBad,
			factory:   plainFactory,
			exporters: exps{nameBad: {Kind: bootstrap.ExporterRedshift, Connection: "rs", ClusterIdentifier: "c"}},
		},
		{
			name:     "redshift with cluster and workgroup",
			wantErr:  events.ErrInvalidSettings,
			wantName: nameBad,
			factory:  plainFactory,
			exporters: exps{nameBad: {
				Kind:              bootstrap.ExporterRedshift,
				Connection:        "rs",
				Database:          "d",
				ClusterIdentifier: "c",
				WorkgroupName:     "w",
			}},
		},
		{
			name: "an AWS kind without a factory", wantErr: events.ErrConnectionClient, wantName: nameBad,
			exporters: exps{nameBad: {Kind: bootstrap.ExporterSQS, Connection: connSQS}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, opts := parse(t, tt.body)

			specs := events.SpecsFrom(cfg)
			if tt.exporters != nil {
				specs.Exporters = tt.exporters
			}

			builder := events.Builder{Secrets: opts}
			if tt.factory != nil {
				builder.Factory = tt.factory(t, cfg, opts)
			}

			if tt.secrets != nil {
				builder.Secrets = tt.secrets(opts)
			}

			built, err := builder.Exporters(context.Background(), specs)
			require.ErrorIs(t, err, tt.wantErr)
			assert.Nil(t, built, "no exporter when one failed")

			var entry *events.EntryError

			require.ErrorAs(t, err, &entry)
			assert.Equal(t, "exporters", entry.Resource)
			assert.Equal(t, tt.wantIndex, entry.Index)
			assert.Equal(t, tt.wantName, entry.Name)
			assert.Contains(t, err.Error(), fmt.Sprintf("exporters[%d] %q", tt.wantIndex, tt.wantName))
		})
	}
}

func TestBuilder_FactoryWithoutTheConnection(t *testing.T) {
	t.Parallel()

	cfg, opts := parse(t, "exporters:\n  e: {kind: sqs, connection: sqsc}\n")

	// the bootstrap file names the connection, the factory does not know it
	builder := events.Builder{Factory: connectionFactory(t, &bootstrap.Config{}, opts), Secrets: opts}

	_, err := builder.Exporters(context.Background(), events.SpecsFrom(cfg))
	require.ErrorIs(t, err, events.ErrConnectionClient)
}

func TestBuilder_ReportsEveryFailingEntry(t *testing.T) {
	t.Parallel()

	specs := events.Specs{Exporters: map[string]bootstrap.Exporter{
		"a": {Kind: bootstrap.ExporterFile}, "b": {Kind: bootstrap.ExporterLog}, "c": {Kind: bootstrap.ExporterWebhook},
	}}

	_, err := events.Builder{}.Exporters(context.Background(), specs)
	require.ErrorIs(t, err, events.ErrInvalidSettings)

	joined, ok := err.(interface{ Unwrap() []error })
	require.True(t, ok)
	require.Len(t, joined.Unwrap(), 2, "a and c")

	var first, second *events.EntryError

	require.ErrorAs(t, joined.Unwrap()[0], &first)
	require.ErrorAs(t, joined.Unwrap()[1], &second)
	assert.Equal(t, []string{"a", "c"}, []string{first.Name, second.Name})
	assert.Equal(t, []int{0, 2}, []int{first.Index, second.Index})
}

func TestBuilder_NotifierKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want     notifier.Notifier
		name     string
		body     string
		wantKind string
	}{
		{
			name: "slack", wantKind: bootstrap.NotifierSlack,
			body: "notifiers:\n  n: {kind: slack, webhookUrl: {fromEnv: URL}}\n",
			want: &slacknotifier.Notifier{SlackWebhookURL: hookURL},
		},
		{
			name: "microsoft teams", wantKind: bootstrap.NotifierMicrosoftTeams,
			body: "notifiers:\n  n: {kind: microsoftteams, webhookUrl: {fromEnv: URL}}\n",
			want: &microsoftteamsnotifier.Notifier{MicrosoftTeamsWebhookURL: hookURL},
		},
		{
			name: "discord", wantKind: bootstrap.NotifierDiscord,
			body: "notifiers:\n  n: {kind: discord, webhookUrl: {fromEnv: URL}}\n",
			want: &discordnotifier.Notifier{DiscordWebhookURL: hookURL},
		},
		{
			name:     kindWebhook,
			wantKind: bootstrap.NotifierWebhook,
			body: "notifiers:\n  n: {kind: webhook, endpointUrl: {fromEnv: URL}, secret: {fromEnv: SECRET},\n" +
				"        headers: {X-A: {fromEnv: HDR}}, meta: {env: prod}}\n",
			want: &webhooknotifier.Notifier{
				EndpointURL: hookURL, Secret: secretValue, Meta: map[string]string{"env": "prod"},
				Headers: map[string][]string{"X-A": {headerValue}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, opts := parse(t, tt.body)
			built, err := events.Builder{Secrets: opts}.Notifiers(context.Background(), events.SpecsFrom(cfg))
			require.NoError(t, err)
			require.Len(t, built, 1)
			assert.Equal(t, "n", built[0].Name)
			assert.Equal(t, tt.wantKind, built[0].Kind)
			assert.Equal(t, tt.want, built[0].Notifier.Unseal())
		})
	}
}

func TestBuilder_NotifierErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		specs   map[string]bootstrap.Notifier
		wantErr error
		name    string
		body    string
	}{
		{
			name:    "unknown kind",
			specs:   map[string]bootstrap.Notifier{"n": {Kind: "pager"}},
			wantErr: events.ErrUnknownKind,
		},
		{
			name:    "chat without a url",
			specs:   map[string]bootstrap.Notifier{"n": {Kind: bootstrap.NotifierSlack}},
			wantErr: events.ErrInvalidSettings,
		},
		{
			name:    "webhook without an endpoint",
			specs:   map[string]bootstrap.Notifier{"n": {Kind: bootstrap.NotifierWebhook}},
			wantErr: events.ErrInvalidSettings,
		},
		{
			name:    "secret that does not resolve",
			body:    "notifiers:\n  n: {kind: discord, webhookUrl: {fromEnv: MISSING}}\n",
			wantErr: events.ErrUnresolvedSecret,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, opts := parse(t, tt.body)
			specs := events.SpecsFrom(cfg)

			if tt.specs != nil {
				specs.Notifiers = tt.specs
			}

			built, err := events.Builder{Secrets: opts}.Notifiers(context.Background(), specs)
			require.ErrorIs(t, err, tt.wantErr)
			assert.Nil(t, built)

			var entry *events.EntryError

			require.ErrorAs(t, err, &entry)
			assert.Equal(t, "notifiers", entry.Resource)
			assert.Equal(t, "n", entry.Name)
		})
	}
}

// TestBuilder_Identity checks what changes the identity of an entry, and what does not.
func TestBuilder_Identity(t *testing.T) {
	t.Parallel()

	const entry = "exporters:\n  e: {kind: webhook, endpointUrl: {fromEnv: URL}, secret: {fromEnv: SECRET}}\n"

	identityOf := func(t *testing.T, body string, env map[string]string) string {
		t.Helper()

		cfg, opts := parseDoc(t, connections+body, env)
		built, err := events.Builder{Secrets: opts}.Exporters(context.Background(), events.SpecsFrom(cfg))
		require.NoError(t, err)

		return built[0].Identity
	}

	rotated := maps.Clone(buildEnv)
	rotated["SECRET"] = "another-signing-key"

	tests := []struct {
		env  map[string]string
		want assert.ComparisonAssertionFunc
		name string
		body string
	}{
		{name: "the same spec", body: entry, env: buildEnv, want: assert.Equal},
		{name: "a rotated secret, though the spec is the same", body: entry, env: rotated, want: assert.NotEqual},
		{
			name: "a changed setting",
			env:  buildEnv,
			want: assert.NotEqual,
			body: "exporters:\n  e: {kind: webhook, endpointUrl: {fromEnv: URL}, secret: {fromEnv: SECRET}, meta: {a: b}}\n",
		},
	}

	base := identityOf(t, entry, buildEnv)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := identityOf(t, tt.body, tt.env)

			tt.want(t, base, got)
		})
	}
}

func TestBuilder_IdentityFollowsTheConnection(t *testing.T) {
	t.Parallel()

	cfg, opts := parse(t, "exporters:\n  e: {kind: sqs, connection: sqsc}\n")
	fac := connectionFactory(t, cfg, opts)
	builder := events.Builder{Factory: fac, Secrets: opts}

	first, err := builder.Exporters(context.Background(), events.SpecsFrom(cfg))
	require.NoError(t, err)

	again, err := builder.Exporters(context.Background(), events.SpecsFrom(cfg))
	require.NoError(t, err)
	assert.Equal(t, first[0].Identity, again[0].Identity)

	// the connection changes (another endpoint): the factory builds it again, so the exporter has to be rebuilt
	conn := cfg.Connections[connSQS]
	conn.SQS.Endpoint = "http://127.0.0.1:9999"
	cfg.Connections[connSQS] = conn

	require.NoError(t, fac.Replace(connectionFactorySpecs(t, cfg, opts)))

	changed, err := builder.Exporters(context.Background(), events.SpecsFrom(cfg))
	require.NoError(t, err)
	assert.NotEqual(t, first[0].Identity, changed[0].Identity)
}

func TestErrors(t *testing.T) {
	t.Parallel()

	err := &events.EntryError{Resource: "exporters", Index: 3, Name: "x", Err: events.ErrUnknownKind}

	assert.Equal(t, `exporters[3] "x": events: unknown kind`, err.Error())
	require.ErrorIs(t, err, events.ErrUnknownKind)
}

// TestBuilder_DefaultsOfVersion1 checks the defaults of a spec that did not go through the loader: they are those
// of version 1.
func TestBuilder_DefaultsOfVersion1(t *testing.T) {
	t.Parallel()

	defaults := config.DefaultExporter()
	specs := events.Specs{
		Exporters: map[string]bootstrap.Exporter{"e": {Kind: bootstrap.ExporterFile, OutputDir: "/o"}},
	}

	built, err := events.Builder{}.Exporters(context.Background(), specs)
	require.NoError(t, err)

	assert.Equal(t, &fileexporter.Exporter{
		Format: defaults.Format, OutputDir: "/o", Filename: defaults.FileName, CsvTemplate: defaults.CsvFormat,
		ParquetCompressionCodec: defaults.ParquetCompressionCodec,
	}, built[0].Inner.Unseal())
	assert.Equal(t, defaults.FlushInterval, built[0].Config.FlushInterval)
	assert.Equal(t, defaults.MaxEventInMemory, built[0].Config.MaxEventInMemory)
}
