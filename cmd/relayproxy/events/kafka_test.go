package events_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/exporter/kafkaexporter"
)

const kafkaBroker = "h:1"

const kafkaBase = `exporters:
  k:
    kind: kafka
    kafka:
      topic: t
      addresses: ["h:9092"]
`

// pemPair returns a self-signed certificate and its key, as PEM.
func pemPair(tb testing.TB) ([]byte, []byte) {
	tb.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(tb, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "events-test"},
		NotBefore: time.Date(
			2020,
			1,
			1,
			0,
			0,
			0,
			0,
			time.UTC,
		),
		NotAfter:              time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(tb, err)

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(tb, err)

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// buildKafka builds the kafka exporter of a document with the given extra keys under `kafka:`.
func buildKafka(
	tb testing.TB,
	extra string,
	files map[string][]byte,
) (*kafkaexporter.Exporter, events.ExporterDef, error) {
	tb.Helper()

	cfg, opts := parseDoc(
		tb,
		"version: 2\n"+kafkaBase+extra,
		map[string]string{"PASS": "kafka-password-123", "GPASS": "gssapi-password-9"},
	)
	opts.ReadFile = func(path string) ([]byte, error) {
		if content, ok := files[path]; ok {
			return content, nil
		}

		return nil, os.ErrNotExist
	}

	built, err := events.Builder{Secrets: opts}.Exporters(context.Background(), events.SpecsFrom(cfg))
	if err != nil {
		return nil, events.ExporterDef{}, err
	}

	return inner[*kafkaexporter.Exporter](tb, &built[0]), built[0], nil
}

// TestKafkaSettingsMapToTheSaramaConfig lists the v1 fields (the fields of sarama.Config that version 1 could set
// under kafka.config) and their v2 keys, and checks that each key reaches its field.
func TestKafkaSettingsMapToTheSaramaConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		check func(*testing.T, *sarama.Config)
		name  string
		extra string
	}{
		{
			name: "Version, ClientID, RackID, ChannelBufferSize, ApiVersionsRequest",
			extra: "      version: 3.6.0\n      clientId: relay\n      rackId: rack-1\n      channelBufferSize: 512\n" +
				"      apiVersionsRequest: false\n",
			check: func(t *testing.T, cfg *sarama.Config) {
				t.Helper()
				assert.Equal(t, sarama.V3_6_0_0, cfg.Version)
				assert.Equal(t, "relay", cfg.ClientID)
				assert.Equal(t, "rack-1", cfg.RackID)
				assert.Equal(t, 512, cfg.ChannelBufferSize)
				assert.False(t, cfg.ApiVersionsRequest)
			},
		},
		{
			name: "Net.MaxOpenRequests DialTimeout ReadTimeout WriteTimeout KeepAlive ResolveCanonicalBootstrapServers",
			extra: "      net: {maxOpenRequests: 3, dialTimeout: 4s, readTimeout: 5s, writeTimeout: 6s, keepAlive: 7s," +
				" resolveCanonicalBootstrapServers: true}\n",
			check: func(t *testing.T, cfg *sarama.Config) {
				t.Helper()
				assert.Equal(t, 3, cfg.Net.MaxOpenRequests)
				assert.Equal(t, 4*time.Second, cfg.Net.DialTimeout)
				assert.Equal(t, 5*time.Second, cfg.Net.ReadTimeout)
				assert.Equal(t, 6*time.Second, cfg.Net.WriteTimeout)
				assert.Equal(t, 7*time.Second, cfg.Net.KeepAlive)
				assert.True(t, cfg.Net.ResolveCanonicalBootstrapServers)
			},
		},
		{
			name: "Net.SASL PLAIN with Enable Handshake User Password AuthIdentity Version",
			extra: "      net: {sasl: {enable: true, handshake: false, mechanism: PLAIN, user: svc, password: {fromEnv: PASS}," +
				" authIdentity: ident, version: 1}}\n",
			check: func(t *testing.T, cfg *sarama.Config) {
				t.Helper()

				sasl := cfg.Net.SASL
				assert.True(t, sasl.Enable)
				assert.False(t, sasl.Handshake)
				assert.Equal(t, sarama.SASLTypePlaintext, string(sasl.Mechanism))
				assert.Equal(t, "svc", sasl.User)
				assert.Equal(t, "kafka-password-123", sasl.Password)
				assert.Equal(t, "ident", sasl.AuthIdentity)
				assert.Equal(t, int16(1), sasl.Version)
				assert.Nil(t, sasl.SCRAMClientGeneratorFunc)
			},
		},
		{
			name: "Net.SASL SCRAM-SHA-256 and SCRAMAuthzID",
			extra: "      net: {sasl: {enable: true, mechanism: SCRAM-SHA-256, user: u, password: {fromEnv: PASS},\n" +
				"        scramAuthzId: z}}\n",
			check: func(t *testing.T, cfg *sarama.Config) {
				t.Helper()
				assert.Equal(t, sarama.SASLTypeSCRAMSHA256, string(cfg.Net.SASL.Mechanism))
				assert.Equal(t, "z", cfg.Net.SASL.SCRAMAuthzID)
				require.NotNil(t, cfg.Net.SASL.SCRAMClientGeneratorFunc, "the generator version 1 set for SCRAM")
			},
		},
		{
			name:  "Net.SASL SCRAM-SHA-512",
			extra: "      net: {sasl: {enable: true, mechanism: SCRAM-SHA-512, user: u, password: {fromEnv: PASS}}}\n",
			check: func(t *testing.T, cfg *sarama.Config) {
				t.Helper()
				assert.Equal(t, sarama.SASLTypeSCRAMSHA512, string(cfg.Net.SASL.Mechanism))
				require.NotNil(t, cfg.Net.SASL.SCRAMClientGeneratorFunc)
			},
		},
		{
			name: "Net.SASL.GSSAPI",
			extra: "      net: {sasl: {enable: true, mechanism: GSSAPI, gssapi: {authType: keytab, keyTabPath: /k, " +
				"kerberosConfigPath: /c, serviceName: kafka, username: u, realm: R, password: {fromEnv: GPASS},\n" +
				"        disablePafxfast: true}}}\n",
			check: func(t *testing.T, cfg *sarama.Config) {
				t.Helper()

				gss := cfg.Net.SASL.GSSAPI
				assert.Equal(t, sarama.KRB5_KEYTAB_AUTH, gss.AuthType)
				assert.Equal(t, "/k", gss.KeyTabPath)
				assert.Equal(t, "/c", gss.KerberosConfigPath)
				assert.Equal(t, "kafka", gss.ServiceName)
				assert.Equal(t, "u", gss.Username)
				assert.Equal(t, "R", gss.Realm)
				assert.Equal(t, "gssapi-password-9", gss.Password)
				assert.True(t, gss.DisablePAFXFAST)
			},
		},
		{
			name:  "Net.SASL.GSSAPI with the user auth type",
			extra: "      net: {sasl: {enable: true, mechanism: GSSAPI, gssapi: {authType: user}}}\n",
			check: func(t *testing.T, cfg *sarama.Config) {
				t.Helper()
				assert.Equal(t, sarama.KRB5_USER_AUTH, cfg.Net.SASL.GSSAPI.AuthType)
			},
		},
		{
			name: "Metadata",
			extra: "      metadata: {retry: {max: 2, backoff: 3s}, refreshFrequency: 4m, timeout: 5s, full: false," +
				" allowAutoTopicCreation: false, singleFlight: true}\n",
			check: func(t *testing.T, cfg *sarama.Config) {
				t.Helper()

				meta := cfg.Metadata
				assert.Equal(t, 2, meta.Retry.Max)
				assert.Equal(t, 3*time.Second, meta.Retry.Backoff)
				assert.Equal(t, 4*time.Minute, meta.RefreshFrequency)
				assert.Equal(t, 5*time.Second, meta.Timeout)
				assert.False(t, meta.Full)
				assert.False(t, meta.AllowAutoTopicCreation)
				assert.True(t, meta.SingleFlight)
			},
		},
		{
			name: "Producer",
			extra: "      producer: {maxMessageBytes: 2048, requiredAcks: all, timeout: 9s, compression: zstd,\n" +
				"        compressionLevel: 3, partitioner: roundrobin, idempotent: true,\n" +
				"        retry: {max: 7, backoff: 2s, maxBufferLength: 11, maxBufferBytes: 12}}\n",
			check: func(t *testing.T, cfg *sarama.Config) {
				t.Helper()

				prod := cfg.Producer
				assert.Equal(t, 2048, prod.MaxMessageBytes)
				assert.Equal(t, sarama.WaitForAll, prod.RequiredAcks)
				assert.Equal(t, 9*time.Second, prod.Timeout)
				assert.Equal(t, sarama.CompressionZSTD, prod.Compression)
				assert.Equal(t, 3, prod.CompressionLevel)
				assert.NotNil(t, prod.Partitioner)
				assert.True(t, prod.Idempotent)
				assert.Equal(t, 7, prod.Retry.Max)
				assert.Equal(t, 2*time.Second, prod.Retry.Backoff)
				assert.Equal(t, 11, prod.Retry.MaxBufferLength)
				assert.Equal(t, int64(12), prod.Retry.MaxBufferBytes)
				assert.True(t, prod.Return.Successes, "forced, as in version 1")
				assert.True(t, prod.Return.Errors, "forced, as in version 1")
			},
		},
		{
			name:  "Producer.Flush",
			extra: "      producer: {flush: {bytes: 1, messages: 2, frequency: 3s, maxMessages: 4}}\n",
			check: func(t *testing.T, cfg *sarama.Config) {
				t.Helper()
				assert.Equal(t, 1, cfg.Producer.Flush.Bytes)
				assert.Equal(t, 2, cfg.Producer.Flush.Messages)
				assert.Equal(t, 3*time.Second, cfg.Producer.Flush.Frequency)
				assert.Equal(t, 4, cfg.Producer.Flush.MaxMessages)
			},
		},
		{
			name:  "Producer.Transaction",
			extra: "      producer: {transaction: {id: tx, timeout: 8s, retry: {max: 5, backoff: 6s}}}\n",
			check: func(t *testing.T, cfg *sarama.Config) {
				t.Helper()
				assert.Equal(t, "tx", cfg.Producer.Transaction.ID)
				assert.Equal(t, 8*time.Second, cfg.Producer.Transaction.Timeout)
				assert.Equal(t, 5, cfg.Producer.Transaction.Retry.Max)
				assert.Equal(t, 6*time.Second, cfg.Producer.Transaction.Retry.Backoff)
			},
		},
		{
			name:  "the other required acks, compressions and partitioners",
			extra: "      producer: {requiredAcks: none, compression: gzip, partitioner: random}\n",
			check: func(t *testing.T, cfg *sarama.Config) {
				t.Helper()
				assert.Equal(t, sarama.NoResponse, cfg.Producer.RequiredAcks)
				assert.Equal(t, sarama.CompressionGZIP, cfg.Producer.Compression)
				assert.NotNil(t, cfg.Producer.Partitioner)
			},
		},
		{
			name:  "local acks, snappy and the hash partitioner",
			extra: "      producer: {requiredAcks: local, compression: snappy, partitioner: hash}\n",
			check: func(t *testing.T, cfg *sarama.Config) {
				t.Helper()
				assert.Equal(t, sarama.WaitForLocal, cfg.Producer.RequiredAcks)
				assert.Equal(t, sarama.CompressionSnappy, cfg.Producer.Compression)
			},
		},
		{
			name:  "lz4 and no compression",
			extra: "      producer: {compression: lz4}\n",
			check: func(t *testing.T, cfg *sarama.Config) {
				t.Helper()
				assert.Equal(t, sarama.CompressionLZ4, cfg.Producer.Compression)
			},
		},
		{
			name:  "none keeps no compression",
			extra: "      producer: {compression: none}\n",
			check: func(t *testing.T, cfg *sarama.Config) {
				t.Helper()
				assert.Equal(t, sarama.CompressionNone, cfg.Producer.Compression)
			},
		},
		{
			name:  "unset values keep the sarama defaults",
			extra: "      clientId: only-this\n",
			check: func(t *testing.T, cfg *sarama.Config) {
				t.Helper()

				def := sarama.NewConfig()

				assert.Equal(t, "only-this", cfg.ClientID)
				assert.Equal(t, def.Producer.MaxMessageBytes, cfg.Producer.MaxMessageBytes)
				assert.Equal(t, def.Net.DialTimeout, cfg.Net.DialTimeout)
				assert.Equal(t, def.Metadata.AllowAutoTopicCreation, cfg.Metadata.AllowAutoTopicCreation)
				assert.Equal(t, def.Version, cfg.Version)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exp, _, err := buildKafka(t, tt.extra, nil)
			require.NoError(t, err)
			require.NotNil(t, exp.Settings.Config)

			assert.Equal(t, "t", exp.Settings.Topic)
			assert.Equal(t, []string{"h:9092"}, exp.Settings.Addresses)
			tt.check(t, exp.Settings.Config)
		})
	}
}

