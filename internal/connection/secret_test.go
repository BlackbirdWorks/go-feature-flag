package connection_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/connection"
)

const canary = "CANARY-sEcReT-9f3a"

func canarySpec() *connection.S3Spec {
	return other(func(spec *connection.S3Spec) {
		spec.Credentials = connection.Credentials{
			Type:            connection.CredentialsStatic,
			AccessKeyID:     connection.NewSecret(canary + "-id"),
			SecretAccessKey: connection.NewSecret(canary + "-secret"),
			SessionToken:    connection.NewSecret(canary + "-token"),
		}
	})
}

func TestSecret_Redacts(t *testing.T) {
	t.Parallel()

	secret := connection.NewSecret(canary)

	var logs bytes.Buffer

	slog.New(slog.NewJSONHandler(&logs, nil)).LogAttrs(context.Background(), slog.LevelInfo, "m", slog.Any("s", secret))

	var logText bytes.Buffer

	slog.New(slog.NewTextHandler(&logText, nil)).
		LogAttrs(context.Background(), slog.LevelInfo, "m", slog.Any("s", secret))

	encoded, err := json.Marshal(struct {
		Secret connection.Secret `json:"secret"`
	}{secret})
	require.NoError(t, err)

	text, err := secret.MarshalText()
	require.NoError(t, err)

	tests := []struct {
		name   string
		format string // when set, got is the secret formatted with it
		got    string
	}{
		{name: "%s", format: "%s"},
		{name: "%v", format: "%v"},
		{name: verbPlusV, format: verbPlusV},
		{name: verbGoV, format: verbGoV},
		{name: "%q", format: "%q"},
		{name: "%x", format: "%x"},
		{name: "String", got: secret.String()},
		{name: "GoString", got: secret.GoString()},
		{name: "MarshalText", got: string(text)},
		{name: "json", got: string(encoded)},
		{name: "slog json", got: logs.String()},
		{name: "slog text", got: logText.String()},
		{name: "LogValue", got: secret.LogValue().String()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.got
			if tt.format != "" {
				got = fmt.Sprintf(tt.format, secret)
			}

			assert.NotContains(t, got, canary)
			assert.NotEmpty(t, got)
		})
	}

	assert.Contains(t, logs.String(), connection.RedactedValue)
	assert.JSONEq(t, `{"secret":"[REDACTED]"}`, string(encoded))
	assert.Equal(t, canary, secret.Reveal())
}

// TestCanary_NoSecretLeaks runs the whole life of a connection with canary secrets and checks that no
// output, error, rendering or log line contains one.
func TestCanary_NoSecretLeaks(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	transport := newScripted(step{status: 403, body: accessDeniedXML})
	fac := connection.New(
		connection.WithRoundTripper(transport),
		connection.WithLogger(logger),
		connection.WithObserver(&recorder{}),
	)

	spec := canarySpec()
	require.NoError(t, fac.Set("c", spec))

	client, err := fac.S3(t.Context(), "c")
	require.NoError(t, err)

	_, reqErr := client.HeadBucket(t.Context(), &s3.HeadBucketInput{Bucket: aws.String("b")})
	require.Error(t, reqErr)
	require.Equal(t, 1, transport.count(), "the signed request reached the scripted transport")
	assert.Contains(t, reqErr.Error(), "403")

	cfg, err := fac.AWSConfig(t.Context(), "c")
	require.NoError(t, err)

	// the credentials were retrieved to sign the request: the caches hold them now
	creds, err := cfg.Credentials.Retrieve(t.Context())
	require.NoError(t, err)
	require.Equal(t, canary+"-id", creds.AccessKeyID)

	_, unknownErr := fac.S3(t.Context(), "missing")
	require.Error(t, unknownErr)

	bad := canarySpec()
	bad.RequestChecksumCalculation = valBogus
	specErr := fac.Set("bad", bad)
	require.Error(t, specErr)

	_, kindErr := fac.S3(t.Context(), "c") // fine; then a kind mismatch on an AWS spec
	require.NoError(t, kindErr)
	require.NoError(t, fac.Set("q", &connection.AWSSpec{Type: connection.KindSQS, Common: spec.Common}))
	_, kindErr = fac.S3(t.Context(), "q")
	require.Error(t, kindErr)

	jsSpec, err := json.Marshal(spec)
	require.NoError(t, err)

	jsCreds, err := json.Marshal(spec.Credentials)
	require.NoError(t, err)

	outputs := map[string]string{
		"identity":           spec.Identity(),
		"request error":      reqErr.Error(),
		"unknown error":      unknownErr.Error(),
		"spec error":         specErr.Error(),
		"kind error":         kindErr.Error(),
		"spec %v":            fmt.Sprintf("%v", spec),
		"spec %+v":           fmt.Sprintf(verbPlusV, spec),
		"spec %#v":           fmt.Sprintf(verbGoV, spec),
		"creds %v":           fmt.Sprintf("%v", spec.Credentials),
		"creds %+v":          fmt.Sprintf(verbPlusV, spec.Credentials),
		"creds %#v":          fmt.Sprintf(verbGoV, spec.Credentials),
		"aws spec %#v":       fmt.Sprintf(verbGoV, &connection.AWSSpec{Type: connection.KindSQS, Common: spec.Common}),
		"aws config %v":      fmt.Sprintf("%v", cfg),
		"aws config %+v":     fmt.Sprintf(verbPlusV, cfg),
		"aws config %#v":     fmt.Sprintf(verbGoV, cfg),
		"client options %v":  fmt.Sprintf("%v", client.Options()),
		"client options %+v": fmt.Sprintf(verbPlusV, client.Options()),
		"client options %#v": fmt.Sprintf(verbGoV, client.Options()),
		"factory %v":         fac.String(),
		"factory %+v":        fmt.Sprintf(verbPlusV, fac),
		"factory %#v":        fmt.Sprintf(verbGoV, fac),
		"spec json":          string(jsSpec),
		"creds json":         string(jsCreds),
		"builds":             fmt.Sprint(fac.Builds()),
		"names":              strings.Join(fac.Names(), ","),
		"slog":               logs.String(),
	}

	for name, out := range outputs {
		assert.NotContains(t, out, canary, name)
	}

	assert.NotEmpty(t, logs.String(), "the build is logged")
	assert.NotContains(t, logs.String(), spec.Identity(), "the identity digest is not logged")
	assert.Contains(t, logs.String(), `"connection":"c"`)
}

