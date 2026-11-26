package bootstrap_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

const (
	canaryCredential = "CANARY-credential-6f1c"
	canaryAdminKey   = "CANARY-admin-key-9b2e"
)

const canaryDoc = `version: 2
connections:
  dev:
    type: s3
    bucket: dev
    credentials:
      type: static
      accessKeyId: CANARY-credential-6f1c
      secretAccessKey: CANARY-credential-6f1c
notifiers:
  n:
    kind: webhook
    endpointUrl: CANARY-credential-6f1c
    headers: {Authorization: CANARY-credential-6f1c}
auth:
  adminKeys: [CANARY-admin-key-9b2e, CANARY-admin-key-9b2e]
`

// TestCanaryNeverLeaks checks that a sentinel credential and a sentinel admin key appear in no error, no
// formatted value, no encoding, no effective print and no log line.
func TestCanaryNeverLeaks(t *testing.T) {
	t.Parallel()

	// the document with inline secrets allowed, and the same document as the default loader sees it
	cfg, err := bootstrap.Parse([]byte(canaryDoc), nil, options(nil, nil, true))
	require.ErrorIs(t, err, bootstrap.ErrDuplicateKey)
	require.Nil(t, cfg)

	_, strictErr := bootstrap.Parse([]byte(canaryDoc), nil, options(nil, nil, false))
	require.ErrorIs(t, strictErr, bootstrap.ErrInlineSecret)

	valid := bytes.Replace([]byte(canaryDoc), []byte("adminKeys: [CANARY-admin-key-9b2e, CANARY-admin-key-9b2e]"),
		[]byte("adminKeys: [CANARY-admin-key-9b2e]"), 1)

	cfg, err = bootstrap.Parse(valid, nil, options(nil, nil, true))
	require.NoError(t, err)

	var printed bytes.Buffer
	require.NoError(t, cfg.Print(&printed))

	asYAML, err := yaml.Marshal(cfg)
	require.NoError(t, err)

	asJSON, err := json.Marshal(cfg)
	require.NoError(t, err)

	var logs bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	logger.Info("config", slog.Any("config", cfg), slog.Any("keys", cfg.Auth.AdminKeys),
		slog.Any("conn", cfg.Connections["dev"].S3.Credentials))
	logger.Info("config", slog.Any("error", strictErr))

	outputs := map[string]string{
		"inline secret error": strictErr.Error(),
		"effective print":     printed.String(),
		"yaml":                string(asYAML),
		logFormatDefault:      string(asJSON),
		"%v":                  fmt.Sprintf("%v", cfg),
		fmtPlus:               fmt.Sprintf(fmtPlus, cfg),
		fmtGo:                 fmt.Sprintf(fmtGo, cfg),
		"%v of the admin keys": fmt.Sprintf(
			"%v %+v %s",
			cfg.Auth.AdminKeys,
			cfg.Auth.AdminKeys,
			cfg.Auth.AdminKeys[0],
		),
		"%+v of the credentials": fmt.Sprintf(fmtPlus, *cfg.Connections["dev"].S3.Credentials),
		"slog":                   logs.String(),
	}

	_, dupErr := bootstrap.Parse([]byte(canaryDoc), nil, options(nil, nil, true))
	outputs["duplicate key error"] = dupErr.Error()

	for name, out := range outputs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.NotContains(t, out, canaryCredential)
			assert.NotContains(t, out, canaryAdminKey)
		})
	}

	assert.Contains(t, printed.String(), bootstrap.RedactedValue)
}

