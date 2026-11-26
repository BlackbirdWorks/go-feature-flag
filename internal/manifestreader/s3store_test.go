package manifestreader_test

import (
	"cmp"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

const (
	s3Conn   = "control"
	s3Bucket = "control-bucket"
	s3Prefix = "cp/"
	s3ETag   = `"9a0364b9e99bb480dd25e1f0284c8555"`
)

const (
	xmlHead         = `<?xml version="1.0" encoding="UTF-8"?>`
	noSuchKeyXML    = xmlHead + `<Error><Code>NoSuchKey</Code><Message>nope</Message></Error>`
	noSuchBucketXML = xmlHead + `<Error><Code>NoSuchBucket</Code><Message>nope</Message></Error>`
	deniedXML       = xmlHead + `<Error><Code>AccessDenied</Code><Message>no</Message></Error>`
	internalXML     = xmlHead + `<Error><Code>InternalError</Code><Message>x</Message></Error>`
	slowDownXML     = xmlHead + `<Error><Code>SlowDown</Code><Message>x</Message></Error>`
)

// httpStep is one scripted answer of the transport.
type httpStep struct {
	err    error
	header http.Header
	body   string
	// contentLength overrides the Content-Length header (which is the body length otherwise).
	contentLength string
	status        int
}

// scriptedTransport answers in order and records the method, path and conditional header of every request.
type scriptedTransport struct {
	steps []httpStep
	seen  []seenRequest
	mu    sync.Mutex
}

type seenRequest struct {
	Method      string
	Path        string
	IfNoneMatch string
	RawQuery    string
}

func (s *scriptedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.seen = append(s.seen, seenRequest{
		Method:      req.Method,
		Path:        req.URL.Path,
		IfNoneMatch: req.Header.Get("If-None-Match"),
		RawQuery:    req.URL.RawQuery,
	})

	idx := len(s.seen) - 1
	if idx >= len(s.steps) {
		return nil, io.ErrUnexpectedEOF
	}

	cur := s.steps[idx]
	if cur.err != nil {
		return nil, cur.err
	}

	header := cur.header.Clone()
	if header == nil {
		header = http.Header{}
	}

	header.Set("Content-Length", cmp.Or(cur.contentLength, strconv.Itoa(len(cur.body))))

	return &http.Response{
		StatusCode: cur.status, Status: strconv.Itoa(cur.status) + " " + http.StatusText(cur.status), Header: header,
		Body: io.NopCloser(strings.NewReader(cur.body)), Request: req,
	}, nil
}

func (s *scriptedTransport) requests() []seenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]seenRequest(nil), s.seen...)
}

func s3Spec() *connection.S3Spec {
	return &connection.S3Spec{
		Endpoint: "http://s3.test", Region: "us-east-1", Bucket: s3Bucket, PathStyle: true,
		Credentials: connection.Credentials{
			Type: connection.CredentialsStatic, AccessKeyID: connection.NewSecret("AKIDTEST"),
			SecretAccessKey: connection.NewSecret("secret-test"),
		},
		Retry: connection.Retry{MaxAttempts: 3, MaxBackoff: time.Millisecond},
	}
}

func okAnswer(body string) httpStep {
	return httpStep{status: http.StatusOK, body: body, header: http.Header{"Etag": {s3ETag}}}
}

