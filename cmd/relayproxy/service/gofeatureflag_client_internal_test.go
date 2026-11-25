package service

// Whitebox: the tests reach unexported functions (initRetrieversWithRegistry, firstSharedRetriever,
// createGoFeatureFlagClient) and inject a fake shared bucket registry. The fakes are in
// shared_bucket_registry_internal_test.go.

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xitongsys/parquet-go/parquet"
	"golang.org/x/net/proxy"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config/kafka"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	retrieverinit "github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf/init"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/exporter/azureexporter"
	"github.com/blackbirdworks/semaphore/exporter/bigqueryexporter"
	"github.com/blackbirdworks/semaphore/exporter/fileexporter"
	"github.com/blackbirdworks/semaphore/exporter/gcstorageexporter"
	"github.com/blackbirdworks/semaphore/exporter/kafkaexporter"
	"github.com/blackbirdworks/semaphore/exporter/kinesisexporter"
	"github.com/blackbirdworks/semaphore/exporter/logsexporter"
	"github.com/blackbirdworks/semaphore/exporter/pubsubexporterv2"
	"github.com/blackbirdworks/semaphore/exporter/redshiftexporter"
	"github.com/blackbirdworks/semaphore/exporter/s3exporterv2"
	"github.com/blackbirdworks/semaphore/exporter/sqsexporter"
	"github.com/blackbirdworks/semaphore/exporter/webhookexporter"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/notifier/discordnotifier"
	"github.com/blackbirdworks/semaphore/notifier/microsoftteamsnotifier"
	"github.com/blackbirdworks/semaphore/notifier/slacknotifier"
	"github.com/blackbirdworks/semaphore/notifier/webhooknotifier"
	"github.com/blackbirdworks/semaphore/pkgs/stringsx"
	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/retriever/postgresqlretriever"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	bitbucket                    = "bitbucket"
	develop                      = "develop"
	clientSecret                 = "1234"
	httpsGofeatureflagOrgWebhook = "https://gofeatureflag.org/webhook-example"
	myBucket                     = "my-bucket"
	myKafkaTopic                 = "my-kafka-topic"
	myPath                       = "/my-path/"
	scramSha512                  = "SCRAM-SHA-512"
	todoUpper                    = "TODO"
	unknown                      = "unknown"
)

var (
	errAccessDenied = errors.New("AccessDenied")
)

