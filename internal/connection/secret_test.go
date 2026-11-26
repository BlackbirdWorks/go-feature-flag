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

// TestSecret_FormatVerbs checks every fmt verb, on the value, a pointer, a slice, a map value and a struct
// field: each prints the marker and nothing else. It fails if a verb reached the value (%d and %x do for a
// plain string type).
func TestSecret_FormatVerbs(t *testing.T) {
	t.Parallel()

	secret := connection.NewSecret(canary)
	nested := nestedSecrets{Plain: secret}
	populated := nestedSecrets{
		Pointer: &secret,
		ByKey:   map[connection.Secret]int{secret: 1},
		ByValue: map[string]connection.Secret{"k": secret},
		Plain:   secret,
	}
	populated.Inner.Secret = secret

	verbs := []string{
		"%v", verbPlusV, verbGoV, "%s", "%q", "%d", "%x", "%X", "%c", "%U", "%t", "%e", "%08.3f", "%10s", "%-10v",
	}

	for _, verb := range verbs {
		tests := []struct {
			got       string
			name      string
			wantExact string // the whole output, when it does not depend on the verb
		}{
			{name: "value", got: fmt.Sprintf(verb, secret), wantExact: connection.RedactedValue},
			{name: "pointer", got: fmt.Sprintf(verb, &secret), wantExact: connection.RedactedValue},
			{name: "slice", got: fmt.Sprintf(verb, []connection.Secret{secret})},
			{name: "struct", got: fmt.Sprintf(verb, nested)},
			{name: "map of secret values", got: fmt.Sprintf(verb, map[string]connection.Secret{"k": secret})},
			{name: "map keyed by secrets", got: fmt.Sprintf(verb, map[connection.Secret]string{secret: "v"})},
			{name: "map of secret pointers", got: fmt.Sprintf(verb, map[string]*connection.Secret{"k": &secret})},
			{name: "struct with populated maps", got: fmt.Sprintf(verb, populated)},
			{name: "pointer to a struct with populated maps", got: fmt.Sprintf(verb, &populated)},
		}

		for _, tt := range tests {
			t.Run(verb+" "+tt.name, func(t *testing.T) {
				t.Parallel()

				assert.NotContains(t, tt.got, canary)
				assert.Contains(t, tt.got, connection.RedactedValue)

				if tt.wantExact != "" {
					assert.Equal(t, tt.wantExact, tt.got)
				}
			})
		}
	}

	assert.Equal(t, connection.RedactedValue, fmt.Sprintf("%d", secret))
	assert.Equal(t, connection.RedactedValue, fmt.Sprintf("%x", secret))
	assert.Equal(t, canary, secret.Reveal())
	assert.True(t, connection.NewSecret("").IsZero())
	assert.False(t, secret.IsZero())
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

// TestSecret_Encoders checks what each encoder and log handler prints for a Secret and for structures that
// hold one. Each case compares the whole output with the expected one.
func TestSecret_Encoders(t *testing.T) {
	t.Parallel()

	secret := connection.NewSecret(canary)
	other := connection.NewSecret(canary + "-other")
	nested := nestedSecrets{
		Pointer: &secret,
		ByKey:   map[connection.Secret]int{secret: 1},
		ByValue: map[string]connection.Secret{"k": secret},
		Plain:   secret,
	}
	nested.Inner.Secret = secret

	marshalled, yamlErr := secret.MarshalYAML()
	require.NoError(t, yamlErr)

	tests := []struct {
		got       func() (string, error)
		name      string
		want      string // exact output
		wantInLog string // the log line must contain it
		wantJSON  bool   // compare as JSON
	}{
		{
			name: "json value", wantJSON: true, want: `"[REDACTED]"`,
			got: func() (string, error) { return marshalString(secret) },
		},
		{
			name: "json nested structure", wantJSON: true,
			want: `{"pointer":"[REDACTED]","byKey":{"[REDACTED]":1},"byValue":{"k":"[REDACTED]"},` +
				`"inner":{"secret":"[REDACTED]"},"plain":"[REDACTED]"}`,
			got: func() (string, error) { return marshalString(nested) },
		},
		{
			name: "json slice", wantJSON: true, want: `["[REDACTED]","[REDACTED]"]`,
			got: func() (string, error) { return marshalString([]connection.Secret{secret, other}) },
		},
		{
			name: "text", want: connection.RedactedValue,
			got: func() (string, error) { return textString(secret) },
		},
		{
			name: "yaml", want: connection.RedactedValue,
			got: func() (string, error) { return fmt.Sprint(marshalled), nil },
		},
		{name: "String", want: connection.RedactedValue, got: func() (string, error) { return secret.String(), nil }},
		{
			name: "GoString",
			want: connection.RedactedValue,
			got:  func() (string, error) { return secret.GoString(), nil },
		},
		{
			name: "LogValue", want: connection.RedactedValue,
			got: func() (string, error) { return secret.LogValue().String(), nil },
		},
		{
			name: "wrapped error", want: "failed with [REDACTED]: " + errScriptExhausted.Error(),
			got: func() (string, error) {
				return fmt.Errorf("failed with %v: %w", secret, errScriptExhausted).Error(), nil
			},
		},
		{
			name: "slog json", wantInLog: `"secret":"[REDACTED]"`,
			got: func() (string, error) {
				var buf bytes.Buffer

				slog.New(slog.NewJSONHandler(&buf, nil)).LogAttrs(context.Background(), slog.LevelInfo, "m",
					slog.Any("secret", secret), slog.Any("nested", nested), slog.Any("pointer", &secret))

				return buf.String(), nil
			},
		},
		{
			name: "slog text", wantInLog: "secret=[REDACTED]",
			got: func() (string, error) {
				var buf bytes.Buffer

				slog.New(slog.NewTextHandler(&buf, nil)).LogAttrs(context.Background(), slog.LevelInfo, "m",
					slog.Any("secret", secret), slog.Any("nested", nested), slog.Any("pointer", &secret))

				return buf.String(), nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, gotErr := tt.got()
			require.NoError(t, gotErr)
			assert.NotContains(t, got, canary)

			switch {
			case tt.wantInLog != "":
				assert.Contains(t, got, tt.wantInLog)
			case tt.wantJSON:
				assert.JSONEq(t, tt.want, got)
			default:
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// TestSecret_AsMapKeyCollides pins a deliberate behaviour: a Secret is not meant to be a map key. As a key it
// encodes to the redaction marker, so several secret keys give several members of the same name (and a
// decoder keeps one of them): the value is never emitted, whatever the cost in entries.
func TestSecret_AsMapKeyCollides(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		keys        []string
		wantMembers int // members of the JSON object, all named [REDACTED]
		wantDecoded int // entries left after decoding into a map
	}{
		{name: "one key", keys: []string{canary + "-1"}, wantMembers: 1, wantDecoded: 1},
		{name: "two keys", keys: []string{canary + "-1", canary + "-2"}, wantMembers: 2, wantDecoded: 1},
		{
			name:        "three keys",
			keys:        []string{canary + "-1", canary + "-2", canary + "-3"},
			wantMembers: 3,
			wantDecoded: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			byKey := map[connection.Secret]int{}
			for i, key := range tt.keys {
				byKey[connection.NewSecret(key)] = i
			}

			encoded, err := json.Marshal(byKey)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), canary)
			assert.Equal(t, tt.wantMembers, strings.Count(string(encoded), `"`+connection.RedactedValue+`":`))

			var decoded map[string]int

			require.NoError(t, json.Unmarshal(encoded, &decoded))
			assert.Len(t, decoded, tt.wantDecoded)
		})
	}
}

func marshalString(value any) (string, error) {
	out, err := json.Marshal(value)

	return string(out), err
}

func textString(secret connection.Secret) (string, error) {
	out, err := secret.MarshalText()

	return string(out), err
}
