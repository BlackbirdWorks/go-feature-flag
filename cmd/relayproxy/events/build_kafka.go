package events

import (
	"crypto/tls"
	"crypto/x509"
	"slices"
	"time"

	"github.com/IBM/sarama"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events/kafka"
	"github.com/blackbirdworks/semaphore/exporter/kafkaexporter"
)

// kafkaSettings builds the settings of the kafka exporter. When the block sets none of the producer settings the
// sarama configuration stays nil, and the exporter takes the sarama defaults, as in version 1. Otherwise the
// settings are applied on the sarama defaults (a zero value keeps the default) and the producer is made to
// return successes and errors, which the synchronous producer needs.
func (b Builder) kafkaSettings(id *identity, spec *bootstrap.KafkaSettings) (kafkaexporter.Settings, error) {
	settings := kafkaexporter.Settings{
		Config: nil, Topic: spec.Topic, Addresses: slices.Clone(spec.Addresses),
	}

	if !hasKafkaConfig(spec) {
		return settings, nil
	}

	cfg := sarama.NewConfig()

	err := b.applyKafkaTop(id, cfg, spec)
	if err != nil {
		return kafkaexporter.Settings{}, err
	}

	applyKafkaMetadata(cfg, spec.Metadata)
	applyKafkaProducer(cfg, spec.Producer)

	// the exporter uses a synchronous producer, which needs both
	cfg.Producer.Return.Errors = true
	cfg.Producer.Return.Successes = true

	settings.Config = cfg

	return settings, nil
}

func hasKafkaConfig(spec *bootstrap.KafkaSettings) bool {
	return spec.Net != nil || spec.Metadata != nil || spec.Producer != nil || spec.Version != "" ||
		spec.ClientID != "" || spec.RackID != "" || spec.ChannelBufferSize != 0 || spec.APIVersionsRequest != nil
}

func (b Builder) applyKafkaTop(id *identity, cfg *sarama.Config, spec *bootstrap.KafkaSettings) error {
	if spec.Version != "" {
		version, err := sarama.ParseKafkaVersion(spec.Version)
		if err != nil {
			return settingsError("kafka.version", "is not a Kafka version")
		}

		cfg.Version = version
	}

	if spec.ClientID != "" {
		cfg.ClientID = spec.ClientID
	}

	if spec.RackID != "" {
		cfg.RackID = spec.RackID
	}

	if spec.ChannelBufferSize != 0 {
		cfg.ChannelBufferSize = spec.ChannelBufferSize
	}

	if spec.APIVersionsRequest != nil {
		cfg.ApiVersionsRequest = *spec.APIVersionsRequest
	}

	if spec.Net == nil {
		return nil
	}

	return b.applyKafkaNet(id, cfg, spec.Net)
}

func (b Builder) applyKafkaNet(id *identity, cfg *sarama.Config, network *bootstrap.KafkaNet) error {
	setDuration(&cfg.Net.DialTimeout, network.DialTimeout)
	setDuration(&cfg.Net.ReadTimeout, network.ReadTimeout)
	setDuration(&cfg.Net.WriteTimeout, network.WriteTimeout)
	setDuration(&cfg.Net.KeepAlive, network.KeepAlive)
	setBool(&cfg.Net.ResolveCanonicalBootstrapServers, network.ResolveCanonicalBootstrapServers)
	setInt(&cfg.Net.MaxOpenRequests, network.MaxOpenRequests)

	if network.TLS != nil {
		err := b.applyKafkaTLS(id, cfg, network.TLS)
		if err != nil {
			return err
		}
	}

	if network.SASL == nil {
		return nil
	}

	return b.applyKafkaSASL(id, cfg, network.SASL)
}