func Test_initRetrieversWithRegistry(t *testing.T) {
	t.Parallel()

	sharedValid := s3FlagSet("a", "b1", pAYaml)
	sharedFolder := s3FlagSet("a", "b1", "")
	tests := []struct {
		retrievers *[]retrieverconf.RetrieverConf
		shared     *config.FlagSet
		reg        *sharedBucketRegistry
		wantErr    assert.ErrorAssertionFunc
		name       string
		wantLen    int
	}{
		{
			name:    "shared-poller flagset with a registry",
			shared:  &sharedValid,
			reg:     newSharedBucketRegistry(nil, 0, false, nil),
			wantLen: 1,
			wantErr: assert.NoError,
		},
		{
			name:    "shared-poller flagset without registry",
			shared:  &sharedValid,
			wantErr: assert.Error,
		},
		{
			name:    "invalid shared-poller configuration",
			shared:  &sharedFolder,
			reg:     newSharedBucketRegistry(nil, 0, false, nil),
			wantErr: assert.Error,
		},
		{
			name:    "both retriever and retrievers",
			wantErr: assert.NoError,
			retrievers: &[]retrieverconf.RetrieverConf{
				{
					Kind:           bitbucket,
					Branch:         develop,
					RepositorySlug: fixtures.RepoGoFeatureFlagConfig,
					Path:           fixtures.FlagsConfigGoffYAML,
					AuthToken:      fixtures.BitbucketTokenEnv,
					BaseURL:        fixtures.BitbucketAPIURL,
				},
				{
					Kind:           bitbucket,
					Branch:         "main",
					RepositorySlug: fixtures.RepoGoFeatureFlagConfig,
					Path:           fixtures.FlagsConfigGoffYAML,
					AuthToken:      fixtures.BitbucketTokenEnv,
					BaseURL:        fixtures.BitbucketAPIURL,
				},
			},
		},
		{
			name:    "should error with invalid retriever",
			wantErr: assert.Error,
			retrievers: &[]retrieverconf.RetrieverConf{
				{
					Kind:           bitbucket,
					Branch:         develop,
					RepositorySlug: fixtures.RepoGoFeatureFlagConfig,
					Path:           fixtures.FlagsConfigGoffYAML,
					AuthToken:      fixtures.BitbucketTokenEnv,
					BaseURL:        fixtures.BitbucketAPIURL,
				},
				{
					Kind: unknown,
				},
			},
		},
		{
			name:    "should error with invalid retriever",
			wantErr: assert.Error,
			retrievers: &[]retrieverconf.RetrieverConf{
				{
					Kind: unknown,
				},
			},
		},
		{
			name:    "only retrievers",
			wantErr: assert.NoError,
			retrievers: &[]retrieverconf.RetrieverConf{
				{
					Kind:           bitbucket,
					Branch:         develop,
					RepositorySlug: fixtures.RepoGoFeatureFlagConfig,
					Path:           fixtures.FlagsConfigGoffYAML,
					AuthToken:      fixtures.BitbucketTokenEnv,
					BaseURL:        fixtures.BitbucketAPIURL,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			proxyConf := config.FlagSet{
				Retrievers: tt.retrievers,
			}
			if tt.shared != nil {
				proxyConf = *tt.shared
			}

			r, err := initRetrieversWithRegistry(&proxyConf, tt.reg)
			tt.wantErr(t, err)
			assertRetrieversLen(t, r, tt.wantLen, tt.retrievers)
			shutdownReleasing(t, r)
		})
	}
}

func assertRetrieversLen(
	t *testing.T, r []retriever.Retriever, wantLen int, retrievers *[]retrieverconf.RetrieverConf,
) {
	t.Helper()

	if r == nil {
		return
	}

	if retrievers != nil {
		wantLen += len(*retrievers)
	}

	assert.Len(t, r, wantLen)
}

func shutdownReleasing(t *testing.T, r []retriever.Retriever) {
	t.Helper()

	for _, rr := range r {
		if sr, ok := rr.(*releasingRetriever); ok {
			require.NoError(t, sr.Shutdown(t.Context()))
		}
	}
}

func Test_initExporters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		exporters *[]config.ExporterConf
		exporter  *config.ExporterConf
		wantErr   assert.ErrorAssertionFunc
		name      string
	}{
		{
			name:    "both exporter and exporters",
			wantErr: assert.NoError,
			exporters: &[]config.ExporterConf{
				{
					Kind:        fixtures.KindWebhook,
					EndpointURL: httpsGofeatureflagOrgWebhook,
					Secret:      clientSecret,
				},
				{
					Kind:        fixtures.KindWebhook,
					EndpointURL: httpsGofeatureflagOrgWebhook,
					Secret:      clientSecret,
				},
			},
		},
		{
			name:    "exporters only",
			wantErr: assert.NoError,
			exporters: &[]config.ExporterConf{
				{
					Kind:        fixtures.KindWebhook,
					EndpointURL: httpsGofeatureflagOrgWebhook,
					Secret:      clientSecret,
				},
			},
		},
		{
			name:    "invalid exporter",
			wantErr: assert.Error,
			exporters: &[]config.ExporterConf{
				{
					Kind: "invalid",
				},
				{
					Kind:        fixtures.KindWebhook,
					EndpointURL: httpsGofeatureflagOrgWebhook,
					Secret:      clientSecret,
				},
			},
		},
		{
			name:    "invalid exporters",
			wantErr: assert.Error,
			exporters: &[]config.ExporterConf{
				{

					Kind:        fixtures.KindWebhook,
					EndpointURL: httpsGofeatureflagOrgWebhook,
					Secret:      clientSecret,
				},
				{
					Kind: "invalid",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			proxyConf := config.FlagSet{
				Exporters: tt.exporters,
			}
			r, err := initDataExporters(&proxyConf)
			tt.wantErr(t, err)

			if r != nil {
				nbExp := 0
				if tt.exporters != nil {
					nbExp += len(*tt.exporters)
				}

				if tt.exporter != nil {
					nbExp++
				}

				assert.Len(t, r, nbExp)
			}
		})
	}
}

