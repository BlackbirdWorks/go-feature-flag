package connection_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/connection"
)

const canary = "CANARY-sEcReT-9f3a"

func canarySpec() *connection.S3Spec {
	return other(func(spec *connection.S3Spec) {
		spec.Credentials = connection.Credentials{
			Type:            connection.CredentialsStatic,
			AccessKeyID:     canary + "-id",
			SecretAccessKey: canary + "-secret",
			SessionToken:    canary + "-token",
		}
	})
}

func TestSecret_Redacts(t *testing.T) {
	t.Parallel()

	secret := connection.Secret(canary)

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
		{name: "%+v", format: "%+v"},
		{name: "%#v", format: "%#v"},
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

	_, reqErr := client.HeadBucket(t.Context(), nil)
	require.Error(t, reqErr)

	_, unknownErr := fac.S3(t.Context(), "missing")
	require.Error(t, unknownErr)

	bad := canarySpec()
	bad.RequestChecksumCalculation = "bogus"
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
		"identity":      spec.Identity(),
		"request error": reqErr.Error(),
		"unknown error": unknownErr.Error(),
		"spec error":    specErr.Error(),
		"kind error":    kindErr.Error(),
		"spec %v":       fmt.Sprintf("%v", spec),
		"spec %+v":      fmt.Sprintf("%+v", spec),
		"spec %#v":      fmt.Sprintf("%#v", spec),
		"creds %v":      fmt.Sprintf("%v", spec.Credentials),
		"creds %+v":     fmt.Sprintf("%+v", spec.Credentials),
		"creds %#v":     fmt.Sprintf("%#v", spec.Credentials),
		"aws spec %#v":  fmt.Sprintf("%#v", &connection.AWSSpec{Type: connection.KindSQS, Common: spec.Common}),
		"factory %v":    fac.String(),
		"factory %+v":   fmt.Sprintf("%+v", fac),
		"factory %#v":   fmt.Sprintf("%#v", fac),
		"spec json":     string(jsSpec),
		"creds json":    string(jsCreds),
		"builds":        fmt.Sprint(fac.Builds()),
		"names":         strings.Join(fac.Names(), ","),
		"slog":          logs.String(),
	}

	for name, out := range outputs {
		assert.NotContains(t, out, canary, name)
	}

	assert.NotEmpty(t, logs.String(), "the build is logged")
}