func TestS3Store_Get(t *testing.T) {
	t.Parallel()

	wantPath := "/" + s3Bucket + "/" + s3Prefix + "manifest.yaml" // the key is fetched verbatim

	tests := []struct {
		wantErrIs    error
		wantFetchErr *manifestreader.FetchError
		name         string
		etag         string
		wantObject   manifestreader.Object
		steps        []httpStep
		wantRequests []seenRequest
		limit        int64
	}{
		{
			name:         "200 returns the bytes and the ETag, unconditional without an ETag",
			steps:        []httpStep{okAnswer("hello")},
			limit:        100,
			wantObject:   manifestreader.Object{Bytes: []byte("hello"), ETag: s3ETag},
			wantRequests: []seenRequest{{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet}},
		},
		{
			name: "200 exactly at the limit", steps: []httpStep{okAnswer("hello")}, limit: 5,
			wantObject:   manifestreader.Object{Bytes: []byte("hello"), ETag: s3ETag},
			wantRequests: []seenRequest{{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet}},
		},
		{
			name:      "304 is not modified, the request carries If-None-Match",
			steps:     []httpStep{{status: http.StatusNotModified}},
			etag:      s3ETag,
			limit:     100,
			wantErrIs: manifestreader.ErrNotModified,
			wantRequests: []seenRequest{
				{Method: http.MethodGet, Path: wantPath, IfNoneMatch: s3ETag, RawQuery: rawGet},
			},
		},
		{
			name:         "404 NoSuchKey is not found and not retried",
			steps:        []httpStep{{status: http.StatusNotFound, body: noSuchKeyXML}},
			limit:        100,
			wantErrIs:    manifestreader.ErrNotFound,
			wantRequests: []seenRequest{{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet}},
		},
		{
			name: "404 without a body is not found", steps: []httpStep{{status: http.StatusNotFound}},
			limit: 100, wantErrIs: manifestreader.ErrNotFound,
			wantRequests: []seenRequest{{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet}},
		},
		{
			name:  "404 NoSuchBucket is a configuration problem, not a missing object",
			steps: []httpStep{{status: http.StatusNotFound, body: noSuchBucketXML}}, limit: 100,
			wantFetchErr: &manifestreader.FetchError{Reason: manifestreader.ReasonDenied},
			wantRequests: []seenRequest{{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet}},
		},
		{
			name:         "403 is denied, not retryable, not retried",
			steps:        []httpStep{{status: http.StatusForbidden, body: deniedXML}},
			limit:        100,
			wantFetchErr: &manifestreader.FetchError{Reason: manifestreader.ReasonDenied},
			wantRequests: []seenRequest{{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet}},
		},
		{
			name: "500 then 200: the SDK retries once",
			steps: []httpStep{
				{status: http.StatusInternalServerError, body: internalXML},
				okAnswer("hello"),
			},
			limit:      100,
			wantObject: manifestreader.Object{Bytes: []byte("hello"), ETag: s3ETag},
			wantRequests: []seenRequest{
				{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet},
				{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet},
			},
		},
		{
			name: "500 on every attempt is a retryable failure",
			steps: []httpStep{
				{
					status: http.StatusInternalServerError,
					body:   internalXML,
				},
				{status: http.StatusInternalServerError, body: internalXML},
				{status: http.StatusInternalServerError, body: internalXML},
			}, limit: 100,
			wantFetchErr: &manifestreader.FetchError{Reason: manifestreader.ReasonError, Retryable: true},
			wantRequests: []seenRequest{
				{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet},
				{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet},
				{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet},
			},
		},
		{
			name: "503 SlowDown on every attempt is retryable",
			steps: []httpStep{
				{
					status: http.StatusServiceUnavailable,
					body:   slowDownXML,
				},
				{status: http.StatusServiceUnavailable, body: slowDownXML},
				{status: http.StatusServiceUnavailable, body: slowDownXML},
			}, limit: 100,
			wantFetchErr: &manifestreader.FetchError{Reason: manifestreader.ReasonError, Retryable: true},
			wantRequests: []seenRequest{
				{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet},
				{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet},
				{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet},
			},
		},
		{
			name: "a throttle code on a 400 is retried and retryable",
			steps: []httpStep{
				{status: http.StatusBadRequest, body: slowDownXML}, {status: http.StatusBadRequest, body: slowDownXML},
				{status: http.StatusBadRequest, body: slowDownXML},
			}, limit: 100,
			wantFetchErr: &manifestreader.FetchError{Reason: manifestreader.ReasonError, Retryable: true},
			wantRequests: []seenRequest{
				{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet},
				{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet},
				{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet},
			},
		},
		{
			name: "a body over the limit is rejected", steps: []httpStep{okAnswer("hello world")}, limit: 5,
			wantErrIs:    manifestreader.ErrTooLarge,
			wantRequests: []seenRequest{{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet}},
		},
		{
			name: "a zero limit accepts an empty body only", steps: []httpStep{okAnswer("")}, limit: 0,
			wantObject:   manifestreader.Object{Bytes: []byte{}, ETag: s3ETag},
			wantRequests: []seenRequest{{Method: http.MethodGet, Path: wantPath, RawQuery: rawGet}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Helper()

			t.Parallel()

			transport := &scriptedTransport{steps: tt.steps}
			fac := connection.New(connection.WithRoundTripper(transport))
			require.NoError(t, fac.Set(s3Conn, s3Spec()))

			store := manifestreader.NewS3Store(fac, s3Conn, s3Bucket)

			got, err := store.Get(
				t.Context(),
				manifestreader.GetRequest{Key: s3Prefix + "manifest.yaml", ETag: tt.etag, Limit: tt.limit},
			)

			switch {
			case tt.wantErrIs != nil:
				require.ErrorIs(t, err, tt.wantErrIs)
				assert.Equal(t, manifestreader.Object{}, got)
			case tt.wantFetchErr != nil:
				var fetchErr *manifestreader.FetchError

				require.ErrorAs(t, err, &fetchErr)
				assert.Equal(t, tt.wantFetchErr.Reason, fetchErr.Reason)
				assert.Equal(t, tt.wantFetchErr.Retryable, fetchErr.Retryable)
				require.NotErrorIs(t, err, manifestreader.ErrNotFound)
				require.NotErrorIs(t, err, manifestreader.ErrNotModified)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.wantObject, got)
			}

			// never a HEAD, never a LIST: every request is a plain GetObject of the key
			reqs := transport.requests()
			assert.Equal(t, tt.wantRequests, reqs)

			for _, req := range reqs {
				assert.Equal(t, http.MethodGet, req.Method)
				assert.Equal(t, wantPath, req.Path, "the key and nothing that lists a prefix")
				assert.NotContains(t, req.RawQuery, "list-type")
				assert.NotContains(t, req.RawQuery, "prefix")
			}
		})
	}
}

