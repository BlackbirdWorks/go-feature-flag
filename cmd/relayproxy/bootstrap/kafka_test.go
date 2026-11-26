package bootstrap_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

const kafkaHead = "version: 2\nexporters:\n  e:\n    kind: kafka\n    kafka:\n      topic: t\n" +
	"      addresses: [\"h:9092\"]\n"

// TestKafkaSettings checks that the producer settings of the kafka exporter are accepted, with the values they
// give, and that a bad one is rejected with the path of the key.
func TestKafkaSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr  error
		name     string
		extra    string
		wantPath string
	}{
		{name: "every setting", extra: kafkaEverything},
		{name: "no setting"},
		{
			name:     "a bad version",
			extra:    "      version: three\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.kafka.version",
		},
		{
			name:     "a negative buffer",
			extra:    "      channelBufferSize: -1\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.kafka.channelBufferSize",
		},
		{
			name:     "a negative open request count",
			extra:    "      net: {maxOpenRequests: -1}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.kafka.net.maxOpenRequests",
		},
		{
			name:     "a duration that is not one",
			extra:    "      net: {dialTimeout: 5}\n",
			wantErr:  bootstrap.ErrInvalidDuration,
			wantPath: "exporters.e.kafka.net.dialTimeout",
		},
		{
			name:     "a cert without a key",
			extra:    "      net: {tls: {certFile: /c}}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.kafka.net.tls",
		},
		{
			name:     "a key without a cert",
			extra:    "      net: {tls: {keyFile: /k}}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.kafka.net.tls",
		},
		{
			name:     "an unknown mechanism",
			extra:    "      net: {sasl: {mechanism: NTLM}}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.kafka.net.sasl.mechanism",
		},
		{
			name:     "a negative sasl version",
			extra:    "      net: {sasl: {version: -1}}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.kafka.net.sasl.version",
		},
		{
			name:     "an inline password",
			extra:    "      net: {sasl: {password: hunter2}}\n",
			wantErr:  bootstrap.ErrInlineSecret,
			wantPath: "exporters.e.kafka.net.sasl.password",
		},
		{
			name:     "an inline gssapi password",
			extra:    "      net: {sasl: {mechanism: GSSAPI, gssapi: {password: hunter2}}}\n",
			wantErr:  bootstrap.ErrInlineSecret,
			wantPath: "exporters.e.kafka.net.sasl.gssapi.password",
		},
		{
			name:     "gssapi without its settings",
			extra:    "      net: {sasl: {mechanism: GSSAPI}}\n",
			wantErr:  bootstrap.ErrMissingValue,
			wantPath: "exporters.e.kafka.net.sasl.gssapi",
		},
		{
			name:     "an unknown gssapi auth type",
			extra:    "      net: {sasl: {mechanism: GSSAPI, gssapi: {authType: pin}}}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.kafka.net.sasl.gssapi.authType",
		},
		{
			name:     "a negative metadata retry",
			extra:    "      metadata: {retry: {max: -1}}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.kafka.metadata.retry.max",
		},
		{
			name:     "unknown acks",
			extra:    "      producer: {requiredAcks: some}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.kafka.producer.requiredAcks",
		},
		{
			name:     "unknown compression",
			extra:    "      producer: {compression: rar}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.kafka.producer.compression",
		},
		{
			name:     "unknown partitioner",
			extra:    "      producer: {partitioner: custom}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.kafka.producer.partitioner",
		},
		{
			name:     "a negative message size",
			extra:    "      producer: {maxMessageBytes: -1}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.kafka.producer.maxMessageBytes",
		},
		{
			name:     "a negative producer retry",
			extra:    "      producer: {retry: {maxBufferBytes: -1}}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.kafka.producer.retry.maxBufferBytes",
		},
		{
			name:     "a negative transaction retry",
			extra:    "      producer: {transaction: {retry: {max: -1}}}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.kafka.producer.transaction.retry.max",
		},
		{
			name:     "a negative flush",
			extra:    "      producer: {flush: {bytes: -1}}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.kafka.producer.flush.bytes",
		},
		{
			name:     "a negative flush message count",
			extra:    "      producer: {flush: {messages: -1}}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.kafka.producer.flush.messages",
		},
		{
			name:     "a negative flush maximum",
			extra:    "      producer: {flush: {maxMessages: -1}}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e.kafka.producer.flush.maxMessages",
		},
		{
			name:  "the buffer limits under producer.retry",
			extra: "      producer: {retry: {maxBufferLength: 10, maxBufferBytes: 20}}\n",
		},
		{
			name:     "maxBufferLength under metadata.retry",
			extra:    "      metadata: {retry: {maxBufferLength: 1}}\n",
			wantErr:  bootstrap.ErrUnknownKey,
			wantPath: "exporters.e.kafka.metadata.retry.maxBufferLength",
		},
		{
			name:     "maxBufferBytes under metadata.retry",
			extra:    "      metadata: {retry: {maxBufferBytes: 1}}\n",
			wantErr:  bootstrap.ErrUnknownKey,
			wantPath: "exporters.e.kafka.metadata.retry.maxBufferBytes",
		},
		{
			name:     "maxBufferLength under producer.transaction.retry",
			extra:    "      producer: {transaction: {retry: {maxBufferLength: 1}}}\n",
			wantErr:  bootstrap.ErrUnknownKey,
			wantPath: "exporters.e.kafka.producer.transaction.retry.maxBufferLength",
		},
		{
			name:     "maxBufferBytes under producer.transaction.retry",
			extra:    "      producer: {transaction: {retry: {maxBufferBytes: 1}}}\n",
			wantErr:  bootstrap.ErrUnknownKey,
			wantPath: "exporters.e.kafka.producer.transaction.retry.maxBufferBytes",
		},
		{
			name:     "an unknown key",
			extra:    "      net: {sasl: {usr: u}}\n",
			wantErr:  bootstrap.ErrUnknownKey,
			wantPath: "exporters.e.kafka.net.sasl.usr",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := bootstrap.Parse([]byte(kafkaHead+tt.extra), nil, options(envMap{"PASS": "p"}, nil, false))

			if tt.wantErr == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tt.wantErr)

			var found bool

			for _, pe := range pathErrors(err) {
				found = found || pe.Path == tt.wantPath
			}

			assert.True(t, found, "an error at %s, got %v", tt.wantPath, err)
		})
	}
}

