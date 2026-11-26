package bootstrap_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

const control = "version: 2\nconnections:\n  control: {type: s3, bucket: acme-control, region: us-east-1}\n"

type parseErrorCase struct {
	wantErr  error
	name     string
	doc      string
	wantPath string
	wantLine int
}

func runParseErrors(t *testing.T, tests []parseErrorCase) {
	t.Helper()

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			_, err := bootstrap.Parse([]byte(tcase.doc), nil, options(nil, nil, false))
			require.ErrorIs(t, err, tcase.wantErr)

			if tcase.wantPath == "" {
				return
			}

			got := findError(t, err, tcase.wantErr, tcase.wantPath)
			assert.Equal(t, tcase.wantLine, got.Line)
			assert.Contains(t, got.Error(), tcase.wantPath)
		})
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()

	runParseErrors(t, []parseErrorCase{
		{
			name: "unknown key", doc: "version: 2\nbogus: 1\n",
			wantErr: bootstrap.ErrUnknownKey, wantPath: "bogus", wantLine: 2,
		},
		{
			name:     "unknown nested key",
			doc:      "version: 2\nserver:\n  listeners:\n    - {name: api, address: \":1\", extra: 1}\n",
			wantErr:  bootstrap.ErrUnknownKey,
			wantPath: "server.listeners[0].extra",
			wantLine: 4,
		},
		{
			name: "studio is an unknown key", doc: "version: 2\nstudio:\n  enabled: true\n",
			wantErr: bootstrap.ErrUnknownKey, wantPath: "studio", wantLine: 2,
		},
		{
			name: "lambda mode is an unknown key", doc: "version: 2\nserver:\n  mode: lambda\n",
			wantErr: bootstrap.ErrUnknownKey, wantPath: "server.mode", wantLine: 3,
		},
		{
			name: "inline flagSets is reserved", doc: "version: 2\nflagSets:\n  - name: a\n",
			wantErr: bootstrap.ErrReservedKey, wantPath: "flagSets", wantLine: 2,
		},
		{
			name:    "dangling controlPlane connection",
			doc:     control + "controlPlane:\n  connection: nope\n  manifest: {key: m.json}\n",
			wantErr: bootstrap.ErrDanglingReference, wantPath: "controlPlane.connection", wantLine: 5,
		},
		{
			name:    "dangling exporter connection",
			doc:     control + "exporters:\n  e1:\n    kind: s3\n    connection: nope\n",
			wantErr: bootstrap.ErrDanglingReference, wantPath: "exporters.e1.connection", wantLine: 7,
		},
		{
			name:    "exporter connection of the wrong type",
			doc:     control + "exporters:\n  e1:\n    kind: kinesis\n    connection: control\n    streamName: s\n",
			wantErr: bootstrap.ErrWrongConnectionType, wantPath: "exporters.e1.connection", wantLine: 7,
		},
		{
			name:    "duplicate connection name",
			doc:     "version: 2\nconnections:\n  a: {type: s3}\n  a: {type: s3}\n",
			wantErr: bootstrap.ErrDuplicateName, wantPath: "connections.a", wantLine: 4,
		},
		{
			name: "duplicate listener name",
			doc: "version: 2\nserver:\n  listeners:\n" +
				"    - {name: api, address: \":1\"}\n    - {name: api, address: \":2\"}\n",
			wantErr:  bootstrap.ErrDuplicateName,
			wantPath: "server.listeners[1].name",
			wantLine: 5,
		},
		{
			name: "duplicate listener address",
			doc: "version: 2\nserver:\n  listeners:\n    - {name: api, address: \":1\"}\n" +
				"    - {name: ops, address: \":1\", role: ops}\n",
			wantErr:  bootstrap.ErrDuplicateName,
			wantPath: "server.listeners[1].address",
			wantLine: 5,
		},
		{
			name:    "bad duration",
			doc:     control + "controlPlane:\n  connection: control\n  manifest: {key: m.json, pollInterval: soon}\n",
			wantErr: bootstrap.ErrInvalidDuration, wantPath: "controlPlane.manifest.pollInterval", wantLine: 6,
		},
		{
			name:    "duration must be a string",
			doc:     control + "controlPlane:\n  connection: control\n  manifest: {key: m.json, pollInterval: 60}\n",
			wantErr: bootstrap.ErrInvalidDuration, wantPath: "controlPlane.manifest.pollInterval", wantLine: 6,
		},
		{
			name:    "negative duration",
			doc:     "version: 2\nconnections:\n  a: {type: s3, poll: {interval: -5s}}\n",
			wantErr: bootstrap.ErrInvalidDuration, wantPath: "connections.a.poll.interval", wantLine: 3,
		},
		{
			name:    "wrong scalar type",
			doc:     control + "controlPlane:\n  connection: control\n  manifest: {key: m.json, maxObjects: many}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "controlPlane.manifest.maxObjects", wantLine: 6,
		},
		{
			name:    "maxObjects must be positive",
			doc:     control + "controlPlane:\n  connection: control\n  manifest: {key: m.json, maxObjects: 0}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "controlPlane.manifest.maxObjects", wantLine: 6,
		},
		{
			name:    "jitter out of range",
			doc:     "version: 2\nconnections:\n  a: {type: s3, poll: {jitter: 2}}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "connections.a.poll.jitter", wantLine: 3,
		},
		{
			name:    "bad checksum setting",
			doc:     "version: 2\nconnections:\n  a: {type: s3, requestChecksumCalculation: always}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "connections.a.requestChecksumCalculation", wantLine: 3,
		},
		{
			name:    "bad endpoint",
			doc:     "version: 2\nconnections:\n  a: {type: s3, endpoint: localhost}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "connections.a.endpoint", wantLine: 3,
		},
		{
			name:    "uppercase name",
			doc:     "version: 2\nconnections:\n  Control: {type: s3}\n",
			wantErr: bootstrap.ErrInvalidName, wantPath: "connections.Control", wantLine: 3,
		},
		{
			name:    "name too long",
			doc:     "version: 2\nconnections:\n  a" + strings.Repeat("b", 63) + ": {type: s3}\n",
			wantErr: bootstrap.ErrInvalidName, wantPath: "connections.a" + strings.Repeat("b", 63), wantLine: 3,
		},
		{
			name:    "reserved name",
			doc:     "version: 2\nconnections:\n  default: {type: s3}\n",
			wantErr: bootstrap.ErrReservedName, wantPath: "connections.default", wantLine: 3,
		},
		{
			name:    "unsupported connection type",
			doc:     "version: 2\nconnections:\n  cache: {type: valkey}\n",
			wantErr: bootstrap.ErrUnsupportedType, wantPath: pathCacheType, wantLine: 3,
		},
		{
			name:    "unknown connection type",
			doc:     "version: 2\nconnections:\n  cache: {type: nonsense}\n",
			wantErr: bootstrap.ErrUnknownType, wantPath: pathCacheType, wantLine: 3,
		},
		{
			name:    "missing connection type",
			doc:     "version: 2\nconnections:\n  cache: {bucket: b}\n",
			wantErr: bootstrap.ErrMissingValue, wantPath: pathCacheType, wantLine: 3,
		},
		{
			name:    "key of another connection type",
			doc:     "version: 2\nconnections:\n  q: {type: sqs, bucket: b}\n",
			wantErr: bootstrap.ErrUnknownKey, wantPath: "connections.q.bucket", wantLine: 3,
		},
	})
}

