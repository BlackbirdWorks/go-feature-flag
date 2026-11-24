package webhooknotifier

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

// The endpoint URL may carry credentials: it must not end up in the errors that are logged.
func TestNotifier_errorsDoNotContainTheWebhookURL(t *testing.T) {
	t.Parallel()
	c := Notifier{
		EndpointURL: "https://user:SECRETMARKER-pass@hooks.example.com/hook?token=SECRETMARKER-q",
		httpClient:  failingClient{},
	}
	err := c.Notify(notifier.DiffCache{})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRETMARKER")
	assert.Contains(t, err.Error(), "connection refused")
}
