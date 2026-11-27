package bootstrap

// KafkaRetry is a sarama `Retry` block of the metadata and the transactions: Max and Backoff.
type KafkaRetry struct {
	Backoff *Duration `json:"backoff,omitempty" yaml:"backoff,omitempty"`
	Max     int       `json:"max,omitempty"     yaml:"max,omitempty"`
}

// KafkaProducerRetry is sarama `Producer.Retry`: Max and Backoff, and the limits of the retry buffer, which only
// the producer has.
type KafkaProducerRetry struct {
	Backoff         *Duration `json:"backoff,omitempty"         yaml:"backoff,omitempty"`
	MaxBufferBytes  int64     `json:"maxBufferBytes,omitempty"  yaml:"maxBufferBytes,omitempty"`
	Max             int       `json:"max,omitempty"             yaml:"max,omitempty"`
	MaxBufferLength int       `json:"maxBufferLength,omitempty" yaml:"maxBufferLength,omitempty"`
}