func TestParseErrorsSecrets(t *testing.T) {
	t.Parallel()

	runParseErrors(t, []parseErrorCase{
		{
			name: "inline secret",
			doc: "version: 2\nconnections:\n  a:\n    type: s3\n" +
				"    credentials: {type: static, accessKeyId: AKIA, secretAccessKey: {fromEnv: S}}\n",
			wantErr:  bootstrap.ErrInlineSecret,
			wantPath: "connections.a.credentials.accessKeyId",
			wantLine: 5,
		},
		{
			name: "secret with two references",
			doc: "version: 2\nconnections:\n  a:\n    type: s3\n" +
				"    credentials: {type: static, accessKeyId: {fromEnv: A, fromFile: B}}\n",
			wantErr:  bootstrap.ErrInvalidSecret,
			wantPath: "connections.a.credentials.accessKeyId",
			wantLine: 5,
		},
		{
			name: "bad environment variable name",
			doc: "version: 2\nconnections:\n  a:\n    type: s3\n" +
				"    credentials: {type: static, accessKeyId: {fromEnv: \"a b\"}, secretAccessKey: {fromEnv: S}}\n",
			wantErr:  bootstrap.ErrInvalidSecret,
			wantPath: "connections.a.credentials.accessKeyId.fromEnv",
			wantLine: 5,
		},
		{
			name: "static credentials need both keys",
			doc: "version: 2\nconnections:\n  a:\n    type: s3\n" +
				"    credentials: {type: static, accessKeyId: {fromEnv: A}}\n",
			wantErr:  bootstrap.ErrMissingValue,
			wantPath: "connections.a.credentials.secretAccessKey",
			wantLine: 5,
		},
		{
			name:    "credentials key of another credentials type",
			doc:     "version: 2\nconnections:\n  a:\n    type: s3\n    credentials: {roleArn: arn}\n",
			wantErr: bootstrap.ErrNotAllowed, wantPath: "connections.a.credentials.roleArn", wantLine: 5,
		},
		{
			name:    "unknown credentials type",
			doc:     "version: 2\nconnections:\n  a:\n    type: s3\n    credentials: {type: magic}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "connections.a.credentials.type", wantLine: 5,
		},
	})
}

