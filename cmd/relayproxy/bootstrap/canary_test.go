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
