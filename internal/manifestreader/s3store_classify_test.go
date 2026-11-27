package manifestreader_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

func attempts3(step httpStep) []httpStep {
	out := make([]httpStep, 3)
	for i := range out {
		out[i] = step
	}

	return out
}

// TestS3Store_ClassificationOfFailures is the table the conformance suite cannot run on the emulator (it does
// not throttle): the classification of each failed answer, with the throttle flag the reader backs off on.
func TestS3Store_ClassificationOfFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		wantReason    string
		steps         []httpStep
		wantRetryable bool
		wantThrottled bool
	}{
		{
			name: "429 is throttled and retryable", wantReason: manifestreader.ReasonError,
			steps:         attempts3(httpStep{status: http.StatusTooManyRequests, body: slowDownXML}),
			wantRetryable: true, wantThrottled: true,
		},
		{
			name: "403 with the code SlowDown is throttled, not denied", wantReason: manifestreader.ReasonError,
			steps:         attempts3(httpStep{status: http.StatusForbidden, body: slowDownXML}),
			wantRetryable: true, wantThrottled: true,
		},
		{
			name: "503 SlowDown is throttled", wantReason: manifestreader.ReasonError,
			steps:         attempts3(httpStep{status: http.StatusServiceUnavailable, body: slowDownXML}),
			wantRetryable: true, wantThrottled: true,
		},
		{
			name: "500 InternalError is retryable and not throttled", wantReason: manifestreader.ReasonError,
			steps:         attempts3(httpStep{status: http.StatusInternalServerError, body: internalXML}),
			wantRetryable: true,
		},
		{
			name: "403 AccessDenied is denied", wantReason: manifestreader.ReasonDenied,
			steps: []httpStep{{status: http.StatusForbidden, body: deniedXML}},
		},
		{
			name: "404 NoSuchBucket is a configuration problem", wantReason: manifestreader.ReasonDenied,
			steps: []httpStep{{status: http.StatusNotFound, body: noSuchBucketXML}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := &scriptedTransport{steps: tt.steps}
			fac := connection.New(connection.WithRoundTripper(transport))
			require.NoError(t, fac.Set(s3Conn, s3Spec()))

			obj, err := manifestreader.NewS3Store(fac, s3Conn, s3Bucket).
				Get(t.Context(), manifestreader.GetRequest{Key: "k", Limit: 10})
			assert.Equal(t, manifestreader.Object{}, obj)

			var fetchErr *manifestreader.FetchError

			require.ErrorAs(t, err, &fetchErr)
			assert.Equal(t, tt.wantReason, fetchErr.Reason)
			assert.Equal(t, tt.wantRetryable, fetchErr.Retryable)
			assert.Equal(t, tt.wantThrottled, fetchErr.Throttled)
		})
	}
}

// chunkedTransport answers 200 with a body of unknown length (no Content-Length, as a chunked answer).
type chunkedTransport struct{ body string }

func (c chunkedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Etag": {s3ETag}},
		Body: io.NopCloser(strings.NewReader(c.body)), ContentLength: -1, Request: req,
	}, nil
}

// TestS3Store_LimitsWithoutAndWithContentLength: the limit holds for a body of unknown length, and a limit that
// is not positive admits the empty object only.
func TestS3Store_LimitsWithoutAndWithContentLength(t *testing.T) {
	t.Parallel()

	tests := []struct {
		transport http.RoundTripper
		wantErr   error
		name      string
		wantBody  string
		limit     int64
	}{
		{
			name:      "no Content-Length, at the limit",
			transport: chunkedTransport{helloBody},
			limit:     5,
			wantBody:  helloBody,
		},
		{
			name:      "no Content-Length, one over",
			transport: chunkedTransport{helloBody},
			limit:     4,
			wantErr:   manifestreader.ErrTooLarge,
		},
		{
			name:      "no Content-Length, far over",
			transport: chunkedTransport{strings.Repeat("x", 1000)},
			limit:     3,
			wantErr:   manifestreader.ErrTooLarge,
		},
		{name: "no Content-Length, empty at zero", transport: chunkedTransport{""}, limit: 0},
		{
			name:      "no Content-Length, one byte at zero",
			transport: chunkedTransport{"x"},
			limit:     0,
			wantErr:   manifestreader.ErrTooLarge,
		},
		{name: "no Content-Length, empty at a negative limit", transport: chunkedTransport{""}, limit: -1},
		{
			name:      "no Content-Length, one byte at a negative limit",
			transport: chunkedTransport{"x"},
			limit:     -5,
			wantErr:   manifestreader.ErrTooLarge,
		},
		{
			name:      "Content-Length 0, empty at a negative limit",
			transport: &scriptedTransport{steps: []httpStep{okAnswer("")}}, limit: -1,
		},
		{
			name: "Content-Length 1, one byte at a negative limit",
			transport: &scriptedTransport{
				steps: []httpStep{okAnswer("x")},
			},
			limit:   -1,
			wantErr: manifestreader.ErrTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fac := connection.New(connection.WithRoundTripper(tt.transport))
			require.NoError(t, fac.Set(s3Conn, s3Spec()))

			obj, err := manifestreader.NewS3Store(fac, s3Conn, s3Bucket).
				Get(t.Context(), manifestreader.GetRequest{Key: "k", Limit: tt.limit})

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Equal(t, manifestreader.Object{}, obj)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantBody, string(obj.Bytes))
			assert.Equal(t, s3ETag, obj.ETag)
			assert.Equal(t, len(obj.Bytes), cap(obj.Bytes), "a buffer of exactly the size of the body")
			assert.LessOrEqual(t, int64(cap(obj.Bytes)), max(tt.limit, 0), "nothing retained beyond the limit")
		})
	}
}