// TestCanaryInErrorPaths checks that a sentinel placed in a wrong-typed or invalid field never reaches an error.
func TestCanaryInErrorPaths(t *testing.T) {
	t.Parallel()

	const canary = "CANARY-error-path-7d41"

	tests := []struct {
		name     string
		doc      string
		wantPath string
	}{
		{
			name: "string where a mapping is expected",
			doc:  "connections:\n  a: {type: s3, credentials: " + canary + "}\n", wantPath: "connections.a.credentials",
		},
		{
			name: "mapping where a string is expected",
			doc:  "connections:\n  a: {type: s3, bucket: {k: " + canary + "}}\n", wantPath: tcConnectionsABucket,
		},
		{
			name:     "text where an integer is expected",
			doc:      "connections:\n  a: {type: s3, retry: {maxAttempts: " + canary + "}}\n",
			wantPath: tcConnectionsARetryMaxattempts,
		},
		{
			name:     "text where a duration is expected",
			doc:      "connections:\n  a: {type: s3, timeouts: {connect: " + canary + "}}\n",
			wantPath: "connections.a.timeouts.connect",
		},
		{
			name:     "text as credentials type",
			doc:      "connections:\n  a: {type: s3, credentials: {type: " + canary + "}}\n",
			wantPath: "connections.a.credentials.type",
		},
		{
			name:     "text as a secret reference key",
			doc:      "auth: {adminKeys: [{" + canary + ": x}]}\n",
			wantPath: tcAuthAdminkeys0,
		},
		{
			name:     "text as an environment variable name",
			doc:      "auth: {adminKeys: [{fromEnv: \"" + canary + " x\"}]}\n",
			wantPath: "auth.adminKeys[0].fromEnv",
		},
		{
			name:     "number as an admin key",
			doc:      "auth: {adminKeys: [12345, " + canary + ", " + canary + "]}\n",
			wantPath: tcAuthAdminkeys0,
		},
		{
			name:     "userinfo in the endpoint",
			doc:      "connections:\n  a: {type: s3, endpoint: \"http://u:" + canary + "@h:1\"}\n",
			wantPath: tcConnectionsAEndpoint,
		},
		{
			name:     "userinfo in the queue URL",
			doc:      "connections:\n  a: {type: sqs, queueUrl: \"https://u:" + canary + "@h/q\"}\n",
			wantPath: "connections.a.queueUrl",
		},
		{
			name:     "userinfo in the tracing endpoint",
			doc:      "observability: {tracing: {otlpEndpoint: \"u:" + canary + "@h:1\"}}\n",
			wantPath: tcObservabilityTracingOtlpendpoint,
		},
		{
			name:     "userinfo in a kafka address",
			doc:      "exporters:\n  e: {kind: kafka, kafka: {topic: t, addresses: [\"u:" + canary + "@h:1\"]}}\n",
			wantPath: tcExportersEKafkaAddresses0,
		},
		{
			name:     "text as the log level",
			doc:      "observability: {logging: {level: " + canary + "}}\n",
			wantPath: "observability.logging.level",
		},
		{
			name:     "text as a listener address",
			doc:      "server: {listeners: [{name: api, address: " + canary + "}]}\n",
			wantPath: tcServerListeners0Address,
		},
		{name: "unknown key under auth", doc: "auth:\n  " + canary + ": x\n", wantPath: "auth"},
		{
			name:     "inline secret in a header",
			doc:      "notifiers:\n  n: {kind: webhook, endpointUrl: {fromEnv: U}, headers: {A: " + canary + "}}\n",
			wantPath: "notifiers.n.headers.A",
		},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			_, err := bootstrap.Parse([]byte("version: 2\n"+tcase.doc), nil, options(nil, nil, false))
			require.Error(t, err)
			assert.NotContains(t, err.Error(), canary)

			// the document is rejected for the reason the case is about, not for an unrelated one
			reasons := pathErrors(err)
			require.Len(t, reasons, 1, err.Error())
			assert.Equal(t, tcase.wantPath, reasons[0].Path)

			for _, e := range pathErrors(err) {
				assert.NotContains(t, e.Path, canary)
				assert.NotContains(t, e.Detail, canary)
			}
		})
	}
}

// TestCanaryInResolveErrors checks the error paths of Secret.Resolve.
func TestCanaryInResolveErrors(t *testing.T) {
	t.Parallel()

	const canary = "CANARY-resolve-5a90"

	failing := bootstrap.LoadOptions{
		LookupEnv: func(name string) (string, bool) { return canary, name == "OTHER" },
		ReadFile:  func(string) ([]byte, error) { return []byte(canary), errNoFile },
	}

	tests := []struct {
		name string
		doc  string
		opts bootstrap.LoadOptions
	}{
		{name: "unset variable", doc: "{fromEnv: MISSING}", opts: failing},
		{name: "unreadable file with content", doc: "{fromFile: /run/missing}", opts: failing},
		{name: noReader, doc: "{fromFile: /run/x}", opts: bootstrap.LoadOptions{}},
		{name: "unset secret", doc: "null", opts: failing},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			_, err := secretOf(t, tcase.doc).Resolve(tcase.opts)
			require.ErrorIs(t, err, bootstrap.ErrSecretUnresolved)
			assert.NotContains(t, err.Error(), canary)
		})
	}
}

// TestCanaryThroughResolvedSecret checks every formatting verb of a resolved secret, alone and inside a struct.
func TestCanaryThroughResolvedSecret(t *testing.T) {
	t.Parallel()

	const canary = "CANARY-resolved-c3b8"

	resolved, err := secretOf(t, "{fromEnv: T}").Resolve(options(envMap{"T": canary}, nil, false))
	require.NoError(t, err)

	holder := struct {
		Ptr    *bootstrap.ResolvedSecret
		Name   string
		Secret bootstrap.ResolvedSecret
	}{Name: "n", Secret: resolved, Ptr: &resolved}

	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%T", "%10.3s", "%-20v"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()

			if verb != "%T" {
				assert.Contains(t, fmt.Sprintf(verb, resolved), bootstrap.RedactedValue[:3])
			}

			assert.NotContains(t, fmt.Sprintf(verb, resolved), canary)
			assert.NotContains(t, fmt.Sprintf(verb, &resolved), canary)
			assert.NotContains(t, fmt.Sprintf(verb, holder), canary)
			assert.NotContains(t, fmt.Sprintf(verb, []bootstrap.ResolvedSecret{resolved}), canary)
			assert.NotContains(t, fmt.Sprintf(verb, map[string]bootstrap.ResolvedSecret{"k": resolved}), canary)
		})
	}
}