type nestedSecrets struct {
	Pointer *connection.Secret           `json:"pointer"`
	ByKey   map[connection.Secret]int    `json:"byKey"`
	ByValue map[string]connection.Secret `json:"byValue"`
	Inner   struct {
		Secret connection.Secret `json:"secret"`
	} `json:"inner"`
	Plain connection.Secret `json:"plain"`
}

func TestSecret_EveryVerbAndEncoder(t *testing.T) {
	t.Parallel()

	secret := connection.NewSecret(canary)
	nested := nestedSecrets{
		Pointer: &secret,
		ByKey:   map[connection.Secret]int{secret: 1},
		ByValue: map[string]connection.Secret{"k": secret},
		Plain:   secret,
	}
	nested.Inner.Secret = secret

	encoded, err := json.Marshal(nested)
	require.NoError(t, err)

	var jsonLogs, textLogs bytes.Buffer

	slog.New(slog.NewJSONHandler(&jsonLogs, nil)).LogAttrs(t.Context(), slog.LevelInfo, "m",
		slog.Any("secret", secret), slog.Any("nested", nested), slog.Any("pointer", &secret))
	slog.New(slog.NewTextHandler(&textLogs, nil)).LogAttrs(t.Context(), slog.LevelInfo, "m",
		slog.Any("secret", secret), slog.Any("nested", nested), slog.Any("pointer", &secret))

	outputs := map[string]string{
		"json nested":    string(encoded),
		"slog json":      jsonLogs.String(),
		"slog text":      textLogs.String(),
		"%v nested":      fmt.Sprintf("%v", nested),
		"%+v nested":     fmt.Sprintf(verbPlusV, nested),
		"%#v nested":     fmt.Sprintf(verbGoV, nested),
		"%v slice":       fmt.Sprintf("%v", []connection.Secret{secret}),
		"%v map":         fmt.Sprintf("%v", map[string]connection.Secret{"k": secret}),
		"%v map key":     fmt.Sprintf("%v", map[connection.Secret]int{secret: 1}),
		"%v error wrap":  fmt.Errorf("failed with %v: %w", secret, errScriptExhausted).Error(),
		"String":         secret.String(),
		"GoString":       secret.GoString(),
		"LogValue":       secret.LogValue().String(),
		"LogValue group": slog.GroupValue(slog.Any("s", secret)).String(),
	}

	verbs := []string{
		"%v",
		verbPlusV,
		verbGoV,
		"%s",
		"%q",
		"%d",
		"%x",
		"%X",
		"%c",
		"%U",
		"%t",
		"%e",
		"%08.3f",
		"%10s",
		"%-10v",
	}

	for _, verb := range verbs {
		outputs[verb] = fmt.Sprintf(verb, secret)
		outputs[verb+" pointer"] = fmt.Sprintf(verb, &secret)
		outputs[verb+" nested"] = fmt.Sprintf(verb, nested)
	}

	marshalled, err := secret.MarshalYAML()
	require.NoError(t, err)

	outputs["yaml"] = fmt.Sprint(marshalled)

	for name, out := range outputs {
		assert.NotContains(t, out, canary, name)
		assert.NotEmpty(t, out, name)
	}

	assert.JSONEq(t, `{"pointer":"[REDACTED]","byKey":{"[REDACTED]":1},"byValue":{"k":"[REDACTED]"},`+
		`"inner":{"secret":"[REDACTED]"},"plain":"[REDACTED]"}`, string(encoded))
	assert.Equal(t, "[REDACTED]", fmt.Sprintf("%d", secret))
	assert.Equal(t, "[REDACTED]", fmt.Sprintf("%x", secret))
	assert.Equal(t, "[REDACTED]", marshalled)
	assert.True(t, connection.NewSecret("").IsZero())
	assert.False(t, secret.IsZero())
}
