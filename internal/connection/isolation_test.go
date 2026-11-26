package connection_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/connection"
)

func headObject(t *testing.T, client *s3.Client) error {
	t.Helper()

	_, err := client.HeadObject(t.Context(), &s3.HeadObjectInput{Bucket: aws.String("b"), Key: aws.String("k")})

	return err
}

func TestRedirects_AreNotFollowed(t *testing.T) {
	t.Parallel()

	t.Run("round tripper path", func(t *testing.T) {
		t.Parallel()

		transport := newScripted(
			step{status: 307, header: http.Header{"Location": {"http://elsewhere.test/b/k"}}},
			step{status: 200},
		)
		rec := &recorder{}
		fac := connection.New(connection.WithRoundTripper(transport), connection.WithObserver(rec))
		require.NoError(t, fac.Set(connC, testS3()))

		client, err := fac.S3(t.Context(), connC)
		require.NoError(t, err)

		require.Error(t, headObject(t, client))
		assert.Equal(t, 1, transport.count())
		assert.Equal(t, []observation{{connC, opHead, resRedirect}}, rec.all())
	})

	t.Run("client built by the factory", func(t *testing.T) {
		t.Parallel()

		var targetHits, sourceHits atomic.Int32

		target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetHits.Add(1) }))
		t.Cleanup(target.Close)

		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sourceHits.Add(1)
			http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
		}))
		t.Cleanup(source.Close)

		rec := &recorder{}
		fac := connection.New(connection.WithObserver(rec))
		require.NoError(t, fac.Set(connC, other(func(spec *connection.S3Spec) { spec.Endpoint = source.URL })))

		client, err := fac.S3(t.Context(), connC)
		require.NoError(t, err)

		require.Error(t, headObject(t, client))
		assert.Equal(t, int32(1), sourceHits.Load())
		assert.Equal(t, int32(0), targetHits.Load())
		assert.Equal(t, []observation{{connC, opHead, resRedirect}}, rec.all())
	})
}

func TestTypedNilSpecs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		spec connection.Spec
		name string
	}{
		{name: "nil S3Spec", spec: (*connection.S3Spec)(nil)},
		{name: "nil AWSSpec", spec: (*connection.AWSSpec)(nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fac := connection.New()

			require.ErrorIs(t, fac.Set("x", tt.spec), connection.ErrInvalidSpec)
			require.ErrorIs(t, fac.Replace(map[string]connection.Spec{"x": tt.spec}), connection.ErrInvalidSpec)
			assert.Empty(t, fac.Names())
			assert.Empty(t, tt.spec.Identity())
		})
	}
}

func TestFactory_EmptyNameAndSortedNames(t *testing.T) {
	t.Parallel()

	fac := connection.New()

	require.ErrorIs(t, fac.Set("", testS3()), connection.ErrInvalidSpec)
	require.ErrorIs(t, fac.Replace(map[string]connection.Spec{"": testS3()}), connection.ErrInvalidSpec)

	require.NoError(t, fac.Replace(map[string]connection.Spec{"zeta": testS3(), "alpha": testS3(), "mid": testS3()}))
	assert.Equal(t, []string{"alpha", "mid", "zeta"}, fac.Names())
}

func TestSpecError_Messages(t *testing.T) {
	t.Parallel()

	err := connection.New().Set("named", other(func(spec *connection.S3Spec) { spec.Region = "" }))
	require.Error(t, err)
	assert.Equal(t, `invalid connection "named": region: is required`, err.Error())

	assert.Equal(t, "invalid connection spec: region: is required", (&connection.S3Spec{}).Validate().Error())
	assert.Equal(t, `connection "q" is of kind "sqs", not "s3"`,
		(&connection.KindError{Name: "q", Want: "s3", Got: "sqs"}).Error())
}

func TestFactory_AWSConfigIsACopy(t *testing.T) {
	t.Parallel()

	fac := connection.New(connection.WithObserver(&recorder{}))
	require.NoError(t, fac.Set("a", testS3()))

	first, err := fac.AWSConfig(t.Context(), "a")
	require.NoError(t, err)
	require.NotEmpty(t, first.APIOptions)

	want := len(first.APIOptions)

	// both writes go through the shared backing array when the slice is not cloned
	first.APIOptions[0] = nil
	first.APIOptions = append(first.APIOptions[:0], nil)

	second, err := fac.AWSConfig(t.Context(), "a")
	require.NoError(t, err)
	require.Len(t, second.APIOptions, want)
	assert.NotNil(t, second.APIOptions[0], "the cached configuration was altered through a returned copy")

	// the cached configuration still carries a working observer
	rec := &recorder{}
	cachedTransport := newScripted(step{status: 200})
	cached := connection.New(connection.WithObserver(rec), connection.WithRoundTripper(cachedTransport))
	require.NoError(t, cached.Set("a", testS3()))

	cfg, err := cached.AWSConfig(t.Context(), "a")
	require.NoError(t, err)

	cfg.APIOptions[0] = nil

	client, err := cached.S3(t.Context(), "a")
	require.NoError(t, err)
	require.NoError(t, headObject(t, client))
	assert.Equal(t, 1, cachedTransport.count())
	assert.Equal(t, []observation{{"a", opHead, resOK}}, rec.all())
}