func TestKafkaWithoutProducerSettingsKeepsTheSaramaDefaults(t *testing.T) {
	t.Parallel()

	exp, _, err := buildKafka(t, "", nil)
	require.NoError(t, err)
	assert.Nil(t, exp.Settings.Config, "no sarama configuration: the exporter takes the defaults, as in version 1")
}

const kafkaTLSFull = "      net: {tls: {enable: true, insecureSkipVerify: true, serverName: broker.example,\n" +
	"        caFile: /ca.pem, certFile: /cert.pem, keyFile: /key.pem}}\n"

// pemFiles returns the files of a certificate authority and a client certificate, and a file that is no PEM.
func pemFiles(tb testing.TB) map[string][]byte {
	tb.Helper()

	certPEM, keyPEM := pemPair(tb)

	return map[string][]byte{
		"/ca.pem":   certPEM,
		"/cert.pem": certPEM,
		"/key.pem":  keyPEM,
		"/bad.pem":  []byte("not pem"),
	}
}

func TestKafkaTLSSettings(t *testing.T) {
	t.Parallel()

	exp, _, err := buildKafka(t, kafkaTLSFull, pemFiles(t))
	require.NoError(t, err)

	tlsCfg := exp.Settings.Net.TLS
	assert.True(t, tlsCfg.Enable)
	require.NotNil(t, tlsCfg.Config)
	assert.True(t, tlsCfg.Config.InsecureSkipVerify)
	assert.Equal(t, "broker.example", tlsCfg.Config.ServerName)
	assert.NotNil(t, tlsCfg.Config.RootCAs)
	assert.Len(t, tlsCfg.Config.Certificates, 1)
}

