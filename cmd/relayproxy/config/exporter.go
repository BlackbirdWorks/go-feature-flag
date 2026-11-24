package config

import (
	"errors"
	"fmt"

	"github.com/xitongsys/parquet-go/parquet"

	"github.com/blackbirdworks/semaphore/exporter/kafkaexporter"
	"github.com/blackbirdworks/semaphore/exporter/redshiftexporter"
)

// ExporterConf contains all the field to configure an exporter.
type ExporterConf struct {
	Kind                    ExporterKind           `mapstructure:"kind"                    koanf:"kind"`
	TracerName              string                 `mapstructure:"tracerName"              koanf:"tracername"`
	OutputDir               string                 `mapstructure:"outputDir"               koanf:"outputdir"`
	Format                  string                 `mapstructure:"format"                  koanf:"format"`
	Filename                string                 `mapstructure:"filename"                koanf:"filename"`
	CsvTemplate             string                 `mapstructure:"csvTemplate"             koanf:"csvtemplate"`
	Bucket                  string                 `mapstructure:"bucket"                  koanf:"bucket"`
	Path                    string                 `mapstructure:"path"                    koanf:"path"`
	EndpointURL             string                 `mapstructure:"endpointUrl"             koanf:"endpointurl"`
	Secret                  string                 `mapstructure:"secret"                  koanf:"secret"` //nolint:gosec // G117: the field is a configuration value read from the file, it is never logged
	Meta                    map[string]string      `mapstructure:"meta"                    koanf:"meta"`
	LogFormat               string                 `mapstructure:"logFormat"               koanf:"logformat"`
	FlushInterval           int64                  `mapstructure:"flushInterval"           koanf:"flushinterval"`
	MaxEventInMemory        int64                  `mapstructure:"maxEventInMemory"        koanf:"maxeventinmemory"`
	ParquetCompressionCodec string                 `mapstructure:"parquetCompressionCodec" koanf:"parquetcompressioncodec"`
	Headers                 map[string][]string    `mapstructure:"headers"                 koanf:"headers"`
	QueueURL                string                 `mapstructure:"queueUrl"                koanf:"queueurl"`
	Kafka                   kafkaexporter.Settings `mapstructure:"kafka"                   koanf:"kafka"`
	ProjectID               string                 `mapstructure:"projectID"               koanf:"projectid"`
	DatasetID               string                 `mapstructure:"datasetID"               koanf:"datasetid"`
	TableName               string                 `mapstructure:"tableName"               koanf:"tablename"`
	GoogleCredentials       string                 `mapstructure:"googleCredentials"       koanf:"googlecredentials"`
	AutoMigrate             bool                   `mapstructure:"autoMigrate"             koanf:"automigrate"`
	Topic                   string                 `mapstructure:"topic"                   koanf:"topic"`
	StreamArn               string                 `mapstructure:"streamArn"               koanf:"streamarn"`
	StreamName              string                 `mapstructure:"streamName"              koanf:"streamname"`
	AccountName             string                 `mapstructure:"accountName"             koanf:"accountname"`
	AccountKey              string                 `mapstructure:"accountKey"              koanf:"accountkey"`
	Container               string                 `mapstructure:"container"               koanf:"container"`
	ExporterEventType       string                 `mapstructure:"eventType"               koanf:"eventtype"`
	ClusterIdentifier       string                 `mapstructure:"clusterIdentifier"       koanf:"clusteridentifier"`
	WorkgroupName           string                 `mapstructure:"workgroupName"           koanf:"workgroupname"`
	Database                string                 `mapstructure:"database"                koanf:"database"`
	DBUser                  string                 `mapstructure:"dbUser"                  koanf:"dbuser"`
	SecretArn               string                 `mapstructure:"secretArn"               koanf:"secretarn"`
	AwsRegion               string                 `mapstructure:"awsRegion"               koanf:"awsregion"`
}

// errInvalidExporter is the root of the errors reported for an exporter configuration that is not valid.
var errInvalidExporter = errors.New("invalid exporter")

// invalidExporterf builds an error that wraps errInvalidExporter: its text is "invalid exporter: " + the formatted text.
func invalidExporterf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errInvalidExporter, fmt.Sprintf(format, args...))
}

// IsValid checks that the exporter has the settings its kind requires.
func (c *ExporterConf) IsValid() error {
	if err := c.Kind.IsValid(); err != nil {
		return err
	}
	if err := c.validateStorage(); err != nil {
		return err
	}
	if len(c.ParquetCompressionCodec) > 0 {
		if _, err := parquet.CompressionCodecFromString(c.ParquetCompressionCodec); err != nil {
			return fmt.Errorf("%w: \"parquetCompressionCodec\" err: %w", errInvalidExporter, err)
		}
	}
	if err := c.validateMessaging(); err != nil {
		return err
	}
	if err := c.validateAzure(); err != nil {
		return err
	}

	return c.isValidRedshift()
}

