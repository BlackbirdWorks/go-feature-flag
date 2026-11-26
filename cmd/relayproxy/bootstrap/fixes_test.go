package bootstrap_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

const s3Pre = "version: 2\nconnections:\n  a:\n    type: s3\n"

func TestParseErrorsDocumentsAndScalars(t *testing.T) {
	t.Parallel()

	runParseErrors(t, []parseErrorCase{
		{name: "second document", doc: "version: 2\n---\nbogus: 1\n", wantErr: bootstrap.ErrSyntax, wantLine: 0},
		{name: "second document is malformed", doc: "version: 2\n---\n[\n", wantErr: bootstrap.ErrSyntax},
		{
			name: "integer for a string", doc: s3Pre + "    bucket: 123\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: tcConnectionsABucket, wantLine: 5,
		},
		{
			name: "boolean for a string", doc: s3Pre + "    region: true\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "connections.a.region", wantLine: 5,
		},
		{
			name: "binary for a string", doc: s3Pre + "    bucket: !!binary aGVsbG8=\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: tcConnectionsABucket, wantLine: 5,
		},
		{
			name: "float for an integer", doc: s3Pre + "    retry: {maxAttempts: 3.0}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: tcConnectionsARetryMaxattempts, wantLine: 5,
		},
		{
			name: "fraction truncated to an integer", doc: s3Pre + "    retry: {maxAttempts: 20.9}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: tcConnectionsARetryMaxattempts, wantLine: 5,
		},
		{
			name: "string for a boolean", doc: s3Pre + "    pathStyle: \"yes\"\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "connections.a.pathStyle", wantLine: 5,
		},
		{
			name: "number for a secret", doc: "version: 2\nauth:\n  adminKeys: [12345]\n",
			wantErr: bootstrap.ErrInvalidSecret, wantPath: tcAuthAdminkeys0, wantLine: 3,
		},
		{
			name: "null secret reference", doc: "version: 2\nauth:\n  adminKeys: [{fromFile: null}]\n",
			wantErr: bootstrap.ErrInvalidSecret, wantPath: tcAuthAdminkeys0, wantLine: 3,
		},
		{
			name: "NaN jitter", doc: s3Pre + "    poll: {jitter: .nan}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "connections.a.poll.jitter", wantLine: 5,
		},
		{
			name: "merge key under a connection", doc: "version: 2\nconnections:\n  a:\n    <<: {type: s3}\n",
			wantErr: bootstrap.ErrUnknownKey, wantPath: "connections.a.<<", wantLine: 4,
		},
		{
			name:     "null value in a map of secrets",
			doc:      "version: 2\nnotifiers:\n  n:\n    kind: webhook\n    endpointUrl: {fromEnv: U}\n    headers: {A: null}\n",
			wantErr:  bootstrap.ErrMissingValue,
			wantPath: "notifiers.n.headers.A",
			wantLine: 6,
		},
		{
			name: "null exporter", doc: "version: 2\nexporters:\n  a: null\n",
			wantErr: bootstrap.ErrMissingValue, wantPath: "exporters.a.kind", wantLine: 3,
		},
	})
}