// TestKafkaTLSFilesAreInTheIdentity checks that a rotated certificate rebuilds the exporter at the next reload.
func TestKafkaTLSFilesAreInTheIdentity(t *testing.T) {
	t.Parallel()

	files := pemFiles(t)

	_, before, err := buildKafka(t, kafkaTLSFull, files)
	require.NoError(t, err)

	files["/cert.pem"], files["/key.pem"] = pemPair(t)

	_, after, err := buildKafka(t, kafkaTLSFull, files)
	require.NoError(t, err)

	assert.NotEqual(t, before.Identity, after.Identity)
}

func TestKafkaTLSErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		readFile func(files map[string][]byte) func(string) ([]byte, error)
		name     string
		extra    string
	}{
		{name: "a missing ca file", extra: "      net: {tls: {enable: true, caFile: /missing.pem}}\n"},
		{name: "a ca file without a certificate", extra: "      net: {tls: {enable: true, caFile: /bad.pem}}\n"},
		{
			name:  "a missing cert file",
			extra: "      net: {tls: {enable: true, certFile: /missing.pem, keyFile: /key.pem}}\n",
		},
		{
			name:  "a missing key file",
			extra: "      net: {tls: {enable: true, certFile: /cert.pem, keyFile: /missing.pem}}\n",
		},
		{
			name:  "a key that is not the key of the certificate",
			extra: "      net: {tls: {enable: true, certFile: /cert.pem, keyFile: /bad.pem}}\n",
		},
		{
			name: "no file reader", extra: "      net: {tls: {enable: true, caFile: /ca.pem}}\n",
			readFile: func(map[string][]byte) func(string) ([]byte, error) { return nil },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, opts := parseDoc(t, "version: 2\n"+kafkaBase+tt.extra, nil)

			files := pemFiles(t)
			opts.ReadFile = filesReader(files)

			if tt.readFile != nil {
				opts.ReadFile = tt.readFile(files)
			}

			_, err := events.Builder{Secrets: opts}.Exporters(context.Background(), events.SpecsFrom(cfg))
			require.ErrorIs(t, err, events.ErrInvalidSettings)

			var entry *events.EntryError

			require.ErrorAs(t, err, &entry)
			assert.Equal(t, "k", entry.Name)
		})
	}
}