func TestS3Store_IfNoneMatchOnlyWithAnETag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		etag     string
		wantSent string
	}{
		{name: "no ETag, no header", etag: "", wantSent: ""},
		{name: "an ETag is sent as given", etag: s3ETag, wantSent: s3ETag},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Helper()

			t.Parallel()

			transport := &scriptedTransport{steps: []httpStep{okAnswer("x")}}
			fac := connection.New(connection.WithRoundTripper(transport))
			require.NoError(t, fac.Set(s3Conn, s3Spec()))

			_, err := manifestreader.NewS3Store(fac, s3Conn, s3Bucket).
				Get(t.Context(), manifestreader.GetRequest{Key: "k", ETag: tt.etag, Limit: 10})
			require.NoError(t, err)

			reqs := transport.requests()
			require.Len(t, reqs, 1)
			assert.Equal(t, tt.wantSent, reqs[0].IfNoneMatch)
			assert.Equal(t, "/"+s3Bucket+"/k", reqs[0].Path, "the store adds nothing to the key")
		})
	}
}

func TestS3Store_TransportErrorIsRetryable(t *testing.T) {
	t.Parallel()

	transport := &scriptedTransport{
		steps: []httpStep{{err: io.ErrUnexpectedEOF}, {err: io.ErrUnexpectedEOF}, {err: io.ErrUnexpectedEOF}},
	}
	fac := connection.New(connection.WithRoundTripper(transport))
	require.NoError(t, fac.Set(s3Conn, s3Spec()))

	_, err := manifestreader.NewS3Store(fac, s3Conn, s3Bucket).
		Get(t.Context(), manifestreader.GetRequest{Key: "k", Limit: 10})

	var fetchErr *manifestreader.FetchError

	require.ErrorAs(t, err, &fetchErr)
	assert.Equal(t, manifestreader.ReasonError, fetchErr.Reason)
	assert.True(t, fetchErr.Retryable)
}

func TestS3Store_UnknownConnectionIsNotRetryable(t *testing.T) {
	t.Parallel()

	fac := connection.New()
	_, err := manifestreader.NewS3Store(fac, "nope", s3Bucket).
		Get(t.Context(), manifestreader.GetRequest{Key: "k", Limit: 10})

	var fetchErr *manifestreader.FetchError

	require.ErrorAs(t, err, &fetchErr)
	assert.Equal(t, manifestreader.ReasonDenied, fetchErr.Reason)
	assert.False(t, fetchErr.Retryable)
	assert.ErrorIs(t, err, connection.ErrUnknownConnection)
}