func TestParseErrorsValidationGaps(t *testing.T) {
	t.Parallel()

	runParseErrors(t, []parseErrorCase{
		{
			name: "userinfo in the endpoint", doc: s3Pre + "    endpoint: http://user:PASSW0RD@host:1\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: tcConnectionsAEndpoint, wantLine: 5,
		},
		{
			name:    "userinfo in the queue URL of a connection",
			doc:     "version: 2\nconnections:\n  q: {type: sqs, queueUrl: \"https://u:p@sqs.example/q\"}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "connections.q.queueUrl", wantLine: 3,
		},
		{
			name: "userinfo in the queue URL of an exporter",
			doc: "version: 2\nconnections:\n  q: {type: sqs}\nexporters:\n  e:\n    kind: sqs\n    connection: q\n" +
				"    queueUrl: \"https://u:p@sqs.example/q\"\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "exporters.e.queueUrl", wantLine: 8,
		},
		{
			name:    "userinfo in the tracing endpoint",
			doc:     "version: 2\nobservability:\n  tracing: {otlpEndpoint: \"http://u:p@otel:4318\"}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: tcObservabilityTracingOtlpendpoint, wantLine: 3,
		},
		{
			name:    "tracing endpoint without a port",
			doc:     "version: 2\nobservability:\n  tracing: {otlpEndpoint: otel}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: tcObservabilityTracingOtlpendpoint, wantLine: 3,
		},
		{
			name:     "userinfo in a kafka address",
			doc:      "version: 2\nexporters:\n  e:\n    kind: kafka\n    kafka: {topic: t, addresses: [\"u:p@kafka:9092\"]}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: tcExportersEKafkaAddresses0,
			wantLine: 5,
		},
		{
			name:    "empty kafka address",
			doc:     "version: 2\nexporters:\n  e:\n    kind: kafka\n    kafka: {topic: t, addresses: [\"\"]}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: tcExportersEKafkaAddresses0, wantLine: 5,
		},
		{
			name:    "maxEventInMemory below one",
			doc:     "version: 2\nexporters:\n  e:\n    kind: log\n    maxEventInMemory: 0\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "exporters.e.maxEventInMemory", wantLine: 5,
		},
		{
			name:     "duplicate protocol",
			doc:      "version: 2\nserver:\n  listeners:\n    - {name: api, address: \":1\", protocols: [http1, http1]}\n",
			wantErr:  bootstrap.ErrDuplicateName,
			wantPath: "server.listeners[0].protocols[1]",
			wantLine: 4,
		},
		{
			name:    "blank channel",
			doc:     control + "controlPlane:\n  connection: control\n  manifest: {key: m.json, channel: \"  \"}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "controlPlane.manifest.channel", wantLine: 6,
		},
		{
			name:    "zero value of a key of another kind",
			doc:     "version: 2\nexporters:\n  e:\n    kind: log\n    autoMigrate: false\n",
			wantErr: bootstrap.ErrNotAllowed, wantPath: "exporters.e.autoMigrate", wantLine: 5,
		},
		{
			name:     "empty string of a key of another kind",
			doc:      "version: 2\nnotifiers:\n  n:\n    kind: slack\n    webhookUrl: {fromEnv: U}\n    endpointUrl: \"\"\n",
			wantErr:  bootstrap.ErrNotAllowed,
			wantPath: "notifiers.n.endpointUrl",
			wantLine: 6,
		},
		{
			name:    "externalId is a secret",
			doc:     s3Pre + "    credentials: {type: assumeRole, roleArn: arn, externalId: plain}\n",
			wantErr: bootstrap.ErrInlineSecret, wantPath: "connections.a.credentials.externalId", wantLine: 5,
		},
	})
}

func TestParseValidFixes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		doc          string
		wantControl  bool
		wantExporter int
	}{
		{name: "null controlPlane", doc: "version: 2\ncontrolPlane: null\n"},
		{
			name: "externalId by reference",
			doc:  s3Pre + "    credentials: {type: assumeRole, roleArn: arn, externalId: {fromEnv: X}}\n",
		},
		{
			name: "tracing endpoint as host:port",
			doc:  "version: 2\nobservability:\n  tracing: {otlpEndpoint: \"otel:4317\"}\n",
		},
		{
			name: "tracing endpoint as URL",
			doc:  "version: 2\nobservability:\n  tracing: {otlpEndpoint: \"https://otel:4318\"}\n",
		},
		{name: "anchored exporter", doc: "version: 2\nexporters:\n  a: &x {kind: log}\n  b: *x\n", wantExporter: 2},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := bootstrap.Parse([]byte(tcase.doc), nil, options(nil, nil, false))
			require.NoError(t, err)
			assert.Equal(t, tcase.wantControl, cfg.ControlPlane != nil)
			assert.Len(t, cfg.Exporters, tcase.wantExporter)
		})
	}
}

func TestEmptyInlineSecretWithDevelopmentFlag(t *testing.T) {
	t.Parallel()

	doc := s3Pre + "    credentials: {type: static, accessKeyId: \"\", secretAccessKey: x}\n"

	_, err := bootstrap.Parse([]byte(doc), nil, options(nil, nil, true))
	require.ErrorIs(t, err, bootstrap.ErrMissingValue)
	require.NotNil(t, findError(t, err, bootstrap.ErrMissingValue, "connections.a.credentials.accessKeyId"))
}