func Test_initExporter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantType               exporter.CommonExporter
		conf                   *config.ExporterConf
		wantErr                assert.ErrorAssertionFunc
		name                   string
		want                   ffclient.DataExporter
		skipCompleteValidation bool
	}{
		{
			name:    "Convert unknown Exporter",
			wantErr: assert.Error,
			conf: &config.ExporterConf{
				Kind: unknown,
			},
		},
		{
			name:    "Convert WebhookExporter",
			wantErr: assert.NoError,
			conf: &config.ExporterConf{
				Kind:        fixtures.KindWebhook,
				EndpointURL: httpsGofeatureflagOrgWebhook,
				Secret:      clientSecret,
			},
			want: ffclient.DataExporter{
				FlushInterval:    config.DefaultExporter().FlushInterval,
				MaxEventInMemory: config.DefaultExporter().MaxEventInMemory,
				Exporter: &webhookexporter.Exporter{
					EndpointURL: httpsGofeatureflagOrgWebhook,
					Secret:      clientSecret,
					Meta:        nil,
				},
				ExporterEventType: ffclient.FeatureEventExporter,
			},
			wantType: &webhookexporter.Exporter{},
		},
		{
			name:    "Convert FileExporter",
			wantErr: assert.NoError,
			conf: &config.ExporterConf{
				Kind:                    fixtures.KindFile,
				OutputDir:               "/outputfolder/",
				ParquetCompressionCodec: parquet.CompressionCodec_UNCOMPRESSED.String(),
			},
			want: ffclient.DataExporter{
				FlushInterval:    config.DefaultExporter().FlushInterval,
				MaxEventInMemory: config.DefaultExporter().MaxEventInMemory,
				Exporter: &fileexporter.Exporter{
					Format:                  config.DefaultExporter().Format,
					OutputDir:               "/outputfolder/",
					Filename:                config.DefaultExporter().FileName,
					CsvTemplate:             config.DefaultExporter().CsvFormat,
					ParquetCompressionCodec: parquet.CompressionCodec_UNCOMPRESSED.String(),
				},
				ExporterEventType: ffclient.FeatureEventExporter,
			},
			wantType: &fileexporter.Exporter{},
		},
		{
			name:    "Convert LogExporter",
			wantErr: assert.NoError,
			conf: &config.ExporterConf{
				Kind: "log",
			},
			want: ffclient.DataExporter{
				FlushInterval:    config.DefaultExporter().FlushInterval,
				MaxEventInMemory: config.DefaultExporter().MaxEventInMemory,
				Exporter: &logsexporter.Exporter{
					LogFormat: config.DefaultExporter().LogFormat,
				},
				ExporterEventType: ffclient.FeatureEventExporter,
			},
			wantType: &logsexporter.Exporter{},
		},
		{
			name:    "Convert S3Exporter",
			wantErr: assert.NoError,
			conf: &config.ExporterConf{
				Kind:          "s3",
				Bucket:        myBucket,
				Path:          myPath,
				FlushInterval: 10,
			},
			want: ffclient.DataExporter{
				FlushInterval:    10 * time.Millisecond,
				MaxEventInMemory: config.DefaultExporter().MaxEventInMemory,
				Exporter: &s3exporterv2.Exporter{
					Bucket:                  myBucket,
					Format:                  config.DefaultExporter().Format,
					S3Path:                  myPath,
					Filename:                config.DefaultExporter().FileName,
					CsvTemplate:             config.DefaultExporter().CsvFormat,
					ParquetCompressionCodec: config.DefaultExporter().ParquetCompressionCodec,
				},
			},
			wantType:               &s3exporterv2.Exporter{},
			skipCompleteValidation: true,
		},
		{
			name:    "Convert SQSExporter",
			wantErr: assert.NoError,
			conf: &config.ExporterConf{
				Kind:          "sqs",
				QueueURL:      "https://sqs.eu-west-1.amazonaws.com/XXX/test-queue",
				FlushInterval: 10,
			},
			want: ffclient.DataExporter{
				FlushInterval:    10 * time.Millisecond,
				MaxEventInMemory: config.DefaultExporter().MaxEventInMemory,
				Exporter: &sqsexporter.Exporter{
					QueueURL: "https://sqs.eu-west-1.amazonaws.com/XXX/test-queue",
				},
			},
			wantType:               &sqsexporter.Exporter{},
			skipCompleteValidation: true,
		},
		{
			name:    "Convert PubSubExporter",
			wantErr: assert.NoError,
			conf: &config.ExporterConf{
				Kind:      "pubsub",
				ProjectID: fixtures.FakeProjectID,
				Topic:     "fake-topic",
			},
			want: ffclient.DataExporter{
				Exporter: &pubsubexporterv2.Exporter{
					ProjectID: fixtures.FakeProjectID,
					Topic:     "fake-topic",
				},
			},
			wantType:               &pubsubexporterv2.Exporter{},
			skipCompleteValidation: true,
		},
		{
			name:    "Convert BigQueryExporter",
			wantErr: assert.NoError,
			conf: &config.ExporterConf{
				Kind:      "bigquery",
				ProjectID: fixtures.FakeProjectID,
				DatasetID: fixtures.FakeDataset,
			},
			want: ffclient.DataExporter{
				Exporter: &bigqueryexporter.Exporter{
					ProjectID: fixtures.FakeProjectID,
					DatasetID: fixtures.FakeDataset,
				},
			},
			wantType:               &bigqueryexporter.Exporter{},
			skipCompleteValidation: true,
		},
		{
			name:    "Convert GoogleStorageExporter",
			wantErr: assert.NoError,
			conf: &config.ExporterConf{
				Kind:             "googleStorage",
				Bucket:           myBucket,
				Path:             myPath,
				MaxEventInMemory: 1990,
			},
			want: ffclient.DataExporter{
				FlushInterval:    config.DefaultExporter().FlushInterval,
				MaxEventInMemory: 1990,
				Exporter: &gcstorageexporter.Exporter{
					Bucket:                  myBucket,
					Format:                  config.DefaultExporter().Format,
					Path:                    myPath,
					Filename:                config.DefaultExporter().FileName,
					CsvTemplate:             config.DefaultExporter().CsvFormat,
					ParquetCompressionCodec: config.DefaultExporter().ParquetCompressionCodec,
				},
				ExporterEventType: ffclient.FeatureEventExporter,
			},
			wantType: &gcstorageexporter.Exporter{},
		},
		{
			name:    "Convert KafkaExporter",
			wantErr: assert.NoError,
			conf: &config.ExporterConf{
				Kind:             "kafka",
				MaxEventInMemory: 1990,
				Kafka: kafkaexporter.Settings{
					Topic:     "example-topic",
					Addresses: []string{fixtures.AddressOne, fixtures.AddressTwo},
				},
				ExporterEventType: ffclient.FeatureEventExporter,
			},
			want: ffclient.DataExporter{
				FlushInterval:    config.DefaultExporter().FlushInterval,
				MaxEventInMemory: 1990,
				Exporter: &kafkaexporter.Exporter{
					Format: config.DefaultExporter().Format,
					Settings: kafkaexporter.Settings{
						Topic:     "example-topic",
						Addresses: []string{fixtures.AddressOne, fixtures.AddressTwo},
					},
				},
				ExporterEventType: ffclient.FeatureEventExporter,
			},
			wantType: &kafkaexporter.Exporter{},
		},
		{
			name:    "AWS Kinesis Exporter",
			wantErr: assert.NoError,
			conf: &config.ExporterConf{
				Kind:       "kinesis",
				StreamName: "my-stream",
			},
			want: ffclient.DataExporter{
				FlushInterval:    10 * time.Millisecond,
				MaxEventInMemory: config.DefaultExporter().MaxEventInMemory,
				Exporter: &kinesisexporter.Exporter{
					Format: config.DefaultExporter().Format,
					Settings: kinesisexporter.NewSettings(
						kinesisexporter.WithStreamArn("my-stream"),
					),
				},
			},
			wantType:               &kinesisexporter.Exporter{},
			skipCompleteValidation: true,
		},
		{
			name:    "Azure Blob Storage Exporter",
			wantErr: assert.NoError,
			conf: &config.ExporterConf{
				Kind:             "azureBlobStorage",
				Container:        "my-container",
				Path:             myPath,
				MaxEventInMemory: 1990,
			},
			want: ffclient.DataExporter{
				FlushInterval:    config.DefaultExporter().FlushInterval,
				MaxEventInMemory: 1990,
				Exporter: &azureexporter.Exporter{
					Container:               "my-container",
					Format:                  config.DefaultExporter().Format,
					Path:                    myPath,
					Filename:                config.DefaultExporter().FileName,
					CsvTemplate:             config.DefaultExporter().CsvFormat,
					ParquetCompressionCodec: config.DefaultExporter().ParquetCompressionCodec,
				},
				ExporterEventType: ffclient.FeatureEventExporter,
			},
			wantType: &azureexporter.Exporter{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := initDataExporter(tt.conf)
			tt.wantErr(t, err)
			assert.IsType(t, tt.wantType, got.Exporter)

			if err == nil && !tt.skipCompleteValidation {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func Test_initExporter_BigQueryCredentialsAndTrackingEventType(t *testing.T) {
	t.Parallel()

	credentials := `{"type":"service_account","project_id":"fake-project-id"}`

	got, err := initDataExporter(&config.ExporterConf{
		Kind:              config.BigQueryExporter,
		ProjectID:         fixtures.FakeProjectID,
		DatasetID:         fixtures.FakeDataset,
		TableName:         "tracking-events",
		GoogleCredentials: credentials,
		AutoMigrate:       true,
		ExporterEventType: ffclient.TrackingEventExporter,
	})

	require.NoError(t, err)
	assert.Equal(t, ffclient.TrackingEventExporter, got.ExporterEventType)
	bigQueryExporter, ok := got.Exporter.(*bigqueryexporter.Exporter)
	assert.True(t, ok)
	assert.Equal(t, fixtures.FakeProjectID, bigQueryExporter.ProjectID)
	assert.Equal(t, fixtures.FakeDataset, bigQueryExporter.DatasetID)
	assert.Equal(t, "tracking-events", bigQueryExporter.TableName)
	assert.Equal(t, []byte(credentials), bigQueryExporter.GoogleCredentials)
	assert.True(t, bigQueryExporter.AutoMigrate)
}

func Test_initExporter_Redshift(t *testing.T) {
	t.Parallel()

	got, err := initDataExporter(&config.ExporterConf{
		Kind:              config.RedshiftExporter,
		WorkgroupName:     "my-workgroup",
		Database:          "analytics",
		SecretArn:         "arn:aws:secretsmanager:us-east-1:123456789012:secret:goff",
		TableName:         "tracking_events",
		AwsRegion:         "eu-west-3",
		AutoMigrate:       true,
		ExporterEventType: ffclient.TrackingEventExporter,
	})

	require.NoError(t, err)
	assert.Equal(t, ffclient.TrackingEventExporter, got.ExporterEventType)
	redshiftExporter, ok := got.Exporter.(*redshiftexporter.Exporter)
	require.True(t, ok)
	assert.Equal(t, "my-workgroup", redshiftExporter.WorkgroupName)
	assert.Empty(t, redshiftExporter.ClusterIdentifier)
	assert.Equal(t, "analytics", redshiftExporter.Database)
	assert.Equal(t, "arn:aws:secretsmanager:us-east-1:123456789012:secret:goff", redshiftExporter.SecretArn)
	assert.Equal(t, "tracking_events", redshiftExporter.TableName)
	assert.True(t, redshiftExporter.AutoMigrate)
	assert.Equal(t, "eu-west-3", redshiftExporter.AwsConfig.Region)
}

func Test_initNotifier(t *testing.T) {
	t.Parallel()

	type args struct {
		c []config.NotifierConf
	}

	tests := []struct {
		wantErr assert.ErrorAssertionFunc
		name    string
		args    args
		want    []notifier.Notifier
	}{
		{
			name: "valid",
			args: args{
				c: []config.NotifierConf{
					{
						Kind:       config.SlackNotifier,
						WebhookURL: "http:xxxx.xxx",
					},
					{
						Kind:        config.WebhookNotifier,
						EndpointURL: "http:yyyy.yyy",
					},
					{
						Kind:       config.MicrosoftTeamsNotifier,
						WebhookURL: "http:zzzz.zzz",
					},
					{
						Kind:       config.DiscordNotifier,
						WebhookURL: "http:aaaa.aaa",
					},
				},
			},
			want: []notifier.Notifier{
				&slacknotifier.Notifier{SlackWebhookURL: "http:xxxx.xxx"},
				&webhooknotifier.Notifier{EndpointURL: "http:yyyy.yyy"},
				&microsoftteamsnotifier.Notifier{MicrosoftTeamsWebhookURL: "http:zzzz.zzz"},
				&discordnotifier.Notifier{DiscordWebhookURL: "http:aaaa.aaa"},
			},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := initNotifier(tt.args.c, slog.New(slog.DiscardHandler))
			if !tt.wantErr(t, err, fmt.Sprintf("initNotifier(%v)", tt.args.c)) {
				return
			}

			assert.Equalf(t, tt.want, got, "initNotifier(%v)", tt.args.c)
		})
	}
}

func TestNewGoFeatureFlagClient_ProxyConfNil(t *testing.T) {
	t.Parallel()

	// Create a logger for testing
	logger := slog.New(slog.DiscardHandler)

	// Call NewGoFeatureFlagClient with nil proxyConf
	goff, err := NewGoFeatureFlagClient(nil, logger, nil)

	// Assert that the function returns nil and an error
	assert.Nil(t, goff, "Expected GoFeatureFlag client to be nil when proxyConf is nil")
	assert.EqualError(
		t,
		err,
		"proxy config is empty",
		"Expected error message to indicate empty proxy config",
	)
}

func testKafkaSCRAMSettings(mechanism sarama.SASLMechanism) kafkaexporter.Settings {
	return kafkaexporter.Settings{
		Topic:     myKafkaTopic,
		Addresses: []string{fixtures.AddressOne, fixtures.AddressTwo},
		Config: &sarama.Config{
			//nolint:govet // fieldalignment: the type must be identical to the anonymous type of sarama.Config.Net
			Net: struct {
				MaxOpenRequests                  int
				DialTimeout                      time.Duration
				ReadTimeout                      time.Duration
				WriteTimeout                     time.Duration
				ResolveCanonicalBootstrapServers bool
				//nolint:govet // fieldalignment: anonymous struct type must match sarama.Config.Net.TLS
				TLS struct {
					Enable bool
					Config *tls.Config
				}
				//nolint:govet // fieldalignment: anonymous struct type must match sarama.Config.Net.SASL
				SASL struct {
					Enable                   bool
					Mechanism                sarama.SASLMechanism
					Version                  int16
					Handshake                bool
					AuthIdentity             string
					User                     string
					Password                 string
					SCRAMAuthzID             string
					SCRAMClientGeneratorFunc func() sarama.SCRAMClient
					TokenProvider            sarama.AccessTokenProvider
					GSSAPI                   sarama.GSSAPIConfig
				}
				KeepAlive time.Duration
				LocalAddr net.Addr
				//nolint:govet // fieldalignment: anonymous struct type must match sarama.Config.Net.Proxy
				Proxy struct {
					Enable bool
					Dialer proxy.Dialer
				}
			}{
				//nolint:govet // fieldalignment: anonymous struct type must match sarama.Config.Net.SASL
				SASL: struct {
					Enable                   bool
					Mechanism                sarama.SASLMechanism
					Version                  int16
					Handshake                bool
					AuthIdentity             string
					User                     string
					Password                 string
					SCRAMAuthzID             string
					SCRAMClientGeneratorFunc func() sarama.SCRAMClient
					TokenProvider            sarama.AccessTokenProvider
					GSSAPI                   sarama.GSSAPIConfig
				}{
					Enable:    true,
					Mechanism: mechanism,
					User:      todoUpper,
					Password:  todoUpper,
				},
			},
		},
	}
}

func TestSetKafkaConfig(t *testing.T) {
	t.Parallel()

	t.Run(
		"should have a SCRAMClientGeneratorFunc if SCRAM is enabled and use SCRAM-SHA-512 mechanism",
		func(t *testing.T) {
			t.Parallel()

			settings := testKafkaSCRAMSettings(scramSha512)
			kafkaConfig, err := setKafkaConfig(settings)
			require.NoError(t, err)

			require.NotNil(t, kafkaConfig.Net.SASL.SCRAMClientGeneratorFunc)
			client, ok := kafkaConfig.Net.SASL.SCRAMClientGeneratorFunc().(*kafka.XDGSCRAMClient)
			require.True(t, ok)
			assert.Equal(t, sha512.Size, client.HashGeneratorFcn().Size())
		},
	)

	t.Run(
		"should have a SCRAMClientGeneratorFunc if SCRAM is enabled and use SCRAM-SHA-256 mechanism",
		func(t *testing.T) {
			t.Parallel()

			settings := testKafkaSCRAMSettings("SCRAM-SHA-256")
			kafkaConfig, err := setKafkaConfig(settings)
			require.NoError(t, err)

			require.NotNil(t, kafkaConfig.Net.SASL.SCRAMClientGeneratorFunc)
			client, ok := kafkaConfig.Net.SASL.SCRAMClientGeneratorFunc().(*kafka.XDGSCRAMClient)
			require.True(t, ok)
			assert.Equal(t, sha256.Size, client.HashGeneratorFcn().Size())
		},
	)

	t.Run(
		"should not have a SCRAMClientGeneratorFunc if SCRAM is enabled and use an unknown mechanism",
		func(t *testing.T) {
			t.Parallel()

			settings := testKafkaSCRAMSettings("UNKNONW-MECHANISM")
			kafkaConfig, err := setKafkaConfig(settings)
			require.NoError(t, err)
			assert.Nil(t, kafkaConfig.Net.SASL.SCRAMClientGeneratorFunc)
		},
	)

	t.Run("should return a valid sarama.Config if settings are nil", func(t *testing.T) {
		t.Parallel()

		settings := kafkaexporter.Settings{
			Topic:     myKafkaTopic,
			Addresses: []string{fixtures.AddressOne, fixtures.AddressTwo},
			Config: &sarama.Config{
				Version: sarama.V2_1_0_0,
			},
		}
		// We expect am error because the settings are nil
		require.Error(t, settings.Config.Validate())

		kafkaConfig, err := setKafkaConfig(settings)
		require.NoError(t, err)

		// after calling setKafkaConfig, the settings should be valid because they are merged with the default config.
		require.NoError(t, kafkaConfig.Config.Validate())
		assert.Equal(t, myKafkaTopic, kafkaConfig.Topic)
		assert.Equal(t, []string{fixtures.AddressOne, fixtures.AddressTwo}, kafkaConfig.Addresses)
		assert.True(t, kafkaConfig.Config.Producer.Return.Successes)
		assert.True(t, kafkaConfig.Config.Producer.Return.Errors)
	})

	t.Run("settings of the operator win over the defaults of sarama", func(t *testing.T) {
		t.Parallel()

		defaults := sarama.NewConfig()

		tests := []struct {
			want any
			user func(c *sarama.Config)
			// got reads the setting from the merged configuration, want is its expected value.
			got  func(c *sarama.Config) any
			name string
		}{
			{
				name: "DialTimeout", user: func(c *sarama.Config) { c.Net.DialTimeout = 5 * time.Second },
				got: func(c *sarama.Config) any { return c.Net.DialTimeout }, want: 5 * time.Second,
			},
			{
				name: "ReadTimeout", user: func(c *sarama.Config) { c.Net.ReadTimeout = 6 * time.Second },
				got: func(c *sarama.Config) any { return c.Net.ReadTimeout }, want: 6 * time.Second,
			},
			{
				name: "Producer.Retry.Max", user: func(c *sarama.Config) { c.Producer.Retry.Max = 7 },
				got: func(c *sarama.Config) any { return c.Producer.Retry.Max }, want: 7,
			},
			{
				name: "Producer.Retry.Backoff", user: func(c *sarama.Config) { c.Producer.Retry.Backoff = time.Second },
				got: func(c *sarama.Config) any { return c.Producer.Retry.Backoff }, want: time.Second,
			},
			{
				name: "Producer.Flush.Messages", user: func(c *sarama.Config) { c.Producer.Flush.Messages = 9 },
				got: func(c *sarama.Config) any { return c.Producer.Flush.Messages }, want: 9,
			},
			{
				name: "Producer.MaxMessageBytes", user: func(c *sarama.Config) { c.Producer.MaxMessageBytes = 2048 },
				got: func(c *sarama.Config) any { return c.Producer.MaxMessageBytes }, want: 2048,
			},
			{
				name: "Producer.RequiredAcks",
				user: func(c *sarama.Config) { c.Producer.RequiredAcks = sarama.WaitForLocal },
				got:  func(c *sarama.Config) any { return c.Producer.RequiredAcks },
				want: sarama.WaitForLocal,
			},
			{
				name: "ClientID", user: func(c *sarama.Config) { c.ClientID = "mine" },
				got: func(c *sarama.Config) any { return c.ClientID }, want: "mine",
			},
			{
				name: "Version", user: func(c *sarama.Config) { c.Version = sarama.V2_1_0_0 },
				got: func(c *sarama.Config) any { return c.Version }, want: sarama.V2_1_0_0,
			},
			{
				name: "Metadata.Retry.Max", user: func(c *sarama.Config) { c.Metadata.Retry.Max = 11 },
				got: func(c *sarama.Config) any { return c.Metadata.Retry.Max }, want: 11,
			},
			{
				// a zero value cannot be told from an unset one: the default of sarama is kept
				name: "an unset ClientID keeps the default", user: func(_ *sarama.Config) {},
				got: func(c *sarama.Config) any { return c.ClientID }, want: defaults.ClientID,
			},
			{
				name: "an unset DialTimeout keeps the default", user: func(_ *sarama.Config) {},
				got: func(c *sarama.Config) any { return c.Net.DialTimeout }, want: defaults.Net.DialTimeout,
			},
			{
				name: "an unset Version keeps the default", user: func(_ *sarama.Config) {},
				got: func(c *sarama.Config) any { return c.Version }, want: defaults.Version,
			},
			{
				name: "an unset Producer.Retry.Max keeps the default", user: func(_ *sarama.Config) {},
				got: func(c *sarama.Config) any { return c.Producer.Retry.Max }, want: defaults.Producer.Retry.Max,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				userConfig := &sarama.Config{}
				tt.user(userConfig)

				kafkaConfig, err := setKafkaConfig(kafkaexporter.Settings{Topic: "t", Config: userConfig})
				require.NoError(t, err)

				assert.Equal(t, tt.want, tt.got(kafkaConfig.Config))
			})
		}
	})

	t.Run("should return a valid sarama.Config with specific settings set and value should be"+
		" merged with default kafka config", func(t *testing.T) {
		t.Parallel()

		settings := testKafkaSCRAMSettings(scramSha512)
		// We expect am error because the settings are nil
		require.Error(t, settings.Config.Validate())

		kafkaConfig, err := setKafkaConfig(settings)
		require.NoError(t, err)

		// after calling setKafkaConfig, the settings should be valid because they are merged with the default config.
		require.NoError(t, kafkaConfig.Config.Validate())
		assert.Equal(t, myKafkaTopic, kafkaConfig.Topic)
		assert.Equal(t, []string{fixtures.AddressOne, fixtures.AddressTwo}, kafkaConfig.Addresses)
		assert.True(t, kafkaConfig.Config.Producer.Return.Successes)
		assert.True(t, kafkaConfig.Config.Producer.Return.Errors)
		assert.True(t, kafkaConfig.Config.Net.SASL.Enable)
		assert.Equal(t, sarama.SASLMechanism(scramSha512), kafkaConfig.Config.Net.SASL.Mechanism)
		assert.Equal(t, todoUpper, kafkaConfig.Config.Net.SASL.User)
		assert.Equal(t, todoUpper, kafkaConfig.Config.Net.SASL.Password)
	})

	t.Run("should return a nil config", func(t *testing.T) {
		t.Parallel()

		settings := kafkaexporter.Settings{
			Topic:     myKafkaTopic,
			Addresses: []string{fixtures.AddressOne, fixtures.AddressTwo},
		}
		kafkaConfig, err := setKafkaConfig(settings)
		require.NoError(t, err)
		assert.Nil(t, kafkaConfig.Config)
	})

	t.Run("should return a config usable by a sync producer if a custom config is set", func(t *testing.T) {
		t.Parallel()

		settings := kafkaexporter.Settings{
			Topic:     myKafkaTopic,
			Addresses: []string{fixtures.AddressOne, fixtures.AddressTwo},
			Config: &sarama.Config{
				Version: sarama.V2_1_0_0,
			},
		}
		kafkaConfig, err := setKafkaConfig(settings)
		require.NoError(t, err)

		// the kafka exporter is using a sarama.SyncProducer, and it refuses to start if one of those is false.
		assert.True(t, kafkaConfig.Producer.Return.Errors)
		assert.True(t, kafkaConfig.Producer.Return.Successes)
	})
}

func Test_initLeveledLogger_FlagsetAttribute(t *testing.T) {
	t.Parallel()

	t.Run("verify flagset attribute logic", func(t *testing.T) {
		t.Parallel()

		// Test the logic directly by checking the conditions
		tests := []struct {
			name        string
			flagsetName string
			wantLog     string
		}{
			{
				name:        "default flagset name should not add attribute",
				flagsetName: stringsx.DefaultFlagSetName,
				wantLog:     "{\"msg\":\"test message\"}\n",
			},
			{
				name:        "empty flagset name should not add attribute",
				flagsetName: "",
				wantLog:     "{\"msg\":\"test message\"}\n",
			},
			{
				name:        "custom flagset name should add attribute",
				flagsetName: "my-custom-flagset",
				wantLog:     "{\"msg\":\"test message\",\"flagset\":\"my-custom-flagset\"}\n",
			},
			{
				name:        "another custom flagset name should add attribute",
				flagsetName: "production-flags",
				wantLog:     "{\"msg\":\"test message\",\"flagset\":\"production-flags\"}\n",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				flagset := &config.FlagSet{
					Name: tt.flagsetName,
				}
				// Create a buffer to capture log output
				var logBuffer bytes.Buffer

				// a logger that writes the message and the attributes only, to our buffer
				baseLogger := slog.New(slog.NewJSONHandler(&logBuffer, &slog.HandlerOptions{
					ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
						if len(groups) == 0 && (a.Key == slog.TimeKey || a.Key == slog.LevelKey) {
							return slog.Attr{}
						}

						return a
					},
				}))

				slogLogger := initLeveledLogger(flagset, baseLogger)
				assert.NotNil(t, slogLogger, "initLeveledLogger should return a non-nil logger")

				slogLogger.Info("test message")

				logOutput := logBuffer.String()

				assert.Equal(t, tt.wantLog, logOutput)
			})
		}
	})
}

