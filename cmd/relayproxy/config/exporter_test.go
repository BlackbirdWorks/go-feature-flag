package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
)

const (
	analytics                          = "analytics"
	bigquery                           = "bigquery"
	fakeProjectID                      = "fake-project-id"
	flagVariationHostnameTimestamp     = "flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}-custom-file"
	goffWriter                         = "goff_writer"
	googleStorage                      = "googleStorage"
	json2                              = "json"
	kindContextKindUserKeyCreationDate = "{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}}\n"
	myCluster                          = "my-cluster"
	myWorkgroup                        = "my-workgroup"
	pubsub                             = "pubsub"
	redshift                           = "redshift"
	testbucket                         = "testbucket"
)

type exporterConfFields struct {
	Kind                    string
	OutputDir               string
	Format                  string
	Filename                string
	CsvTemplate             string
	Bucket                  string
	Path                    string
	EndpointURL             string
	Secret                  string
	Meta                    map[string]string
	ParquetCompressionCodec string
	QueueURL                string
	ProjectID               string
	DatasetID               string
	Topic                   string
	StreamName              string
	Container               string
	AccountName             string
	Database                string
	ClusterIdentifier       string
	WorkgroupName           string
	TableName               string
	DBUser                  string
	SecretArn               string
}

type exporterConfValidCase struct {
	fields   exporterConfFields
	name     string
	errValue string
	wantErr  bool
}

func runExporterConfValidTests(t *testing.T, tests []exporterConfValidCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &config.ExporterConf{
				Kind:                    config.ExporterKind(tt.fields.Kind),
				OutputDir:               tt.fields.OutputDir,
				Format:                  tt.fields.Format,
				Filename:                tt.fields.Filename,
				CsvTemplate:             tt.fields.CsvTemplate,
				Bucket:                  tt.fields.Bucket,
				Path:                    tt.fields.Path,
				EndpointURL:             tt.fields.EndpointURL,
				Secret:                  tt.fields.Secret,
				Meta:                    tt.fields.Meta,
				ParquetCompressionCodec: tt.fields.ParquetCompressionCodec,
				QueueURL:                tt.fields.QueueURL,
				ProjectID:               tt.fields.ProjectID,
				DatasetID:               tt.fields.DatasetID,
				Topic:                   tt.fields.Topic,
				StreamName:              tt.fields.StreamName,
				AccountName:             tt.fields.AccountName,
				Container:               tt.fields.Container,
				Database:                tt.fields.Database,
				ClusterIdentifier:       tt.fields.ClusterIdentifier,
				WorkgroupName:           tt.fields.WorkgroupName,
				TableName:               tt.fields.TableName,
				DBUser:                  tt.fields.DBUser,
				SecretArn:               tt.fields.SecretArn,
			}
			err := cfg.IsValid()
			assert.Equal(t, tt.wantErr, err != nil)

			if tt.wantErr {
				assert.Equal(t, tt.errValue, err.Error())
			}
		})
	}
}

