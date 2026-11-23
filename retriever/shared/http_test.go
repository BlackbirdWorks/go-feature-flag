package shared_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thomaspoignant/go-feature-flag/retriever/shared"
)

type failingClient struct{}

func (failingClient) Do(req *http.Request) (*http.Response, error) {
	return nil, &url.Error{Op: "Get", URL: req.URL.String(), Err: errors.New("connection refused")}
}

func TestCallHTTPAPI_ErrorsDoNotLeakURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		url    string
		client failingClient
		method string
	}{
		{name: "transport error", url: "https://user:SECRETMARKER@flags.example.com/f?token=SECRETMARKER", method: http.MethodGet},
		{name: "invalid request", url: "https://user:SECRETMARKER@flags.example.com/f?token=SECRETMARKER", method: "BAD METHOD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := shared.CallHTTPAPI(context.Background(), tt.url, tt.method, "", 0, nil, tt.client)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "SECRETMARKER")
		})
	}
}