func TestParseErrorsReferences(t *testing.T) {
	t.Parallel()

	runParseErrors(t, []parseErrorCase{
		{
			name:     "controlPlane connection without bucket",
			doc:      "version: 2\nconnections:\n  c: {type: s3}\ncontrolPlane:\n  connection: c\n  manifest: {key: m.json}\n",
			wantErr:  bootstrap.ErrMissingValue,
			wantPath: "controlPlane.connection",
			wantLine: 5,
		},
		{
			name:    "controlPlane without manifest key",
			doc:     control + "controlPlane:\n  connection: control\n",
			wantErr: bootstrap.ErrMissingValue, wantPath: "controlPlane.manifest.key", wantLine: 4,
		},
		{
			name:    "exporter field of another kind",
			doc:     "version: 2\nexporters:\n  e:\n    kind: log\n    bucket: b\n",
			wantErr: bootstrap.ErrNotAllowed, wantPath: "exporters.e.bucket", wantLine: 5,
		},
		{
			name:    "unknown exporter kind",
			doc:     "version: 2\nexporters:\n  e:\n    kind: carrier-pigeon\n",
			wantErr: bootstrap.ErrUnknownType, wantPath: "exporters.e.kind", wantLine: 4,
		},
		{
			name:    "exporter required field",
			doc:     "version: 2\nexporters:\n  e:\n    kind: file\n",
			wantErr: bootstrap.ErrMissingValue, wantPath: "exporters.e.outputDir", wantLine: 3,
		},
		{
			name: "redshift needs cluster or workgroup",
			doc: "version: 2\nconnections:\n  w: {type: redshift}\nexporters:\n" +
				"  e:\n    kind: redshift\n    connection: w\n    database: d\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: "exporters.e",
			wantLine: 5,
		},
		{
			name:    "notifier required field",
			doc:     "version: 2\nnotifiers:\n  n:\n    kind: slack\n",
			wantErr: bootstrap.ErrMissingValue, wantPath: "notifiers.n.webhookUrl", wantLine: 3,
		},
		{
			name:    "unknown notifier kind",
			doc:     "version: 2\nnotifiers:\n  n:\n    kind: sms\n",
			wantErr: bootstrap.ErrUnknownType, wantPath: "notifiers.n.kind", wantLine: 4,
		},
		{
			name:    "reserved h2 protocol",
			doc:     "version: 2\nserver:\n  listeners:\n    - {name: api, address: \":1\", protocols: [h2]}\n",
			wantErr: bootstrap.ErrReservedKey, wantPath: "server.listeners[0].protocols[0]", wantLine: 4,
		},
		{
			name:    "bad listener address",
			doc:     "version: 2\nserver:\n  listeners:\n    - {name: api, address: nope}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "server.listeners[0].address", wantLine: 4,
		},
		{
			name:    "no api listener",
			doc:     "version: 2\nserver:\n  listeners:\n    - {name: ops, address: \":2\", role: ops}\n",
			wantErr: bootstrap.ErrMissingValue, wantPath: "server.listeners", wantLine: 3,
		},
		{
			name:    "bad log level",
			doc:     "version: 2\nobservability:\n  logging: {level: loud}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: levelPath, wantLine: 3,
		},
		{
			name: "syntax error", doc: "version: 2\nserver: [\n",
			wantErr: bootstrap.ErrSyntax,
		},
		{
			name: "wrong shape", doc: "version: 2\nconnections: [a]\n",
			wantErr: bootstrap.ErrSyntax, wantPath: "connections", wantLine: 2,
		},
		{
			name: "root is a list", doc: "- a\n",
			wantErr: bootstrap.ErrSyntax,
		},
	})
}

func TestVersionGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		doc  string
	}{
		{name: "missing version", doc: "connections:\n  a: {type: s3}\n"},
		{name: "empty file", doc: ""},
		{name: "version 1", doc: v1Doc},
		{name: "version 3", doc: "version: 3\n"},
		{name: "version as string", doc: "version: \"2\"\n"},
		{name: "a v1 file", doc: "retrievers:\n  - kind: file\n    path: ./flags.yaml\nlisten: 1031\n"},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			_, err := bootstrap.Parse([]byte(tcase.doc), nil, options(nil, nil, false))
			require.ErrorIs(t, err, bootstrap.ErrVersionRequired)
			assert.Contains(t, err.Error(), "version 2 required")
		})
	}
}

func TestParseValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		doc          string
		wantListener string
		wantKeys     int
		inline       bool
		wantControl  bool
	}{
		{name: "version only", doc: v2Doc, wantListener: defaultAPI},
		{name: "connections only", doc: "version: 2\nconnections:\n  a: {type: s3}\n", wantListener: defaultAPI},
		{
			name:        "controlPlane without anything else optional",
			doc:         control + "controlPlane:\n  connection: control\n  manifest: {key: m.json}\n",
			wantControl: true, wantListener: defaultAPI,
		},
		{
			name:     "admin keys are plain strings without the development flag",
			doc:      "version: 2\nauth:\n  adminKeys: [7d9f1e0c-6a43-4c2e-9d57-0f6b3f0f1a11, {fromEnv: K}]\n",
			wantKeys: 2, wantListener: defaultAPI,
		},
		{
			name: "inline secret with the development flag",
			doc: "version: 2\nconnections:\n  a:\n    type: s3\n" +
				"    credentials: {type: static, accessKeyId: x, secretAccessKey: y}\n",
			inline:       true,
			wantListener: defaultAPI,
		},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := bootstrap.Parse([]byte(tcase.doc), nil, options(nil, nil, tcase.inline))
			require.NoError(t, err)
			assert.Equal(t, 2, cfg.Version)
			assert.Len(t, cfg.Auth.AdminKeys, tcase.wantKeys)
			assert.Equal(t, tcase.wantControl, cfg.ControlPlane != nil)
			assert.Equal(t, tcase.wantListener, cfg.Server.Listeners[0].Address)
		})
	}
}