const kafkaEverything = `      version: 3.6.0
      clientId: relay
      rackId: r1
      channelBufferSize: 256
      apiVersionsRequest: true
      net:
        maxOpenRequests: 5
        dialTimeout: 10s
        readTimeout: 20s
        writeTimeout: 30s
        keepAlive: 1m
        resolveCanonicalBootstrapServers: true
        tls: {enable: true, insecureSkipVerify: false, serverName: b.example, caFile: /ca, certFile: /c, keyFile: /k}
        sasl:
          enable: true
          handshake: true
          mechanism: SCRAM-SHA-512
          user: u
          password: {fromEnv: PASS}
          authIdentity: i
          scramAuthzId: z
          version: 1
          gssapi:
            {authType: keytab, keyTabPath: /kt, kerberosConfigPath: /kc, serviceName: s, username: u, realm: R,
             password: {fromEnv: PASS}, disablePafxfast: true}
      metadata:
        retry: {max: 3, backoff: 2s}
        refreshFrequency: 5m
        timeout: 30s
        full: true
        allowAutoTopicCreation: false
        singleFlight: true
      producer:
        maxMessageBytes: 1000000
        requiredAcks: all
        timeout: 10s
        compression: snappy
        compressionLevel: 2
        partitioner: hash
        idempotent: true
        retry: {max: 5, backoff: 100ms, maxBufferLength: 10, maxBufferBytes: 20}
        flush: {bytes: 1, messages: 2, frequency: 3s, maxMessages: 4}
        transaction: {id: tx, timeout: 1m, retry: {max: 1, backoff: 1s}}
`