// closeCounter is a transport that counts CloseIdleConnections calls.
type closeCounter struct {
	closed atomic.Int32
}

func (*closeCounter) RoundTrip(*http.Request) (*http.Response, error) { return nil, errScriptExhausted }
func (c *closeCounter) CloseIdleConnections()                         { c.closed.Add(1) }

func removeFrom(name string) func(*connection.Factory) error {
	return func(fac *connection.Factory) error {
		fac.Remove(name)

		return nil
	}
}

func TestFactory_ClosesIdleConnectionsOfDroppedClients(t *testing.T) {
	t.Parallel()

	tests := []struct {
		action     func(*connection.Factory) error
		name       string
		wantClosed int32
	}{
		{
			name:       "remove closes",
			action:     removeFrom("a"),
			wantClosed: 1,
		},
		{
			name:       "remove unknown closes nothing",
			action:     removeFrom("zzz"),
			wantClosed: 0,
		},
		{
			name: "replace without it closes",
			action: func(f *connection.Factory) error {
				return f.Replace(map[string]connection.Spec{"b": testS3()})
			},
			wantClosed: 1,
		},
		{
			name: "replace with a changed spec closes the old client",
			action: func(f *connection.Factory) error {
				return f.Replace(map[string]connection.Spec{
					"a": other(func(spec *connection.S3Spec) { spec.Endpoint = "http://changed.test" }),
				})
			},
			wantClosed: 1,
		},
		{
			name: "replace with an unchanged spec closes nothing",
			action: func(f *connection.Factory) error {
				return f.Replace(map[string]connection.Spec{"a": testS3()})
			},
			wantClosed: 0,
		},
		{
			name: "set with a changed spec closes the old client",
			action: func(f *connection.Factory) error {
				return f.Set("a", other(func(spec *connection.S3Spec) { spec.Bucket = bucketOther }))
			},
			wantClosed: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := &closeCounter{}
			fac := connection.New(connection.WithRoundTripper(transport))
			require.NoError(t, fac.Set("a", testS3()))

			_, err := fac.S3(t.Context(), "a")
			require.NoError(t, err)

			require.NoError(t, tt.action(fac))
			assert.Equal(t, tt.wantClosed, transport.closed.Load())
		})
	}
}

func TestFactory_DoesNotCloseACallerSuppliedClient(t *testing.T) {
	t.Parallel()

	transport := &closeCounter{}
	fac := connection.New(connection.WithHTTPClient(&http.Client{Transport: transport}))
	require.NoError(t, fac.Set("a", testS3()))

	_, err := fac.S3(t.Context(), "a")
	require.NoError(t, err)

	fac.Remove("a")
	assert.Equal(t, int32(0), transport.closed.Load())
}

// TestFactory_IgnoresTheProcessEnvironment sets hostile AWS variables and checks that a connection still goes
// to the endpoint and region of its spec, and does not fail on the missing profile.
func TestFactory_IgnoresTheProcessEnvironment(t *testing.T) {
	t.Setenv("AWS_REGION", "ap-south-1")
	t.Setenv("AWS_DEFAULT_REGION", "ap-south-1")
	t.Setenv("AWS_ENDPOINT_URL", "http://hostile.test")
	t.Setenv("AWS_ENDPOINT_URL_S3", "http://hostile-s3.test")
	t.Setenv("AWS_PROFILE", "profile-that-does-not-exist")
	t.Setenv("AWS_DEFAULTS_MODE", "standard")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDHOSTILE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "hostile")

	tests := []struct {
		name  string
		creds connection.Credentials
	}{
		{
			"static",
			connection.Credentials{
				Type:            connection.CredentialsStatic,
				AccessKeyID:     connection.NewSecret("AKIDSPEC"),
				SecretAccessKey: connection.NewSecret("spec"),
			},
		},
	}

	for _, tt := range tests {
		transport := newScripted(step{status: 200})
		fac := connection.New(connection.WithRoundTripper(transport))
		require.NoError(t, fac.Set("a", other(func(spec *connection.S3Spec) {
			spec.Region = "eu-central-1"
			spec.Credentials = tt.creds
		})))

		client, err := fac.S3(t.Context(), "a")
		require.NoError(t, err, tt.name)
		require.NoError(t, headObject(t, client), tt.name)

		require.Equal(t, 1, transport.count(), tt.name)
		req := transport.requests[0]
		assert.Equal(t, "s3.test", req.URL.Host, tt.name)
		assert.Contains(t, req.Header.Get("Authorization"), "Credential=AKIDSPEC/", tt.name)
		assert.Contains(t, req.Header.Get("Authorization"), "/eu-central-1/s3/", tt.name)

		cfg, err := fac.AWSConfig(t.Context(), "a")
		require.NoError(t, err, tt.name)
		assert.Equal(t, "eu-central-1", cfg.Region, tt.name)
		assert.Equal(t, "http://s3.test", aws.ToString(cfg.BaseEndpoint), tt.name)
	}
}