func TestAuthKeysAreNotEchoed(t *testing.T) {
	t.Parallel()

	const canary = "CANARY-misindented-admin-key"

	tests := []struct {
		name string
		doc  string
		line int
	}{
		{name: "key under auth", doc: "version: 2\nauth:\n  adminKeys: []\n  " + canary + ": x\n", line: 4},
		{
			name: "duplicate key under auth",
			doc:  "version: 2\nauth:\n  " + canary + ": x\n  " + canary + ": y\n",
			line: 3,
		},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			_, err := bootstrap.Parse([]byte(tcase.doc), nil, options(nil, nil, true))
			require.ErrorIs(t, err, bootstrap.ErrUnknownKey)
			assert.NotContains(t, err.Error(), canary)

			got := findError(t, err, bootstrap.ErrUnknownKey, tcAuth)
			assert.Equal(t, tcase.line, got.Line)
		})
	}
}

func TestOverrideErrorsNameTheirSource(t *testing.T) {
	t.Parallel()

	doc := "version: 2\nobservability:\n  logging: {level: warn}\nconnections:\n  control: {type: s3, bucket: b}\n" +
		"controlPlane:\n  connection: control\n  manifest: {key: file.json}\n"

	tests := []struct {
		name     string
		env      envMap
		flags    bootstrap.Overrides
		wantPath string
		wantFrom string
	}{
		{
			name: "variable", env: envMap{bootstrap.EnvLogLevel: levelLoud},
			wantPath: levelPath, wantFrom: "environment variable " + bootstrap.EnvLogLevel,
		},
		{
			name: "flag beats the variable", env: envMap{bootstrap.EnvLogLevel: "debug"},
			flags:    bootstrap.Overrides{LogLevel: levelLoud},
			wantPath: levelPath, wantFrom: "flag --log-level",
		},
		{
			name: "listener address", env: envMap{bootstrap.EnvListen: "nope"},
			wantPath: tcServerListeners0Address, wantFrom: "environment variable " + bootstrap.EnvListen,
		},
		{
			name: "ops listener address", flags: bootstrap.Overrides{OpsListen: "nope"},
			wantPath: "server.listeners[1].address", wantFrom: "flag --ops-listen",
		},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			_, err := bootstrap.Parse([]byte(doc), &tcase.flags, options(tcase.env, nil, false))
			require.ErrorIs(t, err, bootstrap.ErrInvalidValue)

			got := findError(t, err, bootstrap.ErrInvalidValue, tcase.wantPath)
			assert.Zero(t, got.Line)
			assert.Contains(t, got.Error(), "set by "+tcase.wantFrom)
		})
	}
}

func TestOpsListenDoesNotCollideWithAListenerName(t *testing.T) {
	t.Parallel()

	doc := "version: 2\nserver:\n  listeners:\n    - {name: ops, address: \":1031\"}\n"
	env := envMap{bootstrap.EnvOpsListen: opsAddr}

	cfg, err := bootstrap.Parse([]byte(doc), nil, options(env, nil, false))
	require.NoError(t, err)

	got := map[string]string{}
	for _, l := range cfg.Server.Listeners {
		got[l.Name] = l.Role + " " + l.Address
	}

	assert.Equal(t, map[string]string{"ops": "api :1031", "ops-2": "ops 127.0.0.1:9090"}, got)
}

func TestUnrelatedVariablesAreNeverRead(t *testing.T) {
	t.Parallel()

	var read []string

	opts := bootstrap.LoadOptions{LookupEnv: func(name string) (string, bool) {
		read = append(read, name)

		return "", false
	}}

	_, err := bootstrap.Parse([]byte("version: 2\n"), nil, opts)
	require.NoError(t, err)

	want := []string{
		bootstrap.EnvLogLevel, bootstrap.EnvLogFormat, bootstrap.EnvListen, bootstrap.EnvOpsListen,
	}
	assert.Equal(t, want, read)
}