func Test_newGoFeatureFlagClient_ReleasesSharedPollerRetrieverOnFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		s3         *fakeS3
		mod        func(fs *config.FlagSet)
		name       string
		wantGroups int
	}{
		{
			name: "invalid exporter",
			s3:   &fakeS3{content: []byte("{}")},
			mod: func(fs *config.FlagSet) {
				fs.Exporter = &config.ExporterConf{Kind: unknown}
			},
			wantGroups: 0,
		},
		{
			name:       "first retrieve fails",
			s3:         &fakeS3{listErr: errAccessDenied},
			mod:        func(*config.FlagSet) {},
			wantGroups: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				reg := newFakeRegistry(t, tt.s3, &atomic.Int32{})
				fs := s3FlagSet("a", "b1", pAYaml)
				tt.mod(&fs)
				_, err := createGoFeatureFlagClient(
					&fs,
					slog.New(slog.DiscardHandler),
					nil,
					withSharedBucketRegistry(reg),
				)
				require.Error(t, err)
				assert.Equal(t, tt.wantGroups, reg.groupCount(), "the retriever must have left its group")
			})
		})
	}
}

func Test_firstSharedRetriever(t *testing.T) {
	t.Parallel()
	reg := newFakeRegistry(t, &fakeS3{}, &atomic.Int32{})
	fs := s3FlagSet("a", "b1", pAYaml)
	r, err := reg.retrieverFor(&fs)
	require.NoError(t, err)
	file, err := retrieverinit.Retriever(&retrieverconf.RetrieverConf{
		Kind: retrieverconf.FileRetriever, Path: testdataSemaphoreConfigurationFlags,
	})
	require.NoError(t, err)

	tests := []struct {
		name       string
		retrievers []retriever.Retriever
		want       bool
	}{
		{name: "no retriever"},
		{name: "another kind of retriever", retrievers: []retriever.Retriever{file.Retriever}},
		{name: "shared-poller retriever", retrievers: []retriever.Retriever{r}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := firstSharedRetriever(tt.retrievers)
			assert.Equal(t, tt.want, ok)
			assert.Equal(t, tt.want, got != nil)
		})
	}

	rel := r
	require.NoError(t, rel.Shutdown(t.Context()))
}

