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
	Meta                    map[string]string      `koanf:"meta"                    mapstructure:"meta"`
	Headers                 map[string][]string    `koanf:"headers"                 mapstructure:"headers"`
	ProjectID               string                 `koanf:"projectid"               mapstructure:"projectID"`
	DatasetID               string                 `koanf:"datasetid"               mapstructure:"datasetID"`
	Filename                string                 `koanf:"filename"                mapstructure:"filename"`
	CsvTemplate             string                 `koanf:"csvtemplate"             mapstructure:"csvTemplate"`
	Bucket                  string                 `koanf:"bucket"                  mapstructure:"bucket"`
	Path                    string                 `koanf:"path"                    mapstructure:"path"`
	EndpointURL             string                 `koanf:"endpointurl"             mapstructure:"endpointUrl"`
	Secret                  string                 `koanf:"secret"                  mapstructure:"secret"`
	OutputDir               string                 `koanf:"outputdir"               mapstructure:"outputDir"`
	LogFormat               string                 `koanf:"logformat"               mapstructure:"logFormat"`
	TableName               string                 `koanf:"tablename"               mapstructure:"tableName"`
	AwsRegion               string                 `koanf:"awsregion"               mapstructure:"awsRegion"`
	ParquetCompressionCodec string                 `koanf:"parquetcompressioncodec" mapstructure:"parquetCompressionCodec"`
	TracerName              string                 `koanf:"tracername"              mapstructure:"tracerName"`
	QueueURL                string                 `koanf:"queueurl"                mapstructure:"queueUrl"`
	SecretArn               string                 `koanf:"secretarn"               mapstructure:"secretArn"`
	Format                  string                 `koanf:"format"                  mapstructure:"format"`
	Kind                    ExporterKind           `koanf:"kind"                    mapstructure:"kind"`
	DBUser                  string                 `koanf:"dbuser"                  mapstructure:"dbUser"`
	GoogleCredentials       string                 `koanf:"googlecredentials"       mapstructure:"googleCredentials"`
	Database                string                 `koanf:"database"                mapstructure:"database"`
	Topic                   string                 `koanf:"topic"                   mapstructure:"topic"`
	StreamArn               string                 `koanf:"streamarn"               mapstructure:"streamArn"`
	StreamName              string                 `koanf:"streamname"              mapstructure:"streamName"`
	AccountName             string                 `koanf:"accountname"             mapstructure:"accountName"`
	AccountKey              string                 `koanf:"accountkey"              mapstructure:"accountKey"`
	Container               string                 `koanf:"container"               mapstructure:"container"`
	ExporterEventType       string                 `koanf:"eventtype"               mapstructure:"eventType"`
	ClusterIdentifier       string                 `koanf:"clusteridentifier"       mapstructure:"clusterIdentifier"`
	WorkgroupName           string                 `koanf:"workgroupname"           mapstructure:"workgroupName"`
	Kafka                   kafkaexporter.Settings `koanf:"kafka"                   mapstructure:"kafka"`
	FlushInterval           int64                  `koanf:"flushinterval"           mapstructure:"flushInterval"`
	MaxEventInMemory        int64                  `koanf:"maxeventinmemory"        mapstructure:"maxEventInMemory"`
	AutoMigrate             bool                   `koanf:"automigrate"             mapstructure:"autoMigrate"`
}

// errInvalidExporter is the root of the errors reported for an exporter configuration that is not valid.
var errInvalidExporter = errors.New("invalid exporter")

// invalidExporterf builds an error that wraps errInvalidExporter: its text is "invalid exporter: " + the formatted
// text.
func invalidExporterf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errInvalidExporter, fmt.Sprintf(format, args...))
}

// IsValid checks that the exporter has the settings its kind requires.
func (c *ExporterConf) IsValid() error {
	err := c.Kind.IsValid()
	if err != nil {
		return err
	}

	err = c.validateStorage()
	if err != nil {
		return err
	}

	if len(c.ParquetCompressionCodec) > 0 {
		_, codecErr := parquet.CompressionCodecFromString(c.ParquetCompressionCodec)
		if codecErr != nil {
			return fmt.Errorf("%w: \"parquetCompressionCodec\" err: %w", errInvalidExporter, codecErr)
		}
	}

	err = c.validateMessaging()
	if err != nil {
		return err
	}

	err = c.validateAzure()
	if err != nil {
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