// TestKafkaSettingsDecode checks the values that decode from a document that sets every key.
func TestKafkaSettingsDecode(t *testing.T) {
	t.Parallel()

	cfg, err := bootstrap.Parse([]byte(kafkaHead+kafkaEverything), nil, options(envMap{"PASS": "p"}, nil, false))
	require.NoError(t, err)

	kafka := cfg.Exporters["e"].Kafka
	require.NotNil(t, kafka)
	require.NotNil(t, kafka.Net)
	require.NotNil(t, kafka.Net.SASL)
	require.NotNil(t, kafka.Net.TLS)
	require.NotNil(t, kafka.Producer)
	require.NotNil(t, kafka.Metadata)

	assert.Equal(t, "3.6.0", kafka.Version)
	assert.Equal(t, "relay", kafka.ClientID)
	assert.Equal(t, 5, kafka.Net.MaxOpenRequests)
	assert.Equal(t, bootstrap.Duration(time.Minute), *kafka.Net.KeepAlive)
	assert.Equal(t, "ca", kafka.Net.TLS.CAFile[1:])
	assert.Equal(t, bootstrap.KafkaSASLScram512, kafka.Net.SASL.Mechanism)
	assert.True(t, kafka.Net.SASL.Password.IsSet())
	assert.Equal(t, "R", kafka.Net.SASL.GSSAPI.Realm)
	assert.Equal(t, bootstrap.Duration(5*time.Minute), *kafka.Metadata.RefreshFrequency)
	assert.False(t, *kafka.Metadata.AllowAutoTopicCreation)
	assert.Equal(t, bootstrap.KafkaAcksAll, kafka.Producer.RequiredAcks)
	assert.Equal(t, int64(20), kafka.Producer.Retry.MaxBufferBytes)
	assert.Equal(t, "tx", kafka.Producer.Transaction.ID)
	assert.Equal(t, 4, kafka.Producer.Flush.MaxMessages)
}

// TestV1ExporterKeysThatV2Carries checks the exporter keys that version 1 accepted and that the first v2 schema
// did not: format for kinesis, sqs and kafka, and autoMigrate for bigquery.
func TestV1ExporterKeysThatV2Carries(t *testing.T) {
	t.Parallel()

	const conns = "version: 2\nconnections:\n  k: {type: kinesis, stream: s}\n" +
		"  q: {type: sqs, queueUrl: \"https://q.example/q\"}\nexporters:\n"

	tests := []struct {
		wantErr error
		name    string
		entry   string
	}{
		{name: "kinesis with a format", entry: "  e: {kind: kinesis, connection: k, format: json}\n"},
		{name: "sqs with a format", entry: "  e: {kind: sqs, connection: q, format: json}\n"},
		{
			name:  "kafka with a format",
			entry: "  e: {kind: kafka, format: json, kafka: {topic: t, addresses: [\"h:1\"]}}\n",
		},
		{
			name:    "kafka with a bad format",
			entry:   "  e: {kind: kafka, format: xml, kafka: {topic: t, addresses: [\"h:1\"]}}\n",
			wantErr: bootstrap.ErrInvalidValue,
		},
		{
			name:  "bigquery with autoMigrate",
			entry: "  e: {kind: bigquery, projectId: p, datasetId: d, autoMigrate: true}\n",
		},
		{
			name:    "log with a format is still not allowed",
			entry:   "  e: {kind: log, format: json}\n",
			wantErr: bootstrap.ErrNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := bootstrap.Parse([]byte(conns+tt.entry), nil, options(nil, nil, false))

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.NotNil(t, cfg.Exporters["e"])
		})
	}
}