func TestDefaults(t *testing.T) {
	t.Parallel()

	doc := control + "controlPlane:\n  connection: control\n  manifest: {key: m.json}\n"
	cfg, err := bootstrap.Parse([]byte(doc), nil, options(nil, nil, false))
	require.NoError(t, err)

	s3Conn := cfg.Connections["control"].S3
	require.NotNil(t, s3Conn)

	assert.Equal(t, "60s", s3Conn.Poll.Interval.String())
	assert.InDelta(t, 0.1, *s3Conn.Poll.Jitter, 0)
	assert.Equal(t, bootstrap.CredentialsChain, s3Conn.Credentials.Type)
	assert.Equal(t, "10s", s3Conn.Timeouts.Connect.String())
	assert.Equal(t, "30s", s3Conn.Timeouts.Request.String())
	assert.Equal(t, 3, *s3Conn.Retry.MaxAttempts)
	assert.Equal(t, "20s", s3Conn.Retry.MaxBackoff.String())
	assert.Equal(t, bootstrap.ChecksumWhenSupported, s3Conn.RequestChecksumCalculation)
	assert.Equal(t, bootstrap.ChecksumWhenSupported, s3Conn.ResponseChecksumValidation)

	m := cfg.ControlPlane.Manifest
	assert.Equal(t, "stable", m.Channel)
	assert.Equal(t, "60s", m.PollInterval.String())
	assert.InDelta(t, 0.1, *m.Jitter, 0)
	assert.Equal(t, 20000, *m.MaxObjects)

	assert.Equal(t, "info", cfg.Observability.Logging.Level)
	assert.Equal(t, logFormatDefault, cfg.Observability.Logging.Format)
	assert.True(t, *cfg.Observability.Metrics.Enabled)
	assert.Equal(t, "parentbased_always_off", cfg.Observability.Tracing.Sampler)
	assert.Equal(t, []string{"http1"}, cfg.Server.Listeners[0].Protocols)
	assert.Equal(t, "api", cfg.Server.Listeners[0].Role)
}

func TestEnvOverrides(t *testing.T) {
	t.Parallel()

	doc := "version: 2\nobservability:\n  logging: {level: warn, format: text}\n" +
		"server:\n  listeners:\n    - {name: web, address: \":9000\"}\n" +
		"connections:\n  control: {type: s3, bucket: b}\n" +
		"controlPlane:\n  connection: control\n  manifest: {key: file.json}\n  stateDir: /file\n"

	tests := []struct {
		env           envMap
		flags         bootstrap.Overrides
		name          string
		wantLevel     string
		wantFormat    string
		wantKey       string
		wantState     string
		wantAddresses []string
	}{
		{
			name: "file only", wantLevel: "warn", wantFormat: formatText, wantAddresses: []string{":9000"},
			wantKey: "file.json", wantState: "/file",
		},
		{
			name: "env beats file",
			env: envMap{
				bootstrap.EnvLogLevel:    levelDebug,
				bootstrap.EnvLogFormat:   logFormatDefault,
				bootstrap.EnvListen:      addr8080,
				bootstrap.EnvOpsListen:   opsAddr,
				bootstrap.EnvManifestKey: envKeyJSON,
				bootstrap.EnvStateDir:    "/env",
			},
			wantLevel: levelDebug, wantFormat: logFormatDefault, wantAddresses: []string{addr8080, opsAddr},
			wantKey: envKeyJSON, wantState: "/env",
		},
		{
			name: "flag beats env",
			env: envMap{
				bootstrap.EnvLogLevel:    levelDebug,
				bootstrap.EnvListen:      addr8080,
				bootstrap.EnvManifestKey: envKeyJSON,
			},
			flags: bootstrap.Overrides{
				LogLevel: levelError, Listen: ":7000", ManifestKey: "flag.json", StateDir: "/flag",
			},
			wantLevel:     levelError,
			wantFormat:    formatText,
			wantAddresses: []string{":7000"},
			wantKey:       "flag.json",
			wantState:     "/flag",
		},
		{
			name: "unrelated SEMAPHORE variables change nothing",
			env: envMap{
				"SEMAPHORE_PORT":                       "1",
				"SEMAPHORE_SERVICE_HOST":               "10.0.0.1",
				"SEMAPHORE_LISTEN_ADDR":                ":1",
				"SEMAPHORE_SERVER_LISTENERS_0_ADDRESS": ":2",
				"LOG_LEVEL":                            levelError,
				"SEMAPHORE_RETRIEVERS_0_KIND":          "s3",
			},
			wantLevel: "warn", wantFormat: formatText, wantAddresses: []string{":9000"},
			wantKey: "file.json", wantState: "/file",
		},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := bootstrap.Parse([]byte(doc), &tcase.flags, options(tcase.env, nil, false))
			require.NoError(t, err)

			var addrs []string
			for _, l := range cfg.Server.Listeners {
				addrs = append(addrs, l.Address)
			}

			assert.Equal(t, tcase.wantLevel, cfg.Observability.Logging.Level)
			assert.Equal(t, tcase.wantFormat, cfg.Observability.Logging.Format)
			assert.Equal(t, tcase.wantAddresses, addrs)
			assert.Equal(t, tcase.wantKey, cfg.ControlPlane.Manifest.Key)
			assert.Equal(t, tcase.wantState, cfg.ControlPlane.StateDir)
		})
	}
}