func TestExporterConf_IsValid(t *testing.T) {
	t.Parallel()

	runExporterConfValidTests(t, []exporterConfValidCase{
		{
			name:     "no fields",
			fields:   exporterConfFields{},
			wantErr:  true,
			errValue: "invalid exporter: kind \"\" is not supported",
		},
		{
			name: "invalid kind",
			fields: exporterConfFields{
				Kind: invalid2,
			},
			wantErr:  true,
			errValue: "invalid exporter: kind \"invalid\" is not supported",
		},
		{
			name: "kind file without outputDir",
			fields: exporterConfFields{
				Kind: file2,
			},
			wantErr:  true,
			errValue: "invalid exporter: no \"outputDir\" property found for kind \"file\"",
		},
		{
			name: "kind s3 without bucket",
			fields: exporterConfFields{
				Kind: "s3",
			},
			wantErr:  true,
			errValue: "invalid exporter: no \"bucket\" property found for kind \"s3\"",
		},
		{
			name: "kind kinesis without stream",
			fields: exporterConfFields{
				Kind: "kinesis",
			},
			wantErr:  true,
			errValue: "invalid exporter: no \"streamArn\" or \"streamName\" property found for kind \"kinesis\"",
		},
		{
			name: "kind googleStorage without bucket",
			fields: exporterConfFields{
				Kind: googleStorage,
			},
			wantErr:  true,
			errValue: "invalid exporter: no \"bucket\" property found for kind \"googleStorage\"",
		},
		{
			name: "kind azureBlobStorage without container",
			fields: exporterConfFields{
				Kind:        "azureBlobStorage",
				Container:   "",
				AccountName: "MyAccount",
			},
			wantErr:  true,
			errValue: "invalid exporter: no \"container\" property found for kind \"azureBlobStorage\"",
		},
		{
			name: "kind azureBlobStorage without accountName",
			fields: exporterConfFields{
				Kind:        "azureBlobStorage",
				Container:   "MyContainer",
				AccountName: "",
			},
			wantErr:  true,
			errValue: "invalid exporter: no \"accountName\" property found for kind \"azureBlobStorage\"",
		},
		{
			name: "kind webhook without bucket",
			fields: exporterConfFields{
				Kind: webhook,
			},
			wantErr:  true,
			errValue: "invalid exporter: no \"endpointUrl\" property found for kind \"webhook\"",
		},
		{
			name: "kind webhook valid",
			fields: exporterConfFields{
				Kind:        webhook,
				EndpointURL: httpTestingwebhookComTest,
				Secret:      secretForSigning,
				Meta: map[string]string{
					extraInfo2: info2,
				},
			},
			wantErr: false,
		},
		{
			name: "kind file valid",
			fields: exporterConfFields{
				Kind:        file2,
				OutputDir:   "/tmp/",
				Format:      json2,
				Filename:    flagVariationHostnameTimestamp,
				CsvTemplate: kindContextKindUserKeyCreationDate,
			},
			wantErr: false,
		},
		{
			name: "kind log valid",
			fields: exporterConfFields{
				Kind:   log2,
				Format: "[{{ .FormattedDate}}] user=\"{{ .UserKey}}\", flag=\"{{ .Key}}\", value=\"{{ .Value}}\"",
			},
			wantErr: false,
		},
		{
			name: "kind s3 valid",
			fields: exporterConfFields{
				Kind:        "s3",
				Bucket:      testbucket,
				CsvTemplate: kindContextKindUserKeyCreationDate,
				Filename:    flagVariationHostnameTimestamp,
				Format:      json2,
				Path:        "/here/",
			},
			wantErr: false,
		},
		{
			name: "kind googleStorage valid",
			fields: exporterConfFields{
				Kind:        googleStorage,
				Bucket:      testbucket,
				CsvTemplate: kindContextKindUserKeyCreationDate,
				Filename:    flagVariationHostnameTimestamp,
				Format:      json2,
				Path:        "/here/",
			},
			wantErr: false,
		},
		{
			name: "invalid parquetCompressionCodec",
			fields: exporterConfFields{
				Kind:                    googleStorage,
				Bucket:                  testbucket,
				Format:                  "parquet",
				ParquetCompressionCodec: invalid2,
			},
			wantErr:  true,
			errValue: "invalid exporter: \"parquetCompressionCodec\" err: not a valid CompressionCodec string",
		},
	})
}

