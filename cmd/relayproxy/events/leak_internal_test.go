package events

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// syncBuffer is a log sink that several goroutines can write.
type syncBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.buf.String()
}

// jsonQuoted returns the JSON string that encodes value.
func jsonQuoted(value string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}

	return string(raw)
}

// TestErrorsNeverCarrySecretsInAnyForm takes the definition the builder makes of a webhook exporter (so the
// secrets it scrubs are the ones the builder resolved), puts an exporter that echoes each secret in one encoded
// form in its place, and runs it under the real engine manager: neither the log, nor the status, nor the report
// shows a secret.
func TestErrorsNeverCarrySecretsInAnyForm(t *testing.T) {
	t.Parallel()

	const (
		endpoint = "https://hook.example/CANARY-ENDPOINT-9f3a"
		signing  = "CANARY-SIGNING-77c1/with+chars"
		header   = "CANARY-HEADER-5b2e"
	)

	forms := map[string]func(string) string{
		"raw":                 func(s string) string { return s },
		"query escaped":       url.QueryEscape,
		"path escaped":        url.PathEscape,
		"json escaped":        jsonQuoted,
		"base64":              func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) },
		"base64 unpadded":     func(s string) string { return base64.RawStdEncoding.EncodeToString([]byte(s)) },
		"base64 url":          func(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) },
		"base64 url unpadded": func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) },
	}

	opts := bootstrap.LoadOptions{LookupEnv: func(name string) (string, bool) {
		value, ok := map[string]string{"URL": endpoint, "SECRET": signing, "HDR": header}[name]

		return value, ok
	}}

	cfg, err := bootstrap.Parse([]byte(`version: 2
exporters:
  hook: {kind: webhook, endpointUrl: {fromEnv: URL}, secret: {fromEnv: SECRET}, headers: {X-Token: {fromEnv: HDR}}}
`), nil, opts)
	require.NoError(t, err)

	built, err := Builder{Secrets: opts}.Exporters(context.Background(), SpecsFrom(cfg))
	require.NoError(t, err)

	for name, encode := range forms {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// the scrubbing wrapper of the builder (its secrets), around an exporter that echoes
			echo := built[0]
			echoCounter := &countingExporter{
				inner: echoExporter{
					err: fmt.Errorf("%w: %s %s %s", errRemote, encode(endpoint), encode(signing), encode(header)),
				},
				instance: &instanceStats{},
				secrets:  built[0].wrapper().secrets,
			}
			echo.Config.Exporter = echoCounter

			logs := &syncBuffer{}
			disp := NewDispatcher(&DispatcherConfig{Logger: slog.New(slog.NewTextHandler(logs, nil))})
			require.NoError(t, disp.Start(context.Background()))

			_, applyErr := disp.Apply(context.Background(), []ExporterDef{echo})
			require.NoError(t, applyErr)
			require.True(t, disp.Collect(newEvent()))

			report, shutdownErr := disp.Shutdown(context.Background())
			require.NoError(t, shutdownErr)

			status := disp.Status()
			statusJSON, marshalErr := json.Marshal(status)
			require.NoError(t, marshalErr)

			everything := logs.String() + string(statusJSON) + fmt.Sprintf("%+v %#v %v", status, status, report)

			for _, canary := range []string{"CANARY-ENDPOINT", "CANARY-SIGNING", "CANARY-HEADER"} {
				assert.NotContains(t, everything, canary)
			}

			assert.Contains(
				t,
				logs.String(),
				bootstrap.RedactedValue,
				"the failure was logged, with the secrets hidden",
			)
			assert.Equal(t, ErrorOther, status.Exporters[0].LastError)
			assert.Contains(t, logs.String(), "level=ERROR")
		})
	}
}

// TestKafkaPasswordAndBasicPairAreScrubbed checks that the password of the SASL settings, and the user and
// password pair a Basic header would carry (in both base64 alphabets), are among the forms the wrapper scrubs.
func TestKafkaPasswordAndBasicPairAreScrubbed(t *testing.T) {
	t.Parallel()

	const (
		password = "kafka-password-123"
		user     = "svc-user"
	)

	opts := bootstrap.LoadOptions{LookupEnv: func(name string) (string, bool) {
		if name == "PASS" {
			return password, true
		}

		return "", false
	}}

	cfg, err := bootstrap.Parse([]byte(`version: 2
exporters:
  k:
    kind: kafka
    kafka:
      topic: t
      addresses: ["h:9092"]
      net: {sasl: {enable: true, user: svc-user, password: {fromEnv: PASS}}}
`), nil, opts)
	require.NoError(t, err)

	built, err := Builder{Secrets: opts}.Exporters(context.Background(), SpecsFrom(cfg))
	require.NoError(t, err)

	forms := built[0].wrapper().secrets

	for _, want := range []string{
		password,
		user + ":" + password,
		base64.StdEncoding.EncodeToString([]byte(user + ":" + password)),
		base64.RawURLEncoding.EncodeToString([]byte(user + ":" + password)),
	} {
		assert.Contains(t, forms, want)
	}
}
