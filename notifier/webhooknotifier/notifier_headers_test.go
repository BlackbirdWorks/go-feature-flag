package webhooknotifier

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/signer"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/notifier"
)

type headerTestHTTPClient func(*http.Request) (*http.Response, error)

func (f headerTestHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestWebhookNotifierConcurrentSignatures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		headers map[string][]string
		// wantAuthorization is the Authorization header of both requests
		wantAuthorization string
	}{
		{name: "nil headers", wantAuthorization: ""},
		{
			name:              "custom headers",
			headers:           map[string][]string{"Authorization": {"Bearer test-token"}},
			wantAuthorization: "Bearer test-token",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			requests := make(chan *http.Request, 2)
			results := make(chan error, 2)
			release := make(chan struct{})
			client := headerTestHTTPClient(func(req *http.Request) (*http.Response, error) {
				requests <- req
				select {
				case <-release:
					return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
			n := Notifier{
				EndpointURL: "https://example.com/hook",
				Secret:      "test-secret",
				Headers:     tt.headers,
				httpClient:  client,
			}

			// Keep both requests in flight; the second notification must not change the first request.
			captured := make([]*http.Request, 0, 2)
			for _, key := range []string{"first-flag", "second-flag"} {
				go func() {
					results <- n.Notify(notifier.DiffCache{Added: map[string]flag.Flag{key: &flag.InternalFlag{}}})
				}()
				select {
				case req := <-requests:
					captured = append(captured, req)
				case err := <-results:
					require.Fail(t, "notification returned before sending a request", "err: %v", err)
				case <-ctx.Done():
					require.Fail(t, "context done before the notification", ctx.Err().Error())
				}
			}
			for _, req := range captured {
				body, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				assert.Equal(t, signer.Sign(body, []byte(n.Secret)), req.Header.Get("X-Hub-Signature-256"))
				assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
				assert.Equal(t, tt.wantAuthorization, req.Header.Get("Authorization"))
			}
			close(release)
			for range captured {
				select {
				case err := <-results:
					require.NoError(t, err)
				case <-ctx.Done():
					require.Fail(t, "context done before the notification", ctx.Err().Error())
				}
			}
		})
	}
}

func TestWebhookNotifierPreservesConfiguredHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		headers map[string][]string
		secret  string
		// wantHeaders is the configuration of the headers after the notification: it is left untouched
		wantHeaders http.Header
	}{
		{name: "nil headers", secret: "test-secret", wantHeaders: nil},
		{
			name: "signed request", secret: "test-secret",
			headers: map[string][]string{
				"X-Custom": {"first", "second"}, "Content-Type": {"text/plain"},
				"X-Hub-Signature-256": {"configured-signature"},
			},
			wantHeaders: http.Header{
				"X-Custom": {"first", "second"}, "Content-Type": {"text/plain"},
				"X-Hub-Signature-256": {"configured-signature"},
			},
		},
		{
			name:        "unsigned request",
			headers:     map[string][]string{"X-Custom": {"first", "second"}},
			wantHeaders: http.Header{"X-Custom": {"first", "second"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			original := http.Header(tt.headers).Clone()
			n := Notifier{
				EndpointURL: "https://example.com/hook",
				Secret:      tt.secret,
				Headers:     tt.headers,
				httpClient: headerTestHTTPClient(func(req *http.Request) (*http.Response, error) {
					assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
					assert.Equal(t, original.Values("X-Custom"), req.Header.Values("X-Custom"))
					if tt.secret == "" {
						assert.Empty(t, req.Header.Get("X-Hub-Signature-256"))
					} else {
						body, err := io.ReadAll(req.Body)
						require.NoError(t, err)
						assert.Equal(t, signer.Sign(body, []byte(tt.secret)), req.Header.Get("X-Hub-Signature-256"))
					}
					// A client changing a request header must not mutate the configuration's value slice.
					if values := req.Header.Values("X-Custom"); len(values) > 0 {
						values[0] = "changed by client"
					}

					return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
				}),
			}
			require.NoError(t, n.Notify(notifier.DiffCache{}))
			assert.Equal(t, tt.wantHeaders, http.Header(n.Headers))
			assert.Equal(t, tt.wantHeaders, http.Header(tt.headers))
		})
	}
}
