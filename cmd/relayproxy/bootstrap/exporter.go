package bootstrap

// Exporter kinds.
const (
	ExporterFile          = "file"
	ExporterWebhook       = "webhook"
	ExporterLog           = "log"
	ExporterS3            = "s3"
	ExporterKinesis       = "kinesis"
	ExporterGoogleStorage = "googleStorage"
	ExporterSQS           = "sqs"
	ExporterKafka         = "kafka"
	ExporterPubSub        = "pubsub"
	ExporterBigQuery      = "bigquery"
	ExporterRedshift      = "redshift"
	ExporterAzure         = "azureBlobStorage"
	ExporterOTel          = "opentelemetry"
)

// Exporter is an evaluation-data sink. Which keys apply depends on the kind. The AWS endpoint, region and
// credentials of version 1 are replaced by a reference to a connection. Only the schema is defined here: the
// runtime wiring comes later.
type Exporter struct {
	Meta                    map[string]string `json:"meta,omitempty"                    yaml:"meta,omitempty"`
	Headers                 map[string]Secret `json:"headers,omitzero"                  yaml:"headers,omitempty"`
	FlushInterval           *Duration         `json:"flushInterval,omitempty"           yaml:"flushInterval,omitempty"`
	MaxEventInMemory        *int              `json:"maxEventInMemory,omitempty"        jsonschema:"minimum=1,default=100000"                                                                                                                                                     yaml:"maxEventInMemory,omitempty"` //nolint:lll // struct tags can not be wrapped
	Kafka                   *KafkaSettings    `json:"kafka,omitempty"                   yaml:"kafka,omitempty"`
	ClusterIdentifier       string            `json:"clusterIdentifier,omitempty"       yaml:"clusterIdentifier,omitempty"`
	StreamName              string            `json:"streamName,omitempty"              yaml:"streamName,omitempty"`
	EventType               string            `json:"eventType,omitempty"               yaml:"eventType,omitempty"`
	Format                  string            `json:"format,omitempty"                  jsonschema:"enum=json,enum=csv,enum=parquet"                                                                                                                                              yaml:"format,omitempty"` //nolint:lll // struct tags can not be wrapped
	Filename                string            `json:"filename,omitempty"                yaml:"filename,omitempty"`
	CsvTemplate             string            `json:"csvTemplate,omitempty"             yaml:"csvTemplate,omitempty"`
	ParquetCompressionCodec string            `json:"parquetCompressionCodec,omitempty" yaml:"parquetCompressionCodec,omitempty"` //nolint:lll // struct tags can not be wrapped
	Path                    string            `json:"path,omitempty"                    yaml:"path,omitempty"`
	OutputDir               string            `json:"outputDir,omitempty"               yaml:"outputDir,omitempty"`
	LogFormat               string            `json:"logFormat,omitempty"               yaml:"logFormat,omitempty"`
	TracerName              string            `json:"tracerName,omitempty"              yaml:"tracerName,omitempty"`
	Container               string            `json:"container,omitempty"               yaml:"container,omitempty"`
	Bucket                  string            `json:"bucket,omitempty"                  yaml:"bucket,omitempty"`
	AccountName             string            `json:"accountName,omitempty"             yaml:"accountName,omitempty"`
	StreamARN               string            `json:"streamArn,omitempty"               yaml:"streamArn,omitempty"`
	Connection              string            `json:"connection,omitempty"              yaml:"connection,omitempty"`
	QueueURL                string            `json:"queueUrl,omitempty"                yaml:"queueUrl,omitempty"`
	ProjectID               string            `json:"projectId,omitempty"               yaml:"projectId,omitempty"`
	DatasetID               string            `json:"datasetId,omitempty"               yaml:"datasetId,omitempty"`
	Topic                   string            `json:"topic,omitempty"                   yaml:"topic,omitempty"`
	TableName               string            `json:"tableName,omitempty"               yaml:"tableName,omitempty"`
	Database                string            `json:"database,omitempty"                yaml:"database,omitempty"`
	Kind                    string            `json:"kind"                              jsonschema:"enum=file,enum=webhook,enum=log,enum=s3,enum=kinesis,enum=googleStorage,enum=sqs,enum=kafka,enum=pubsub,enum=bigquery,enum=redshift,enum=azureBlobStorage,enum=opentelemetry" yaml:"kind"` //nolint:lll // struct tags can not be wrapped
	WorkgroupName           string            `json:"workgroupName,omitempty"           yaml:"workgroupName,omitempty"`
	DBUser                  string            `json:"dbUser,omitempty"                  yaml:"dbUser,omitempty"`
	SecretARN               string            `json:"secretArn,omitempty"               yaml:"secretArn,omitempty"`
	GoogleCredentials       Secret            `json:"googleCredentials,omitzero"        yaml:"googleCredentials,omitempty"`
	AccountKey              Secret            `json:"accountKey,omitzero"               yaml:"accountKey,omitempty"`
	Secret                  Secret            `json:"secret,omitzero"                   yaml:"secret,omitempty"`
	EndpointURL             Secret            `json:"endpointUrl,omitzero"              yaml:"endpointUrl,omitempty"`
	AutoMigrate             bool              `json:"autoMigrate,omitempty"             yaml:"autoMigrate,omitempty"`
}

// KafkaSettings are the settings of the kafka exporter.
type KafkaSettings struct {
	Topic     string   `json:"topic"     yaml:"topic"`
	Addresses []string `json:"addresses" yaml:"addresses"`
}

// Notifier kinds.
const (
	NotifierSlack          = "slack"
	NotifierMicrosoftTeams = "microsoftteams"
	NotifierWebhook        = "webhook"
	NotifierDiscord        = "discord"
)

// Notifier sends flag changes. Which keys apply depends on the kind. Only the schema is defined here.
type Notifier struct {
	Meta        map[string]string `json:"meta,omitempty"       yaml:"meta,omitempty"`
	Headers     map[string]Secret `json:"headers,omitzero"     yaml:"headers,omitempty"`
	Kind        string            `json:"kind"                 jsonschema:"enum=slack,enum=microsoftteams,enum=webhook,enum=discord" yaml:"kind"` //nolint:lll // struct tags can not be wrapped
	WebhookURL  Secret            `json:"webhookUrl,omitzero"  yaml:"webhookUrl,omitempty"`
	EndpointURL Secret            `json:"endpointUrl,omitzero" yaml:"endpointUrl,omitempty"`
	Secret      Secret            `json:"secret,omitzero"      yaml:"secret,omitempty"`
}