func TestTimeouts(t *testing.T) {
	t.Parallel()

	const timeout = 100 * time.Millisecond

	t.Run("a body read longer than RequestTimeout is not cut", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "11")
			_, _ = io.WriteString(w, "hello")
			flusher, _ := w.(http.Flusher)

			if flusher != nil {
				flusher.Flush()
			}

			select {
			case <-time.After(3 * timeout): // the body outlasts RequestTimeout
			case <-r.Context().Done():
				return
			}

			_, _ = io.WriteString(w, " world")
		}))
		t.Cleanup(srv.Close)

		fac := connection.New()
		require.NoError(t, fac.Set(connC, other(func(spec *connection.S3Spec) {
			spec.Endpoint = srv.URL
			spec.RequestTimeout = timeout
			spec.Retry.MaxAttempts = 1
		})))

		client, err := fac.S3(t.Context(), connC)
		require.NoError(t, err)

		out, err := client.GetObject(t.Context(), &s3.GetObjectInput{Bucket: aws.String("b"), Key: aws.String("k")})
		require.NoError(t, err)

		body, err := io.ReadAll(out.Body)
		require.NoError(t, err)
		require.NoError(t, out.Body.Close())
		assert.Equal(t, "hello world", string(body))
	})

	t.Run("stalled response headers hit RequestTimeout", func(t *testing.T) {
		t.Parallel()

		var (
			mu   sync.Mutex
			hits int
		)

		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			mu.Lock()
			hits++
			mu.Unlock()

			<-r.Context().Done() // never answers until the client gives up
		}))
		t.Cleanup(srv.Close)

		rec := &recorder{}
		fac := connection.New(connection.WithObserver(rec))
		require.NoError(t, fac.Set(connC, other(func(spec *connection.S3Spec) {
			spec.Endpoint = srv.URL
			spec.RequestTimeout = timeout
			spec.Retry.MaxAttempts = 1
		})))

		client, err := fac.S3(t.Context(), connC)
		require.NoError(t, err)

		require.Error(t, headObject(t, client))
		assert.Equal(t, []observation{{connC, opHead, resError}}, rec.all())

		mu.Lock()
		defer mu.Unlock()

		assert.Equal(t, 1, hits)
	})

	t.Run("no RequestTimeout means no header limit", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-time.After(2 * timeout):
			case <-r.Context().Done():
				return
			}

			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)

		fac := connection.New()
		require.NoError(t, fac.Set(connC, other(func(spec *connection.S3Spec) { spec.Endpoint = srv.URL })))

		client, err := fac.S3(t.Context(), connC)
		require.NoError(t, err)
		require.NoError(t, headObject(t, client))
	})
}

func TestClassify_ThrottleAndClientErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantResult string
		status     int
	}{
		{name: "503 SlowDown", body: slowDownXML, status: 503, wantResult: resThrottled},
		{
			name: "400 RequestLimitExceeded", status: 400, wantResult: resThrottled,
			body: strings.ReplaceAll(slowDownXML, "SlowDown", "RequestLimitExceeded"),
		},
		{
			name: "400 other code", status: 400, wantResult: resClientError,
			body: strings.ReplaceAll(slowDownXML, "SlowDown", "InvalidArgument"),
		},
		{
			name: "503 other code", status: 503, wantResult: resServerError,
			body: strings.ReplaceAll(slowDownXML, "SlowDown", "ServiceUnavailable"),
		},
		{name: "301 redirect", status: 301, wantResult: resRedirect},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := &recorder{}
			transport := newScripted(step{status: tt.status, body: tt.body})
			fac := connection.New(
				connection.WithRoundTripper(transport),
				connection.WithObserver(rec),
			)
			require.NoError(t, fac.Set(connC, other(func(spec *connection.S3Spec) { spec.Retry.MaxAttempts = 1 })))

			client, err := fac.S3(t.Context(), connC)
			require.NoError(t, err)

			require.Error(t, headObject(t, client))
			assert.Equal(t, 1, transport.count())
			assert.Equal(t, []observation{{connC, opHead, tt.wantResult}}, rec.all())
		})
	}
}
