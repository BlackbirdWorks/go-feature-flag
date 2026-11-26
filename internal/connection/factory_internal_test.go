package connection

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func removeFrom(name string) func(*Factory) error {
	return func(fac *Factory) error {
		fac.Remove(name)

		return nil
	}
}

func internalSpec() *S3Spec {
	return &S3Spec{
		Endpoint: "http://s3.test",
		Region:   "us-east-1",
		Credentials: Credentials{
			Type: CredentialsStatic, AccessKeyID: NewSecret("AKID"), SecretAccessKey: NewSecret("secret"),
		},
		Bucket: "bucket",
	}
}

// TestFactory_RetiredEntryIsNotBuilt pauses a caller between the lookup of an entry and the lock of its build
// (the afterLookup hook), retires the entry meanwhile, and checks what the caller gets: nothing is built for
// the removed entry; the caller gets an unknown connection error, or the client of the entry that took the
// name over.
func TestFactory_RetiredEntryIsNotBuilt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		retire     func(*Factory) error
		wantBuilds map[string]int
		wantErr    error
		name       string
	}{
		{
			name:       "removed",
			retire:     removeFrom("a"),
			wantErr:    ErrUnknownConnection,
			wantBuilds: map[string]int{},
		},
		{
			name:       "dropped by a reload",
			retire:     func(f *Factory) error { return f.Replace(map[string]Spec{}) },
			wantErr:    ErrUnknownConnection,
			wantBuilds: map[string]int{},
		},
		{
			name: "removed and added again",
			retire: func(f *Factory) error {
				f.Remove("a")

				return f.Set("a", internalSpec())
			},
			wantBuilds: map[string]int{"a": 1},
		},
		{
			name: "replaced by a changed spec",
			retire: func(f *Factory) error {
				changed := internalSpec()
				changed.Bucket = "other"

				return f.Set("a", changed)
			},
			wantBuilds: map[string]int{"a": 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fac := New()
			require.NoError(t, fac.Set("a", internalSpec()))

			var paused atomic.Bool

			fac.afterLookup = func(string) {
				// the first lookup is the paused one: retire its entry before it takes the lock
				if paused.CompareAndSwap(false, true) {
					assert.NoError(t, tt.retire(fac))
				}
			}

			client, err := fac.S3(t.Context(), "a")
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, client)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, client)
			}

			assert.Equal(t, tt.wantBuilds, fac.Builds())
		})
	}
}

// TestFactory_BuildFinishedBeforeRetireIsClosed covers the other order: the build finishes first, and the
// retirement then closes what was built.
func TestFactory_BuildFinishedBeforeRetireIsClosed(t *testing.T) {
	t.Parallel()

	transport := &idleCloser{}
	fac := New(WithRoundTripper(transport))
	require.NoError(t, fac.Set("a", internalSpec()))

	ent, err := fac.lookup("a")
	require.NoError(t, err)

	_, err = fac.S3(t.Context(), "a")
	require.NoError(t, err)

	fac.Remove("a")
	assert.True(t, ent.retired)
	assert.Equal(t, int32(1), transport.closed.Load())
}

type idleCloser struct {
	closed atomic.Int32
}

func (*idleCloser) RoundTrip(*http.Request) (*http.Response, error) { return nil, errRetired }
func (c *idleCloser) CloseIdleConnections()                         { c.closed.Add(1) }

// TestTransport_HasNoProxy checks the transport the factory builds, for several settings of the spec. It does
// not read HTTP_PROXY: the standard library resolves the proxy environment once per process, so a test that sets
// it passes or fails depending on which test ran first. The property is that the transport has no proxy
// function at all, so no process setting can route a connection through one; the second server below would
// only be reached by a proxy, and a request through the production transport reaches the target.
func TestTransport_HasNoProxy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mutate          func(*S3Spec)
		name            string
		wantHeaderLimit time.Duration
	}{
		{name: "defaults", mutate: func(*S3Spec) {}},
		{
			name:            "request timeout",
			mutate:          func(s *S3Spec) { s.RequestTimeout = 3 * time.Second },
			wantHeaderLimit: 3 * time.Second,
		},
		{name: "connect timeout", mutate: func(s *S3Spec) { s.ConnectTimeout = time.Second }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var targetHits, proxyHits atomic.Int32

			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				targetHits.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(target.Close)

			proxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { proxyHits.Add(1) }))
			t.Cleanup(proxy.Close)

			fac := New()
			spec := internalSpec()
			spec.Endpoint = target.URL
			spec.PathStyle = true
			tt.mutate(spec)
			require.NoError(t, fac.Set("a", spec))

			choice := fac.newHTTPClient(&spec.Common, nil)
			require.NotNil(t, choice.owned)

			transport, ok := choice.owned.Transport.(*http.Transport)
			require.True(t, ok)
			assert.Nil(t, transport.Proxy)
			assert.Equal(t, tt.wantHeaderLimit, transport.ResponseHeaderTimeout)

			client, err := fac.S3(t.Context(), "a")
			require.NoError(t, err)

			_, err = client.HeadObject(t.Context(), headInput())
			require.NoError(t, err)
			assert.Equal(t, int32(1), targetHits.Load())
			assert.Equal(t, int32(0), proxyHits.Load())
		})
	}
}

func headInput() *s3.HeadObjectInput {
	return &s3.HeadObjectInput{Bucket: aws.String("b"), Key: aws.String("k")}
}
