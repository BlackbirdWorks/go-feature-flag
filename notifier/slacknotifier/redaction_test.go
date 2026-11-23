package slacknotifier

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thomaspoignant/go-feature-flag/notifier"
)

type failingClient struct{}

func (failingClient) Do(req *http.Request) (*http.Response, error) {
	return nil, &url.Error{Op: "Post", URL: req.URL.String(), Err: errors.New("connection refused")}
}

// The Slack webhook URL is the secret of the integration: it must not end up in the errors that are logged.
func TestNotifier_errorsDoNotContainTheWebhookURL(t *testing.T) {
	t.Parallel()
	c := Notifier{
		SlackWebhookURL: "https://hooks.slack.com/services/T0/B0/SECRETMARKER-token",
		httpClient:      failingClient{},
	}
	err := c.Notify(notifier.DiffCache{})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRETMARKER")
	assert.Contains(t, err.Error(), "connection refused")
}
