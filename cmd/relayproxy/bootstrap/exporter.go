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
// credentials of version 1 are replaced by a reference to a connection.
type Exporter struct {
	Meta                    map[string]string `json:"meta,omitempty"                    yaml:"meta,omitempty"`
	Headers                 map[string]Secret `json:"headers,omitzero"                  yaml:"headers,omitempty"`
	FlushInterval           *Duration         `json:"flushInterval,omitempty"           yaml:"flushInterval,omitempty"`
	MaxEventInMemory        *int              `json:"maxEventInMemory,omitempty"        jsonschema:"minimum=1,default=100000"                                                                                                                                                     yaml:"maxEventInMemory,omitempty"` //nolint:lll // struct tags can not be wrapped
	Retry                   *ExporterRetry    `json:"retry,omitempty"                   yaml:"retry,omitempty"`
	MaxBytes                *ByteSize         `json:"maxBytes,omitempty"                yaml:"maxBytes,omitempty"`
	BlockTimeout            *Duration         `json:"blockTimeout,omitempty"            yaml:"blockTimeout,omitempty"`
	DrainTimeout            *Duration         `json:"drainTimeout,omitempty"            yaml:"drainTimeout,omitempty"`
	Kafka                   *KafkaSettings    `json:"kafka,omitempty"                   yaml:"kafka,omitempty"`
	ClusterIdentifier       string            `json:"clusterIdentifier,omitempty"       yaml:"clusterIdentifier,omitempty"`
	StreamName              string            `json:"streamName,omitempty"              yaml:"streamName,omitempty"`
	EventType               string            `json:"eventType,omitempty"               yaml:"eventType,omitempty"`
	Format                  string            `json:"format,omitempty"                  jsonschema:"enum=json,enum=csv,enum=parquet"                                                                                                                                              yaml:"format,omitempty"` //nolint:lll // struct tags can not be wrapped
	OverflowPolicy          string            `json:"overflowPolicy,omitempty"          yaml:"overflowPolicy,omitempty"`
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

// The overflow policies of an exporter.
const (
	OverflowDropNewest = "dropNewest"
	OverflowDropOldest = "dropOldest"
	OverflowBlock      = "block"
)

// ExporterRetry is the retry of a failed export inside the pipeline: exponential backoff with jitter, bounded in
// attempts and in the time spent waiting. A batch that exhausts it is dropped and counted.
type ExporterRetry struct {
	// MaxAttempts counts the first attempt: 1 means no retry.
	MaxAttempts *int `json:"maxAttempts,omitempty" yaml:"maxAttempts,omitempty"`
	// InitialBackoff is the wait before the first retry; it doubles up to MaxBackoff.
	InitialBackoff *Duration `json:"initialBackoff,omitempty" yaml:"initialBackoff,omitempty"`
	// MaxBackoff caps one wait.
	MaxBackoff *Duration `json:"maxBackoff,omitempty" yaml:"maxBackoff,omitempty"`
	// MaxElapsed caps the time of the retry of one flush: its waits and its attempts.
	MaxElapsed *Duration `json:"maxElapsed,omitempty" yaml:"maxElapsed,omitempty"`
	// OnExhausted says what becomes of a batch that fails every attempt: keep (default) hands it back to be sent again
	// at the next flush, drop drops it and counts it.
	OnExhausted string `json:"onExhausted,omitempty" yaml:"onExhausted,omitempty"`
}

// What an exporter does with a batch that exhausted its retry.
const (
	RetryKeep = "keep"
	RetryDrop = "drop"
)

// Notifier kinds.
const (
	NotifierSlack          = "slack"
	NotifierMicrosoftTeams = "microsoftteams"
	NotifierWebhook        = "webhook"
	NotifierDiscord        = "discord"
)

// Notifier sends flag changes. Which keys apply depends on the kind. The relay builds and runs them from the
// bootstrap file.
type Notifier struct {
	Meta        map[string]string `json:"meta,omitempty"       yaml:"meta,omitempty"`
	Headers     map[string]Secret `json:"headers,omitzero"     yaml:"headers,omitempty"`
	Kind        string            `json:"kind"                 jsonschema:"enum=slack,enum=microsoftteams,enum=webhook,enum=discord" yaml:"kind"` //nolint:lll // struct tags can not be wrapped
	WebhookURL  Secret            `json:"webhookUrl,omitzero"  yaml:"webhookUrl,omitempty"`
	EndpointURL Secret            `json:"endpointUrl,omitzero" yaml:"endpointUrl,omitempty"`
	Secret      Secret            `json:"secret,omitzero"      yaml:"secret,omitempty"`
}