func TestEnvOverridesWithoutControlPlane(t *testing.T) {
	t.Parallel()

	env := envMap{bootstrap.EnvManifestKey: "k.json", bootstrap.EnvStateDir: "/s", bootstrap.EnvOpsListen: ":2"}

	cfg, err := bootstrap.Parse([]byte(v2Doc), nil, options(env, nil, false))
	require.NoError(t, err)
	assert.Nil(t, cfg.ControlPlane)
	assert.Len(t, cfg.Server.Listeners, 2)
}

func TestEnvOverrideIsValidated(t *testing.T) {
	t.Parallel()

	env := envMap{bootstrap.EnvLogLevel: levelLoud}

	_, err := bootstrap.Parse([]byte(v2Doc), nil, options(env, nil, false))
	require.ErrorIs(t, err, bootstrap.ErrInvalidValue)
}

func TestLoad(t *testing.T) {
	t.Parallel()

	files := fileMap{etcA: v2Doc, etcBad: v1Doc}

	tests := []struct {
		name    string
		flags   bootstrap.Overrides
		env     envMap
		opts    func(envMap) bootstrap.LoadOptions
		wantErr error
		wantMsg string
	}{
		{name: "flag path", flags: bootstrap.Overrides{Config: etcA}},
		{name: "env path", env: envMap{bootstrap.EnvConfig: etcA}},
		{
			name:  "flag beats env",
			flags: bootstrap.Overrides{Config: etcA},
			env:   envMap{bootstrap.EnvConfig: etcBad},
		},
		{name: "no path", wantErr: bootstrap.ErrNoConfigPath},
		{name: "v1 file", flags: bootstrap.Overrides{Config: etcBad}, wantErr: bootstrap.ErrVersionRequired},
		{
			name:    "unreadable",
			flags:   bootstrap.Overrides{Config: "/etc/none.yaml"},
			wantMsg: "read bootstrap file /etc/none.yaml",
		},
		{
			name: noReader, flags: bootstrap.Overrides{Config: etcA}, wantErr: bootstrap.ErrNoReader,
			opts: func(e envMap) bootstrap.LoadOptions { return bootstrap.LoadOptions{LookupEnv: e.lookup} },
		},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			opts := options(tcase.env, files, false)
			if tcase.opts != nil {
				opts = tcase.opts(tcase.env)
			}

			cfg, err := bootstrap.Load(&tcase.flags, opts)

			switch {
			case tcase.wantErr != nil:
				require.ErrorIs(t, err, tcase.wantErr)
			case tcase.wantMsg != "":
				require.ErrorContains(t, err, tcase.wantMsg)
			default:
				require.NoError(t, err)
				assert.Equal(t, 2, cfg.Version)
			}
		})
	}
}

func TestLoadWithoutEnvironment(t *testing.T) {
	t.Parallel()

	_, err := bootstrap.Load(nil, bootstrap.LoadOptions{})
	require.ErrorIs(t, err, bootstrap.ErrNoConfigPath)
	assert.NotErrorIs(t, err, bootstrap.ErrNoReader)
}