// Not parallel: the test uses t.Setenv.
//
// T.Setenv: the AWS SDK default config chain (third-party code) reads the AWS_* variables from the process environment.
func Test_newGoFeatureFlagClient_sharedPollerWithoutRegistry(t *testing.T) {
	// no S3 is reachable: the point is that the flagset gets a private registry and releases it
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	t.Setenv("AWS_ENDPOINT_URL", "http://127.0.0.1:1")
	t.Setenv("AWS_MAX_ATTEMPTS", "1")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CONFIG_FILE", "/nonexistent/config")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/nonexistent/credentials")

	fs := s3FlagSet("a", "b1", pAYaml)
	fs.PollingInterval, fs.EnablePollingJitter = 5000, true
	got, err := NewGoFeatureFlagClient(&fs, slog.New(slog.DiscardHandler), nil)
	assert.Nil(t, got)
	require.ErrorContains(t, err, `unable to list S3 bucket "b1"`)
}

func Test_exporterCleanQueueInterval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		flagset  time.Duration
		fallback time.Duration
		want     time.Duration
	}{
		{name: "unset everywhere lets the engine use its default", want: 0},
		{name: "flagset value", flagset: 5 * time.Second, want: 5 * time.Second},
		{name: "top-level fallback", fallback: 30 * time.Second, want: 30 * time.Second},
		{
			name:     "flagset overrides the fallback",
			flagset:  5 * time.Second,
			fallback: 30 * time.Second,
			want:     5 * time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fs := &config.FlagSet{ExporterCleanQueueInterval: tt.flagset}

			var o clientOptions
			withDefaultExporterCleanQueueInterval(tt.fallback)(&o)
			assert.Equal(t, tt.want, exporterCleanQueueInterval(fs, &o))
		})
	}
}

