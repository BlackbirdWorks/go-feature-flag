package bootstrap

// KafkaMetadata is sarama `Metadata`.
type KafkaMetadata struct {
	Retry *KafkaRetry `json:"retry,omitempty" yaml:"retry,omitempty"`
	// RefreshFrequency and Timeout are the sarama fields of the same names.
	RefreshFrequency *Duration `json:"refreshFrequency,omitempty" yaml:"refreshFrequency,omitempty"`
	Timeout          *Duration `json:"timeout,omitempty"          yaml:"timeout,omitempty"`
	// Full, AllowAutoTopicCreation and SingleFlight are the sarama fields of the same names.
	Full                   *bool `json:"full,omitempty"                   yaml:"full,omitempty"`
	AllowAutoTopicCreation *bool `json:"allowAutoTopicCreation,omitempty" yaml:"allowAutoTopicCreation,omitempty"`
	SingleFlight           *bool `json:"singleFlight,omitempty"           yaml:"singleFlight,omitempty"`
}

// KafkaProducer is sarama `Producer`. `Return.Successes` and `Return.Errors` are always true, as in version 1.
type KafkaProducer struct {
	Retry       *KafkaProducerRetry `json:"retry,omitempty"       yaml:"retry,omitempty"`
	Flush       *KafkaFlush         `json:"flush,omitempty"       yaml:"flush,omitempty"`
	Transaction *KafkaTransaction   `json:"transaction,omitempty" yaml:"transaction,omitempty"`
	// Timeout is sarama `Producer.Timeout`.
	Timeout *Duration `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	// Idempotent is sarama `Producer.Idempotent`.
	Idempotent *bool `json:"idempotent,omitempty" yaml:"idempotent,omitempty"`
	// RequiredAcks is sarama `Producer.RequiredAcks`: none, local or all.
	RequiredAcks string `json:"requiredAcks,omitempty" jsonschema:"enum=none,enum=local,enum=all" yaml:"requiredAcks,omitempty"` //nolint:lll // struct tags can not be wrapped
	// Compression is sarama `Producer.Compression`.
	Compression string `json:"compression,omitempty" jsonschema:"enum=none,enum=gzip,enum=snappy,enum=lz4,enum=zstd" yaml:"compression,omitempty"` //nolint:lll // struct tags can not be wrapped
	// Partitioner picks the sarama `Producer.Partitioner` constructor.
	Partitioner string `json:"partitioner,omitempty" jsonschema:"enum=hash,enum=random,enum=roundrobin" yaml:"partitioner,omitempty"` //nolint:lll // struct tags can not be wrapped
	// MaxMessageBytes and CompressionLevel are the sarama fields of the same names.
	MaxMessageBytes  int `json:"maxMessageBytes,omitempty"  yaml:"maxMessageBytes,omitempty"`
	CompressionLevel int `json:"compressionLevel,omitempty" yaml:"compressionLevel,omitempty"`
}

// KafkaFlush is sarama `Producer.Flush`.
type KafkaFlush struct {
	Frequency   *Duration `json:"frequency,omitempty"   yaml:"frequency,omitempty"`
	Bytes       int       `json:"bytes,omitempty"       yaml:"bytes,omitempty"`
	Messages    int       `json:"messages,omitempty"    yaml:"messages,omitempty"`
	MaxMessages int       `json:"maxMessages,omitempty" yaml:"maxMessages,omitempty"`
}

// KafkaTransaction is sarama `Producer.Transaction`.
type KafkaTransaction struct {
	Retry *KafkaRetry `json:"retry,omitempty" yaml:"retry,omitempty"`
	// Timeout is sarama `Producer.Transaction.Timeout`.
	Timeout *Duration `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	// ID is sarama `Producer.Transaction.ID`.
	ID string `json:"id,omitempty" yaml:"id,omitempty"`
}