func TestS3Store_CancelledContext(t *testing.T) {
	t.Parallel()

	fac := connection.New(connection.WithRoundTripper(&scriptedTransport{steps: []httpStep{okAnswer("x")}}))
	require.NoError(t, fac.Set(s3Conn, s3Spec()))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := manifestreader.NewS3Store(fac, s3Conn, s3Bucket).
		Get(ctx, manifestreader.GetRequest{Key: "k", Limit: 10})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestS3Store_VersionID(t *testing.T) {
	t.Parallel()

	const invalidArgumentXML = xmlHead + `<Error><Code>InvalidArgument</Code><Message>Invalid version id</Message></Error>`

	tests := []struct {
		want  error
		name  string
		wantQ string
		step  httpStep
	}{
		{name: "the version id is sent as the versionId query parameter", step: okAnswer("hi"), wantQ: queryVersion},
		{
			name:  "404 NoSuchKey for a version is not found",
			step:  httpStep{status: http.StatusNotFound, body: noSuchKeyXML},
			want:  manifestreader.ErrNotFound,
			wantQ: queryVersion,
		},
		{
			name:  "400 InvalidArgument for a version is also not found",
			step:  httpStep{status: http.StatusBadRequest, body: invalidArgumentXML},
			want:  manifestreader.ErrNotFound,
			wantQ: queryVersion,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := &scriptedTransport{steps: []httpStep{tt.step}}
			fac := connection.New(connection.WithRoundTripper(transport))
			require.NoError(t, fac.Set(s3Conn, s3Spec()))

			_, err := manifestreader.NewS3Store(fac, s3Conn, s3Bucket).
				Get(t.Context(), manifestreader.GetRequest{Key: "k", VersionID: "v-1", Limit: 10})

			if tt.want != nil {
				require.ErrorIs(t, err, tt.want)
			} else {
				require.NoError(t, err)
			}

			reqs := transport.requests()
			require.Len(t, reqs, 1)
			assert.Contains(t, reqs[0].RawQuery, tt.wantQ)
		})
	}
}

func TestS3Store_InvalidArgumentWithoutAVersionIsADenial(t *testing.T) {
	t.Parallel()

	const invalidArgumentXML = xmlHead + `<Error><Code>InvalidArgument</Code><Message>bad</Message></Error>`

	transport := &scriptedTransport{steps: []httpStep{{status: http.StatusBadRequest, body: invalidArgumentXML}}}
	fac := connection.New(connection.WithRoundTripper(transport))
	require.NoError(t, fac.Set(s3Conn, s3Spec()))

	_, err := manifestreader.NewS3Store(fac, s3Conn, s3Bucket).
		Get(t.Context(), manifestreader.GetRequest{Key: "k", Limit: 10})

	var fetchErr *manifestreader.FetchError

	require.ErrorAs(t, err, &fetchErr)
	assert.Equal(t, manifestreader.ReasonDenied, fetchErr.Reason)
}

func TestS3Store_ContentLengthSizesTheBuffer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErrIs error
		name      string
		wantBytes string
		step      httpStep
		limit     int64
		wantFetch bool
	}{
		{
			name:      "a Content-Length over the limit is refused before reading",
			step:      okAnswer("hello world"),
			limit:     5,
			wantErrIs: manifestreader.ErrTooLarge,
		},
		{
			name:      "a body shorter than its Content-Length is a broken transfer",
			step:      httpStep{status: 200, body: "hel", contentLength: "5"},
			limit:     5,
			wantFetch: true,
		},
		{name: "a body of the Content-Length", step: okAnswer("hello"), limit: 100, wantBytes: "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := &scriptedTransport{steps: []httpStep{tt.step}}
			fac := connection.New(connection.WithRoundTripper(transport))
			require.NoError(t, fac.Set(s3Conn, s3Spec()))

			got, err := manifestreader.NewS3Store(fac, s3Conn, s3Bucket).
				Get(t.Context(), manifestreader.GetRequest{Key: "k", Limit: tt.limit})

			switch {
			case tt.wantErrIs != nil:
				require.ErrorIs(t, err, tt.wantErrIs)
			case tt.wantFetch:
				var fetchErr *manifestreader.FetchError

				require.ErrorAs(t, err, &fetchErr)
				assert.True(t, fetchErr.Retryable)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.wantBytes, string(got.Bytes))
				assert.Equal(t, len(tt.wantBytes), cap(got.Bytes), "one buffer of exactly the size")
			}
		})
	}
}
