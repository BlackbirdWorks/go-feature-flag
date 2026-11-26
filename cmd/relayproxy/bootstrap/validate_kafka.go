package bootstrap

import "regexp"

var kafkaVersionRE = regexp.MustCompile(`^\d+\.\d+\.\d+(\.\d+)?$`)

// kafka checks the producer settings of the kafka exporter.
func (v *validator) kafka(path string, kafka *KafkaSettings) {
	if kafka.Version != "" && !kafkaVersionRE.MatchString(kafka.Version) {
		v.failf(path+".version", ErrInvalidValue, "expected a version such as 3.6.0")
	}

	v.nonNegative(path+".channelBufferSize", int64(kafka.ChannelBufferSize))

	if kafka.Net != nil {
		v.kafkaNet(path+".net", kafka.Net)
	}

	if meta := kafka.Metadata; meta != nil && meta.Retry != nil {
		v.kafkaRetry(path+".metadata.retry", meta.Retry)
	}

	if producer := kafka.Producer; producer != nil {
		v.kafkaProducer(path+".producer", producer)
	}
}

func (v *validator) nonNegative(path string, value int64) {
	if value < 0 {
		v.failf(path, ErrInvalidValue, "expected an integer of 0 or more")
	}
}

func (v *validator) kafkaRetry(path string, retry *KafkaRetry) {
	v.nonNegative(path+".max", int64(retry.Max))
}

func (v *validator) kafkaProducerRetry(path string, retry *KafkaProducerRetry) {
	v.nonNegative(path+".max", int64(retry.Max))
	v.nonNegative(path+".maxBufferLength", int64(retry.MaxBufferLength))
	v.nonNegative(path+".maxBufferBytes", retry.MaxBufferBytes)
}

func (v *validator) kafkaProducer(path string, producer *KafkaProducer) {
	if producer.RequiredAcks != "" {
		v.oneOf(path+".requiredAcks", producer.RequiredAcks, KafkaAcksNone, KafkaAcksLocal, KafkaAcksAll)
	}

	if producer.Compression != "" {
		v.oneOf(path+".compression", producer.Compression, "none", "gzip", "snappy", "lz4", "zstd")
	}

	if producer.Partitioner != "" {
		v.oneOf(path+".partitioner", producer.Partitioner, "hash", "random", "roundrobin")
	}

	v.nonNegative(path+".maxMessageBytes", int64(producer.MaxMessageBytes))

	if producer.Retry != nil {
		v.kafkaProducerRetry(path+".retry", producer.Retry)
	}

	if tx := producer.Transaction; tx != nil && tx.Retry != nil {
		v.kafkaRetry(path+".transaction.retry", tx.Retry)
	}

	if flush := producer.Flush; flush != nil {
		v.nonNegative(path+".flush.bytes", int64(flush.Bytes))
		v.nonNegative(path+".flush.messages", int64(flush.Messages))
		v.nonNegative(path+".flush.maxMessages", int64(flush.MaxMessages))
	}
}

func (v *validator) kafkaNet(path string, network *KafkaNet) {
	v.nonNegative(path+".maxOpenRequests", int64(network.MaxOpenRequests))

	if tls := network.TLS; tls != nil && (tls.CertFile == "") != (tls.KeyFile == "") {
		v.failf(path+".tls", ErrInvalidValue, "certFile and keyFile go together")
	}

	sasl := network.SASL
	if sasl == nil {
		return
	}

	v.secret(path+".sasl.password", sasl.Password)

	if sasl.Mechanism != "" {
		v.oneOf(path+".sasl.mechanism", sasl.Mechanism,
			KafkaSASLPlain, KafkaSASLScram256, KafkaSASLScram512, KafkaSASLGSSAPI)
	}

	v.nonNegative(path+".sasl.version", int64(sasl.Version))

	if gssapi := sasl.GSSAPI; gssapi != nil {
		v.secret(path+".sasl.gssapi.password", gssapi.Password)

		if gssapi.AuthType != "" {
			v.oneOf(path+".sasl.gssapi.authType", gssapi.AuthType, KafkaGSSAPIUser, KafkaGSSAPIKeytab)
		}
	}

	if sasl.Mechanism == KafkaSASLGSSAPI && sasl.GSSAPI == nil {
		v.failf(path+".sasl.gssapi", ErrMissingValue, "the GSSAPI mechanism needs its settings")
	}
}
