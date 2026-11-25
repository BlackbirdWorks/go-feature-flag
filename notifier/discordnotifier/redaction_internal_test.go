package discordnotifier

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/notifier"
)

var (
	errConnectionRefused = errors.New("connection refused")
)

type failingClient struct{}

func (failingClient) Do(req *http.Request) (*http.Response, error) {
	return nil, &url.Error{Op: "Post", URL: req.URL.String(), Err: errConnectionRefused}
}

// The Discord webhook URL embeds the webhook token: it must not end up in the errors that are logged.
func TestNotifier_errorsDoNotContainTheWebhookURL(t *testing.T) {
	t.Parallel()

	c := Notifier{
		DiscordWebhookURL: "https://discord.com/api/webhooks/1/SECRETMARKER-token",
		httpClient:        failingClient{},
	}
	err := c.Notify(notifier.DiffCache{})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRETMARKER")
	assert.Contains(t, err.Error(), "connection refused")
}
