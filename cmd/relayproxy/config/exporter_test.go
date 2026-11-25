package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
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
				Kind: "invalid",
			},
			wantErr:  true,
			errValue: "invalid exporter: kind \"invalid\" is not supported",
		},
		{
			name: "kind file without outputDir",
			fields: exporterConfFields{
				Kind: "file",
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
				Kind: "googleStorage",
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
				Kind: "webhook",
			},
			wantErr:  true,
			errValue: "invalid exporter: no \"endpointUrl\" property found for kind \"webhook\"",
		},
		{
			name: "kind webhook valid",
			fields: exporterConfFields{
				Kind:        "webhook",
				EndpointURL: "http://testingwebhook.com/test/",
				Secret:      "secret-for-signing",
				Meta: map[string]string{
					"extraInfo": "info",
				},
			},
			wantErr: false,
		},
		{
			name: "kind file valid",
			fields: exporterConfFields{
				Kind:        "file",
				OutputDir:   "/tmp/",
				Format:      "json",
				Filename:    "flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}-custom-file",
				CsvTemplate: "{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}}\n",
			},
			wantErr: false,
		},
		{
			name: "kind log valid",
			fields: exporterConfFields{
				Kind:   "log",
				Format: "[{{ .FormattedDate}}] user=\"{{ .UserKey}}\", flag=\"{{ .Key}}\", value=\"{{ .Value}}\"",
			},
			wantErr: false,
		},
		{
			name: "kind s3 valid",
			fields: exporterConfFields{
				Kind:        "s3",
				Bucket:      "testbucket",
				CsvTemplate: "{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}}\n",
				Filename:    "flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}-custom-file",
				Format:      "json",
				Path:        "/here/",
			},
			wantErr: false,
		},
		{
			name: "kind googleStorage valid",
			fields: exporterConfFields{
				Kind:        "googleStorage",
				Bucket:      "testbucket",
				CsvTemplate: "{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}}\n",
				Filename:    "flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}-custom-file",
				Format:      "json",
				Path:        "/here/",
			},
			wantErr: false,
		},
		{
			name: "invalid parquetCompressionCodec",
			fields: exporterConfFields{
				Kind:                    "googleStorage",
				Bucket:                  "testbucket",
				Format:                  "parquet",
				ParquetCompressionCodec: "invalid",
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
				Kind:      "pubsub",
				ProjectID: "fake-project-id",
				Topic:     "fake-topic",
			},
			wantErr: false,
		},
		{
			name: "kind PubSub without project id",
			fields: exporterConfFields{
				Kind:  "pubsub",
				Topic: "fake-topic",
			},
			wantErr:  true,
			errValue: "invalid exporter: \"projectID\" and \"topic\" are required for kind \"pubsub\"",
		},
		{
			name: "kind PubSub without topic",
			fields: exporterConfFields{
				Kind:      "pubsub",
				ProjectID: "fake-project-id",
			},
			wantErr:  true,
			errValue: "invalid exporter: \"projectID\" and \"topic\" are required for kind \"pubsub\"",
		},
		{
			name: "kind BigQuery valid",
			fields: exporterConfFields{
				Kind:      "bigquery",
				ProjectID: "fake-project-id",
				DatasetID: "fake-dataset",
			},
			wantErr: false,
		},
		{
			name: "kind BigQuery without project id",
			fields: exporterConfFields{
				Kind:      "bigquery",
				DatasetID: "fake-dataset",
			},
			wantErr:  true,
			errValue: "invalid exporter: \"projectID\" and \"datasetID\" are required for kind \"bigquery\"",
		},
		{
			name: "kind BigQuery without dataset id",
			fields: exporterConfFields{
				Kind:      "bigquery",
				ProjectID: "fake-project-id",
			},
			wantErr:  true,
			errValue: "invalid exporter: \"projectID\" and \"datasetID\" are required for kind \"bigquery\"",
		},
		{
			name: "kind Redshift valid with cluster",
			fields: exporterConfFields{
				Kind:              "redshift",
				Database:          "analytics",
				ClusterIdentifier: "my-cluster",
			},
			wantErr: false,
		},
		{
			name: "kind Redshift valid with workgroup",
			fields: exporterConfFields{
				Kind:          "redshift",
				Database:      "analytics",
				WorkgroupName: "my-workgroup",
			},
			wantErr: false,
		},
		{
			name: "kind Redshift valid with schema and table name",
			fields: exporterConfFields{
				Kind:              "redshift",
				Database:          "analytics",
				ClusterIdentifier: "my-cluster",
				TableName:         "goff.feature_flag_evaluations",
			},
			wantErr: false,
		},
		{
			name: "kind Redshift with invalid table name",
			fields: exporterConfFields{
				Kind:              "redshift",
				Database:          "analytics",
				ClusterIdentifier: "my-cluster",
				TableName:         "events; DROP TABLE users",
			},
			wantErr: true,
			errValue: "invalid exporter: \"tableName\" \"events; DROP TABLE users\" is not a valid table name for kind \"r" +
				"edshift\"",
		},
		{
			name: "kind Redshift without database",
			fields: exporterConfFields{
				Kind:              "redshift",
				ClusterIdentifier: "my-cluster",
			},
			wantErr:  true,
			errValue: "invalid exporter: no \"database\" property found for kind \"redshift\"",
		},
		{
			name: "kind Redshift without cluster or workgroup",
			fields: exporterConfFields{
				Kind:     "redshift",
				Database: "analytics",
			},
			wantErr: true,
			errValue: "invalid exporter: exactly one of \"clusterIdentifier\" or \"workgroupName\" is required for kind \"" +
				"redshift\"",
		},
		{
			name: "kind Redshift with both cluster and workgroup",
			fields: exporterConfFields{
				Kind:              "redshift",
				Database:          "analytics",
				ClusterIdentifier: "my-cluster",
				WorkgroupName:     "my-workgroup",
			},
			wantErr: true,
			errValue: "invalid exporter: exactly one of \"clusterIdentifier\" or \"workgroupName\" is required for kind \"" +
				"redshift\"",
		},
		{
			name: "kind Redshift valid with cluster and dbUser",
			fields: exporterConfFields{
				Kind:              "redshift",
				Database:          "analytics",
				ClusterIdentifier: "my-cluster",
				DBUser:            "goff_writer",
			},
			wantErr: false,
		},
		{
			name: "kind Redshift valid with workgroup and secretArn",
			fields: exporterConfFields{
				Kind:          "redshift",
				Database:      "analytics",
				WorkgroupName: "my-workgroup",
				SecretArn:     "arn:aws:secretsmanager:us-east-1:123456789012:secret:goff",
			},
			wantErr: false,
		},
		{
			name: "kind Redshift with dbUser and workgroup",
			fields: exporterConfFields{
				Kind:          "redshift",
				Database:      "analytics",
				WorkgroupName: "my-workgroup",
				DBUser:        "goff_writer",
			},
			wantErr:  true,
			errValue: "invalid exporter: \"dbUser\" cannot be used with \"workgroupName\" for kind \"redshift\"",
		},
		{
			name: "kind Redshift with dbUser and secretArn",
			fields: exporterConfFields{
				Kind:              "redshift",
				Database:          "analytics",
				ClusterIdentifier: "my-cluster",
				DBUser:            "goff_writer",
				SecretArn:         "arn:aws:secretsmanager:us-east-1:123456789012:secret:goff",
			},
			wantErr:  true,
			errValue: "invalid exporter: \"dbUser\" and \"secretArn\" are mutually exclusive for kind \"redshift\"",
		},
	})
}