// validateStorage checks the settings of the file, bucket, stream and webhook exporters.
func (c *ExporterConf) validateStorage() error {
	switch c.Kind {
	case FileExporter:
		if c.OutputDir == "" {
			return invalidExporterf("no \"outputDir\" property found for kind \"%s\"", c.Kind)
		}
	case S3Exporter, GoogleStorageExporter:
		if c.Bucket == "" {
			return invalidExporterf("no \"bucket\" property found for kind \"%s\"", c.Kind)
		}
	case KinesisExporter:
		if c.StreamArn == "" && c.StreamName == "" {
			return invalidExporterf("no \"streamArn\" or \"streamName\" property found for kind \"%s\"", c.Kind)
		}
	case WebhookExporter:
		if c.EndpointURL == "" {
			return invalidExporterf("no \"endpointUrl\" property found for kind \"%s\"", c.Kind)
		}
	default:
		// the other kinds are checked after the parquet codec
	}

	return nil
}

// validateMessaging checks the settings of the queue, pubsub and bigquery exporters.
func (c *ExporterConf) validateMessaging() error {
	switch c.Kind {
	case SQSExporter:
		if c.QueueURL == "" {
			return invalidExporterf("no \"queueUrl\" property found for kind \"%s\"", c.Kind)
		}
	case KafkaExporter:
		if c.Kafka.Topic == "" || len(c.Kafka.Addresses) == 0 {
			return invalidExporterf("\"kakfa.topic\" and \"kafka.addresses\" are required for kind \"%s\"", c.Kind)
		}
	case PubSubExporter:
		if c.ProjectID == "" || c.Topic == "" {
			return invalidExporterf("\"projectID\" and \"topic\" are required for kind \"%s\"", c.Kind)
		}
	case BigQueryExporter:
		if c.ProjectID == "" || c.DatasetID == "" {
			return invalidExporterf("\"projectID\" and \"datasetID\" are required for kind \"%s\"", c.Kind)
		}
	default:
		// the other kinds have nothing more to check here
	}

	return nil
}

func (c *ExporterConf) validateAzure() error {
	if c.Kind != AzureExporter {
		return nil
	}
	if c.Container == "" {
		return invalidExporterf("no \"container\" property found for kind \"%s\"", c.Kind)
	}
	if c.AccountName == "" {
		return invalidExporterf("no \"accountName\" property found for kind \"%s\"", c.Kind)
	}

	return nil
}

func (c *ExporterConf) isValidRedshift() error {
	if c.Kind != RedshiftExporter {
		return nil
	}
	if c.Database == "" {
		return invalidExporterf("no \"database\" property found for kind \"%s\"", c.Kind)
	}
	if (c.ClusterIdentifier == "") == (c.WorkgroupName == "") {
		return invalidExporterf(
			"exactly one of \"clusterIdentifier\" or \"workgroupName\" is required for kind \"%s\"", c.Kind)
	}
	if c.DBUser != "" && c.WorkgroupName != "" {
		return invalidExporterf("\"dbUser\" cannot be used with \"workgroupName\" for kind \"%s\"", c.Kind)
	}
	if c.DBUser != "" && c.SecretArn != "" {
		return invalidExporterf("\"dbUser\" and \"secretArn\" are mutually exclusive for kind \"%s\"", c.Kind)
	}
	if !redshiftexporter.IsValidTableName(c.TableName) {
		return invalidExporterf(
			"\"tableName\" %q is not a valid table name for kind \"%s\"", c.TableName, c.Kind)
	}

	return nil
}

type ExporterKind string

const (
	FileExporter          ExporterKind = "file"
	WebhookExporter       ExporterKind = "webhook"
	LogExporter           ExporterKind = "log"
	S3Exporter            ExporterKind = "s3"
	KinesisExporter       ExporterKind = "kinesis"
	GoogleStorageExporter ExporterKind = "googleStorage"
	SQSExporter           ExporterKind = "sqs"
	KafkaExporter         ExporterKind = "kafka"
	PubSubExporter        ExporterKind = "pubsub"
	BigQueryExporter      ExporterKind = "bigquery"
	RedshiftExporter      ExporterKind = "redshift"
	AzureExporter         ExporterKind = "azureBlobStorage"
	OpenTelemetryExporter ExporterKind = "opentelemetry"
)

// IsValid is checking if the value is part of the enum.
func (r ExporterKind) IsValid() error {
	switch r {
	case FileExporter,
		WebhookExporter,
		LogExporter,
		S3Exporter,
		GoogleStorageExporter,
		SQSExporter,
		KafkaExporter,
		PubSubExporter,
		BigQueryExporter,
		RedshiftExporter,
		KinesisExporter,
		AzureExporter,
		OpenTelemetryExporter:
		return nil
	}

	return invalidExporterf("kind \"%s\" is not supported", r)
}
