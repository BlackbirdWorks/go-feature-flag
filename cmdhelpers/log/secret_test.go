package log_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmdhelpers/log"
)

const secretValue = "SECRETMARKER-hunter2"

// settings is a configuration-like struct holding secrets, as the relay proxy configuration does.
type settings struct {
	Name    string     `json:"name"   yaml:"name"`
	APIKey  log.Secret `json:"apiKey" yaml:"apiKey"`
	Backend struct {
		URI log.Secret `json:"uri" yaml:"uri"`
	} `json:"backend" yaml:"backend"`
}

func newSettings() settings {
	s := settings{Name: "visible-name", APIKey: secretValue}
	s.Backend.URI = "mongodb://user:" + secretValue + "@host/db"

	return s
}

func TestSecretIsNeverPrinted(t *testing.T) {
	t.Parallel()

	sample := newSettings()

	tests := []struct {
		got  func(t *testing.T) string
		name string
	}{
		{name: "Sprint", got: func(*testing.T) string { return fmt.Sprint(sample.APIKey) }},
		{name: "%v of the struct", got: func(*testing.T) string { return fmt.Sprintf("%v", sample) }},
		{name: "%+v of the struct", got: func(*testing.T) string { return fmt.Sprintf("%+v", sample) }},
		{name: "%#v of the struct", got: func(*testing.T) string { return fmt.Sprintf("%#v", sample) }},
		{name: "json", got: func(t *testing.T) string {
			t.Helper()

			b, err := json.Marshal(sample)
			require.NoError(t, err)

			return string(b)
		}},
		{name: "yaml", got: func(t *testing.T) string {
			t.Helper()

			b, err := yaml.Marshal(sample)
			require.NoError(t, err)

			return string(b)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.NotContains(t, tt.got(t), "SECRETMARKER")
		})
	}
}

func TestSecretInTheLogs(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"json", "logfmt"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			logger, err := log.New(format, "debug", log.WithWriter(&out))
			require.NoError(t, err)

			// the three ways a secret reaches the logs: a secret attribute, a secret field of a struct, an
			// error built from a secret.
			logger.Slog.Debug("config",
				log.SecretAttr("token", secretValue),
				slog.Any("settings", newSettings()),
				slog.Any("secret", log.Secret(secretValue)),
			)

			assert.NotContains(t, out.String(), "SECRETMARKER")
			assert.Contains(t, out.String(), log.RedactedValue)
			assert.Contains(t, out.String(), "visible-name")
		})
	}
}