func TestExporterConf_IsValid_CloudAndWarehouse(t *testing.T) {
	t.Parallel()

	runExporterConfValidTests(t, []exporterConfValidCase{
		{
			name: "kind SQS valid",
			fields: exporterConfFields{
				Kind:     "sqs",
				QueueURL: "https://sqs.eu-west-1.amazonaws.com/XXX/test-queue",
			},
			wantErr: false,
		},
		{
			name: "kind Kinesis valid",
			fields: exporterConfFields{
				Kind:       "kinesis",
				StreamName: "test-stream",
			},
			wantErr: false,
		},
		{
			name: "kind SQS with queueURL",
			fields: exporterConfFields{
				Kind:     "sqs",
				QueueURL: "",
			},
			wantErr:  true,
			errValue: "invalid exporter: no \"queueUrl\" property found for kind \"sqs\"",
		},
		{
			name: "kind PubSub valid",
			fields: exporterConfFields{
				Kind:      pubsub,
				ProjectID: fakeProjectID,
				Topic:     "fake-topic",
			},
			wantErr: false,
		},
		{
			name: "kind PubSub without project id",
			fields: exporterConfFields{
				Kind:  pubsub,
				Topic: "fake-topic",
			},
			wantErr:  true,
			errValue: "invalid exporter: \"projectID\" and \"topic\" are required for kind \"pubsub\"",
		},
		{
			name: "kind PubSub without topic",
			fields: exporterConfFields{
				Kind:      pubsub,
				ProjectID: fakeProjectID,
			},
			wantErr:  true,
			errValue: "invalid exporter: \"projectID\" and \"topic\" are required for kind \"pubsub\"",
		},
		{
			name: "kind BigQuery valid",
			fields: exporterConfFields{
				Kind:      bigquery,
				ProjectID: fakeProjectID,
				DatasetID: "fake-dataset",
			},
			wantErr: false,
		},
		{
			name: "kind BigQuery without project id",
			fields: exporterConfFields{
				Kind:      bigquery,
				DatasetID: "fake-dataset",
			},
			wantErr:  true,
			errValue: "invalid exporter: \"projectID\" and \"datasetID\" are required for kind \"bigquery\"",
		},
		{
			name: "kind BigQuery without dataset id",
			fields: exporterConfFields{
				Kind:      bigquery,
				ProjectID: fakeProjectID,
			},
			wantErr:  true,
			errValue: "invalid exporter: \"projectID\" and \"datasetID\" are required for kind \"bigquery\"",
		},
		{
			name: "kind Redshift valid with cluster",
			fields: exporterConfFields{
				Kind:              redshift,
				Database:          analytics,
				ClusterIdentifier: myCluster,
			},
			wantErr: false,
		},
		{
			name: "kind Redshift valid with workgroup",
			fields: exporterConfFields{
				Kind:          redshift,
				Database:      analytics,
				WorkgroupName: myWorkgroup,
			},
			wantErr: false,
		},
		{
			name: "kind Redshift valid with schema and table name",
			fields: exporterConfFields{
				Kind:              redshift,
				Database:          analytics,
				ClusterIdentifier: myCluster,
				TableName:         "goff.feature_flag_evaluations",
			},
			wantErr: false,
		},
		{
			name: "kind Redshift with invalid table name",
			fields: exporterConfFields{
				Kind:              redshift,
				Database:          analytics,
				ClusterIdentifier: myCluster,
				TableName:         "events; DROP TABLE users",
			},
			wantErr: true,
			errValue: "invalid exporter: \"tableName\" \"events; DROP TABLE users\" is not a valid table name for kind \"r" +
				"edshift\"",
		},
		{
			name: "kind Redshift without database",
			fields: exporterConfFields{
				Kind:              redshift,
				ClusterIdentifier: myCluster,
			},
			wantErr:  true,
			errValue: "invalid exporter: no \"database\" property found for kind \"redshift\"",
		},
		{
			name: "kind Redshift without cluster or workgroup",
			fields: exporterConfFields{
				Kind:     redshift,
				Database: analytics,
			},
			wantErr: true,
			errValue: "invalid exporter: exactly one of \"clusterIdentifier\" or \"workgroupName\" is required for kind \"" +
				"redshift\"",
		},
		{
			name: "kind Redshift with both cluster and workgroup",
			fields: exporterConfFields{
				Kind:              redshift,
				Database:          analytics,
				ClusterIdentifier: myCluster,
				WorkgroupName:     myWorkgroup,
			},
			wantErr: true,
			errValue: "invalid exporter: exactly one of \"clusterIdentifier\" or \"workgroupName\" is required for kind \"" +
				"redshift\"",
		},
		{
			name: "kind Redshift valid with cluster and dbUser",
			fields: exporterConfFields{
				Kind:              redshift,
				Database:          analytics,
				ClusterIdentifier: myCluster,
				DBUser:            goffWriter,
			},
			wantErr: false,
		},
		{
			name: "kind Redshift valid with workgroup and secretArn",
			fields: exporterConfFields{
				Kind:          redshift,
				Database:      analytics,
				WorkgroupName: myWorkgroup,
				SecretArn:     "arn:aws:secretsmanager:us-east-1:123456789012:secret:goff",
			},
			wantErr: false,
		},
		{
			name: "kind Redshift with dbUser and workgroup",
			fields: exporterConfFields{
				Kind:          redshift,
				Database:      analytics,
				WorkgroupName: myWorkgroup,
				DBUser:        goffWriter,
			},
			wantErr:  true,
			errValue: "invalid exporter: \"dbUser\" cannot be used with \"workgroupName\" for kind \"redshift\"",
		},
		{
			name: "kind Redshift with dbUser and secretArn",
			fields: exporterConfFields{
				Kind:              redshift,
				Database:          analytics,
				ClusterIdentifier: myCluster,
				DBUser:            goffWriter,
				SecretArn:         "arn:aws:secretsmanager:us-east-1:123456789012:secret:goff",
			},
			wantErr:  true,
			errValue: "invalid exporter: \"dbUser\" and \"secretArn\" are mutually exclusive for kind \"redshift\"",
		},
	})
}
