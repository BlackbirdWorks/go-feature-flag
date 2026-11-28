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

const canaryStorePassword = "CANARY-store-password-3a7d"

// The password of a segment store never reaches a print, an encoding, a format verb, a log line or an error: the file
// holds a reference (fromEnv or fromFile), a resolved value is a secret that prints as redacted, and an inline value
// is refused unless the development switch allows it, and then still prints as redacted.
func TestCanaryStorePasswordNeverLeaks(t *testing.T) {
	t.Parallel()

	const doc = "version: 2\nconnections:\n" +
		"  cp: {type: file, path: /x}\n" +
		"  audiences: {type: valkey, addresses: [\"v:6379\"], username: relay, password: " + canaryStorePassword + "}\n" +
		"  cache: {type: redis, addresses: [\"r:6379\"], password: " + canaryStorePassword + "}\n" +
		"controlPlane:\n  connection: cp\n  manifest: {key: m.yaml}\n"

	_, strictErr := bootstrap.Parse([]byte(doc), nil, options(nil, nil, false))
	require.ErrorIs(t, strictErr, bootstrap.ErrInlineSecret)

	cfg, err := bootstrap.Parse([]byte(doc), nil, options(nil, nil, true))
	require.NoError(t, err)

	var printed bytes.Buffer
	require.NoError(t, cfg.Print(&printed))

	asYAML, err := yaml.Marshal(cfg)
	require.NoError(t, err)

	asJSON, err := json.Marshal(cfg)
	require.NoError(t, err)

	var logs bytes.Buffer

	slog.New(slog.NewJSONHandler(&logs, nil)).Info("config", slog.Any("config", cfg), slog.Any("error", strictErr))

	// the references of the file resolve to a value that is a secret too
	refDoc := "version: 2\nconnections:\n  cp: {type: file, path: /x}\n" +
		"  audiences: {type: valkey, addresses: [\"v:6379\"], password: {fromEnv: STORE_PASSWORD}}\n" +
		"controlPlane:\n  connection: cp\n  manifest: {key: m.yaml}\n"

	refCfg, err := bootstrap.Parse(
		[]byte(refDoc),
		nil,
		options(envMap{"STORE_PASSWORD": canaryStorePassword}, nil, false),
	)
	require.NoError(t, err)

	var refPrinted bytes.Buffer
	require.NoError(t, refCfg.Print(&refPrinted))

	secret, err := refCfg.Connections["audiences"].Valkey.Password.Resolve(options(
		envMap{"STORE_PASSWORD": canaryStorePassword}, nil, false))
	require.NoError(t, err)

	outputs := map[string]string{
		"inline secret error": strictErr.Error(),
		"effective print":     printed.String(),
		"reference print":     refPrinted.String(),
		"yaml":                string(asYAML),
		"encoded as JSON":     string(asJSON),
		"%v":                  fmt.Sprintf("%v", cfg),
		"%+v":                 fmt.Sprintf(fmtPlus, cfg),
		"%#v":                 fmt.Sprintf(fmtGo, cfg),
		"resolved %v":         fmt.Sprintf("%v %+v %s %#v", secret, secret, secret, secret),
		"log line":            logs.String(),
	}

	for name, out := range outputs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.NotContains(t, out, canaryStorePassword)
		})
	}

	assert.Contains(t, printed.String(), bootstrap.RedactedValue)
	assert.Equal(t, canaryStorePassword, secret.Reveal(), "only Reveal gives the value, to the dialer")
}
