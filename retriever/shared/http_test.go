package shared_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/retriever/shared"
)

var (
	errConnectionRefused       = errors.New("connection refused")
	errStoppedAfter10Redirects = errors.New("stopped after 10 redirects")
)

type failingClient struct{}

func (failingClient) Do(req *http.Request) (*http.Response, error) {
	return nil, &url.Error{Op: "Get", URL: req.URL.String(), Err: errConnectionRefused}
}

func TestCallHTTPAPI_ErrorsDoNotLeakURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		url     string
		client  failingClient
		method  string
		wantErr string
	}{
		{
			name:    "transport error",
			url:     "https://user:SECRETMARKER@flags.example.com/f?token=SECRETMARKER",
			method:  http.MethodGet,
			wantErr: `Get "[REDACTED]": connection refused`,
		},
		{
			name:    "unparsable url (invalid port)",
			url:     "https://user:SECRETMARKER",
			method:  http.MethodGet,
			wantErr: `parse "[REDACTED]": invalid URL`,
		},
		{
			name:    "unparsable url (invalid escape)",
			url:     "https://u:p@h/%zzSECRETMARKER",
			method:  http.MethodGet,
			wantErr: `parse "[REDACTED]": invalid URL`,
		},
		{
			name:    "unparsable url (control character)",
			url:     "https://h/\x7fSECRETMARKER",
			method:  http.MethodGet,
			wantErr: `parse "[REDACTED]": invalid URL`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := shared.CallHTTPAPI(context.Background(), tt.url, tt.method, "", 0, nil, tt.client)
			require.EqualError(t, err, tt.wantErr)
			assert.NotContains(t, err.Error(), "SECRETMARKER")
		})
	}
}

type redirectRejectingClient struct{ resp *http.Response }

func (c redirectRejectingClient) Do(req *http.Request) (*http.Response, error) {
	return c.resp, &url.Error{Op: "Get", URL: req.URL.String(), Err: errStoppedAfter10Redirects}
}

func TestCallHTTPAPI_KeepsResponseOnClientError(t *testing.T) {
	t.Parallel()
	want := &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"/next"}}}
	resp, err := shared.CallHTTPAPI(context.Background(),
		"https://user:SECRETMARKER@flags.example.com/f", "", "", 0, nil, redirectRejectingClient{resp: want})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRETMARKER")
	require.Same(t, want, resp)
}