func (b Builder) applyKafkaTLS(id *identity, cfg *sarama.Config, spec *bootstrap.KafkaTLS) error {
	setBool(&cfg.Net.TLS.Enable, spec.Enable)

	if spec.InsecureSkipVerify == nil && spec.ServerName == "" && spec.CAFile == "" && spec.CertFile == "" {
		return nil
	}

	tlsCfg := &tls.Config{ServerName: spec.ServerName, MinVersion: tls.VersionTLS12}
	if spec.InsecureSkipVerify != nil {
		tlsCfg.InsecureSkipVerify = *spec.InsecureSkipVerify
	}

	if spec.CAFile != "" {
		pem, err := b.readFile("kafka.net.tls.caFile", spec.CAFile)
		if err != nil {
			return err
		}

		id.add("kafka.tls.ca", string(pem))

		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return settingsError("kafka.net.tls.caFile", "holds no PEM certificate")
		}

		tlsCfg.RootCAs = pool
	}

	if spec.CertFile != "" {
		certPEM, err := b.readFile("kafka.net.tls.certFile", spec.CertFile)
		if err != nil {
			return err
		}

		keyPEM, err := b.readFile("kafka.net.tls.keyFile", spec.KeyFile)
		if err != nil {
			return err
		}

		id.add("kafka.tls.cert", string(certPEM))
		id.add("kafka.tls.key", string(keyPEM))

		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return settingsError("kafka.net.tls", "certFile and keyFile are not a key pair")
		}

		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	cfg.Net.TLS.Config = tlsCfg

	return nil
}

// readFile reads a file of the settings through the injected reader. The error never holds the content.
func (b Builder) readFile(field, path string) ([]byte, error) {
	if b.Secrets.ReadFile == nil {
		return nil, settingsError(field, "cannot be read: no file reader")
	}

	content, err := b.Secrets.ReadFile(path)
	if err != nil {
		return nil, settingsError(field, "cannot be read")
	}

	return content, nil
}

func (b Builder) applyKafkaSASL(id *identity, cfg *sarama.Config, spec *bootstrap.KafkaSASL) error {
	sasl := &cfg.Net.SASL

	setBool(&sasl.Enable, spec.Enable)
	setBool(&sasl.Handshake, spec.Handshake)

	if spec.Mechanism != "" {
		sasl.Mechanism = sarama.SASLMechanism(spec.Mechanism)
	}

	if spec.User != "" {
		sasl.User = spec.User
	}

	if spec.AuthIdentity != "" {
		sasl.AuthIdentity = spec.AuthIdentity
	}

	if spec.SCRAMAuthzID != "" {
		sasl.SCRAMAuthzID = spec.SCRAMAuthzID
	}

	if spec.Version != 0 {
		sasl.Version = int16(spec.Version) //nolint:gosec // a SASL version is 0 or 1
	}

	password, err := b.secret(id, "kafka.net.sasl.password", spec.Password)
	if err != nil {
		return err
	}

	if password != "" {
		sasl.Password = password
		// a client that builds a Basic header from the pair would echo it
		id.secrets = append(id.secrets, spec.User+":"+password)
	}

	switch sasl.Mechanism {
	case sarama.SASLTypeSCRAMSHA256:
		sasl.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
			return &kafka.XDGSCRAMClient{HashGeneratorFcn: kafka.SHA256}
		}
	case sarama.SASLTypeSCRAMSHA512:
		sasl.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
			return &kafka.XDGSCRAMClient{HashGeneratorFcn: kafka.SHA512}
		}
	}

	if spec.GSSAPI == nil {
		return nil
	}

	return b.applyKafkaGSSAPI(id, cfg, spec.GSSAPI)
}

func (b Builder) applyKafkaGSSAPI(id *identity, cfg *sarama.Config, spec *bootstrap.KafkaGSSAPI) error {
	gss := &cfg.Net.SASL.GSSAPI

	switch spec.AuthType {
	case bootstrap.KafkaGSSAPIUser:
		gss.AuthType = sarama.KRB5_USER_AUTH
	case bootstrap.KafkaGSSAPIKeytab:
		gss.AuthType = sarama.KRB5_KEYTAB_AUTH
	}

	for dst, src := range map[*string]string{
		&gss.KeyTabPath: spec.KeyTabPath, &gss.KerberosConfigPath: spec.KerberosConfigPath,
		&gss.ServiceName: spec.ServiceName, &gss.Username: spec.Username, &gss.Realm: spec.Realm,
	} {
		if src != "" {
			*dst = src
		}
	}

	setBool(&gss.DisablePAFXFAST, spec.DisablePAFXFAST)

	password, err := b.secret(id, "kafka.net.sasl.gssapi.password", spec.Password)
	if err != nil {
		return err
	}

	if password != "" {
		gss.Password = password
	}

	return nil
}

