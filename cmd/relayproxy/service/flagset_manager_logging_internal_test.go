package service

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmdhelpers/cmdlog"
	"github.com/blackbirdworks/semaphore/cmd/cli/evaluate/retrieverconf"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

// An invalid configuration reload is logged at debug level together with the configuration:
// neither the log line nor the error may contain a secret.
func Test_flagsetManagerImpl_OnConfigChange_invalidConfigDoesNotLogSecrets(t *testing.T) {
	t.Parallel()

	const (
		sharedAPIKey = "SECRETMARKER-shared-api-key"
		token        = "SECRETMARKER-github-token"
		webhook      = "https://hooks.slack.example/SECRETMARKER-webhook"
		mongoURI     = "mongodb://user:SECRETMARKER-mongo@host/db"
	)

	retriever := &retrieverconf.RetrieverConf{
		Kind:      fixtures.KindFile,
		Path:      "flags.yaml",
		AuthToken: token,
		URI:       mongoURI,
	}
	flagSet := func(name string) config.FlagSet {
		return config.FlagSet{
			Name:      name,
			APIKeys:   []string{sharedAPIKey},
			Retriever: retriever,
			Notifiers: []config.NotifierConf{{Kind: "slack", SlackWebhookURL: webhook, Secret: "SECRETMARKER-n"}},
		}
	}
	invalid := &config.Config{FlagSets: []config.FlagSet{flagSet("one"), flagSet("two")}}
	require.Error(t, invalid.IsValid())

	var buf bytes.Buffer

	logger, err := cmdlog.New("json", "debug", cmdlog.WithWriter(&buf))
	require.NoError(t, err)

	mgr := &FlagsetManagerImpl{logger: logger.Slog}

	mgr.OnConfigChange(invalid)

	out := buf.String()
	assert.Contains(t, out, "the new configuration is invalid")
	assert.Contains(t, out, "invalid configuration:")
	assert.Contains(t, out, "one and two", "the flagset names identify the duplicate")
	assert.NotContains(t, out, "SECRETMARKER")
}