func TestKafkaSettingsErrors(t *testing.T) {
	t.Parallel()

	specs := func(settings *bootstrap.KafkaSettings) events.Specs {
		return events.Specs{
			Exporters: map[string]bootstrap.Exporter{"k": {Kind: bootstrap.ExporterKafka, Kafka: settings}},
		}
	}

	tests := []struct {
		settings *bootstrap.KafkaSettings
		wantErr  error
		name     string
	}{
		{
			name:     "a version that is not one",
			settings: &bootstrap.KafkaSettings{Topic: "t", Addresses: []string{kafkaBroker}, Version: "three"},
			wantErr:  events.ErrInvalidSettings,
		},
		{
			name: "a password that does not resolve",
			settings: &bootstrap.KafkaSettings{Topic: "t", Addresses: []string{kafkaBroker}, Net: &bootstrap.KafkaNet{
				SASL: &bootstrap.KafkaSASL{Password: mustSecret(t, "{fromEnv: NOT_SET}")},
			}},
			wantErr: events.ErrUnresolvedSecret,
		},
		{
			name: "a gssapi password that does not resolve",
			settings: &bootstrap.KafkaSettings{Topic: "t", Addresses: []string{kafkaBroker}, Net: &bootstrap.KafkaNet{
				SASL: &bootstrap.KafkaSASL{
					GSSAPI: &bootstrap.KafkaGSSAPI{Password: mustSecret(t, "{fromEnv: NOT_SET}")},
				},
			}},
			wantErr: events.ErrUnresolvedSecret,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := events.Builder{}.Exporters(context.Background(), specs(tt.settings))
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

// mustSecret decodes a secret reference.
func mustSecret(tb testing.TB, ref string) bootstrap.Secret {
	tb.Helper()

	cfg, err := bootstrap.Parse([]byte("version: 2\nexporters:\n  e: {kind: webhook, endpointUrl: "+ref+"}\n"), nil,
		bootstrap.LoadOptions{LookupEnv: func(string) (string, bool) { return "", false }})
	require.NoError(tb, err)

	return cfg.Exporters["e"].EndpointURL
}

// filesReader reads the files of a map, and no other.
func filesReader(files map[string][]byte) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		if content, ok := files[path]; ok {
			return content, nil
		}

		return nil, os.ErrNotExist
	}
}