func Test_newGoFeatureFlagClient_forwardsExporterCleanQueueInterval(t *testing.T) {
	t.Parallel()

	rtr := &retrieverconf.RetrieverConf{Kind: fixtures.KindFile, Path: testdataSemaphoreConfigurationFlags}

	tests := []struct {
		name     string
		flagset  time.Duration
		fallback time.Duration
		want     time.Duration
	}{
		{name: "unset", want: 0},
		{name: "flagset value", flagset: 5 * time.Second, fallback: 30 * time.Second, want: 5 * time.Second},
		{name: "fallback", fallback: 30 * time.Second, want: 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fs := &config.FlagSet{Name: "fs",
				Retriever: rtr, ExporterCleanQueueInterval: tt.flagset}

			var got time.Duration

			client, err := createGoFeatureFlagClient(fs, slog.New(slog.DiscardHandler), nil,
				withDefaultExporterCleanQueueInterval(tt.fallback),
				withClientFactory(func(c *ffclient.Config) (*ffclient.GoFeatureFlag, error) {
					got = c.ExporterCleanQueueInterval

					return ffclient.New(c)
				}))
			require.NoError(t, err)
			t.Cleanup(client.Close)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_initRetrieversWithRegistry_SharesThePostgresPoolOfADefaultClient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		uris            []string
		wantSharedPools bool
	}{
		{
			name:            "two retrievers on the same database",
			uris:            []string{"postgres://localhost/db", "postgres://localhost/db"},
			wantSharedPools: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			retrievers := make([]retrieverconf.RetrieverConf, 0, len(tt.uris))
			for _, uri := range tt.uris {
				retrievers = append(retrievers, retrieverconf.RetrieverConf{
					Kind: retrieverconf.PostgreSQLRetriever, URI: uri, Table: fixtures.FlagsDir,
				})
			}

			var opts clientOptions
			opts.ensurePostgresPools()

			got, err := initRetrieversWithRegistry(
				&config.FlagSet{Retrievers: &retrievers},
				nil,
				opts.retrieverOptions()...)

			require.NoError(t, err)
			require.Len(t, got, len(tt.uris))
			first, ok := got[0].(*postgresqlretriever.Retriever)
			require.True(t, ok)
			second, ok := got[1].(*postgresqlretriever.Retriever)
			require.True(t, ok)
			require.NotNil(t, first.Pools)
			assert.Equal(
				t,
				tt.wantSharedPools,
				first.Pools == second.Pools,
				"retrievers of one client share their pools",
			)
		})
	}
}