func applyKafkaMetadata(cfg *sarama.Config, spec *bootstrap.KafkaMetadata) {
	if spec == nil {
		return
	}

	meta := &cfg.Metadata

	if spec.Retry != nil {
		setInt(&meta.Retry.Max, spec.Retry.Max)
		setDuration(&meta.Retry.Backoff, spec.Retry.Backoff)
	}

	setDuration(&meta.RefreshFrequency, spec.RefreshFrequency)
	setDuration(&meta.Timeout, spec.Timeout)
	setBool(&meta.Full, spec.Full)
	setBool(&meta.AllowAutoTopicCreation, spec.AllowAutoTopicCreation)
	setBool(&meta.SingleFlight, spec.SingleFlight)
}

func applyKafkaProducer(cfg *sarama.Config, spec *bootstrap.KafkaProducer) {
	if spec == nil {
		return
	}

	prod := &cfg.Producer

	setInt(&prod.MaxMessageBytes, spec.MaxMessageBytes)
	setInt(&prod.CompressionLevel, spec.CompressionLevel)
	setDuration(&prod.Timeout, spec.Timeout)
	setBool(&prod.Idempotent, spec.Idempotent)

	if acks, ok := kafkaAcks(spec.RequiredAcks); ok {
		prod.RequiredAcks = acks
	}

	if codec, ok := kafkaCompression(spec.Compression); ok {
		prod.Compression = codec
	}

	if partitioner := kafkaPartitioner(spec.Partitioner); partitioner != nil {
		prod.Partitioner = partitioner
	}

	if spec.Retry != nil {
		setInt(&prod.Retry.Max, spec.Retry.Max)
		setDuration(&prod.Retry.Backoff, spec.Retry.Backoff)
		setInt(&prod.Retry.MaxBufferLength, spec.Retry.MaxBufferLength)

		if spec.Retry.MaxBufferBytes != 0 {
			prod.Retry.MaxBufferBytes = spec.Retry.MaxBufferBytes
		}
	}

	if trans := spec.Transaction; trans != nil {
		if trans.ID != "" {
			prod.Transaction.ID = trans.ID
		}

		setDuration(&prod.Transaction.Timeout, trans.Timeout)

		if trans.Retry != nil {
			applyKafkaRetry(&prod.Transaction.Retry.Max, &prod.Transaction.Retry.Backoff, trans.Retry)
		}
	}

	if spec.Flush != nil {
		setInt(&prod.Flush.Bytes, spec.Flush.Bytes)
		setInt(&prod.Flush.Messages, spec.Flush.Messages)
		setInt(&prod.Flush.MaxMessages, spec.Flush.MaxMessages)
		setDuration(&prod.Flush.Frequency, spec.Flush.Frequency)
	}
}

func applyKafkaRetry(maxRetries *int, backoff *time.Duration, spec *bootstrap.KafkaRetry) {
	setInt(maxRetries, spec.Max)
	setDuration(backoff, spec.Backoff)
}

func kafkaAcks(name string) (sarama.RequiredAcks, bool) {
	switch name {
	case bootstrap.KafkaAcksNone:
		return sarama.NoResponse, true
	case bootstrap.KafkaAcksLocal:
		return sarama.WaitForLocal, true
	case bootstrap.KafkaAcksAll:
		return sarama.WaitForAll, true
	}

	return 0, false
}

func kafkaCompression(name string) (sarama.CompressionCodec, bool) {
	switch name {
	case "none":
		return sarama.CompressionNone, true
	case "gzip":
		return sarama.CompressionGZIP, true
	case "snappy":
		return sarama.CompressionSnappy, true
	case "lz4":
		return sarama.CompressionLZ4, true
	case "zstd":
		return sarama.CompressionZSTD, true
	}

	return 0, false
}

func kafkaPartitioner(name string) sarama.PartitionerConstructor {
	switch name {
	case "hash":
		return sarama.NewHashPartitioner
	case "random":
		return sarama.NewRandomPartitioner
	case "roundrobin":
		return sarama.NewRoundRobinPartitioner
	}

	return nil
}

func setInt(dst *int, value int) {
	if value != 0 {
		*dst = value
	}
}

func setBool(dst, value *bool) {
	if value != nil {
		*dst = *value
	}
}

func setDuration(dst *time.Duration, value *bootstrap.Duration) {
	if value != nil && *value > 0 {
		*dst = time.Duration(*value)
	}
}
