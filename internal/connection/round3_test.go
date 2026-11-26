package connection_test

import (
	"context"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/connection"
)

var errBundleGone = errors.New("the bundle file is gone")

// bundleFiles is an in-memory file system that counts the reads and can lose its files.
type bundleFiles struct {
	files map[string][]byte
	reads atomic.Int32
	mu    sync.Mutex
}

func (b *bundleFiles) read(path string) ([]byte, error) {
	b.reads.Add(1)

	b.mu.Lock()
	defer b.mu.Unlock()

	data, ok := b.files[path]
	if !ok {
		return nil, errBundleGone
	}

	return data, nil
}

func (b *bundleFiles) set(data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if data == nil {
		delete(b.files, testCAPath)

		return
	}

	b.files[testCAPath] = data
}

func certPEM(srv *httptest.Server) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: pemCertificate, Bytes: srv.Certificate().Raw})
}

func TestFactory_CABundleIsReadOnceAtSet(t *testing.T) {
	t.Parallel()

	srvA := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
	)
	t.Cleanup(srvA.Close)

	srvB := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(srvB.Close)

	files := &bundleFiles{files: map[string][]byte{testCAPath: certPEM(srvA)}}
	rec := &recorder{}
	fac := connection.New(connection.WithReadFile(files.read), connection.WithObserver(rec))

	spec := func() *connection.S3Spec {
		return other(func(s *connection.S3Spec) {
			s.Endpoint = srvA.URL
			s.CABundleFile = testCAPath
			s.Retry.MaxAttempts = 1
		})
	}

	require.NoError(t, fac.Set(connC, spec()))
	assert.Equal(t, int32(1), files.reads.Load(), "one read per Set")

	// the file disappears before the first use: the validated roots are used, the file is not read again
	files.set(nil)

	client, err := fac.S3(t.Context(), connC)
	require.NoError(t, err)
	require.NoError(t, headObject(t, client))
	assert.Equal(t, int32(1), files.reads.Load(), "the lazy build does not read the file")
	assert.Equal(t, []observation{{connC, opHead, resOK}}, rec.all())

	// the same content again: one more read, the client is reused
	files.set(certPEM(srvA))
	require.NoError(t, fac.Set(connC, spec()))
	assert.Equal(t, int32(2), files.reads.Load())
	assert.Equal(t, map[string]int{connC: 1}, fac.Builds())

	again, err := fac.S3(t.Context(), connC)
	require.NoError(t, err)
	assert.Same(t, client, again)

	// another certificate under the same path: a new client on the next use
	files.set(append(certPEM(srvB), []byte("# rotated\n")...))
	require.NoError(t, fac.Set(connC, spec()))
	assert.Equal(t, int32(3), files.reads.Load())

	rebuilt, err := fac.S3(t.Context(), connC)
	require.NoError(t, err)
	assert.NotSame(t, client, rebuilt)
	assert.Equal(t, map[string]int{connC: 2}, fac.Builds())

	// a file that cannot be read now refuses the Set and keeps the working client
	files.set(nil)
	require.ErrorIs(t, fac.Set(connC, spec()), connection.ErrInvalidSpec)

	kept, err := fac.S3(t.Context(), connC)
	require.NoError(t, err)
	assert.Same(t, rebuilt, kept)
}

// reentrantHandler is a slog handler that calls back into the factory, as a handler that logs through the
// connection layer would.
type reentrantHandler struct {
	call  func()
	calls atomic.Int32
}

func (*reentrantHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *reentrantHandler) Handle(context.Context, slog.Record) error {
	h.calls.Add(1)
	h.call()

	return nil
}
func (h *reentrantHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *reentrantHandler) WithGroup(string) slog.Handler      { return h }

// finishes runs fn and reports whether it returned within the timeout: a deadlock never returns.
func finishes(run func()) bool {
	done := make(chan struct{})

	go func() {
		defer close(done)

		run()
	}()

	select {
	case <-done:
		return true
	case <-time.After(10 * time.Second):
		return false
	}
}

func TestFactory_NoLockIsHeldAcrossACallback(t *testing.T) {
	t.Parallel()

	t.Run("logger", func(t *testing.T) {
		t.Parallel()

		var fac *connection.Factory

		var callbackErr error

		handler := &reentrantHandler{}
		handler.call = func() {
			_, callbackErr = fac.AWSConfig(context.Background(), "a")
			assert.Equal(t, []string{"a"}, fac.Names())
			assert.Equal(t, map[string]int{"a": 1}, fac.Builds())
		}

		fac = connection.New(connection.WithLogger(slog.New(handler)))
		require.NoError(t, fac.Set("a", testS3()))

		var s3Err error

		require.True(t, finishes(func() {
			_, s3Err = fac.S3(t.Context(), "a")
		}), "the logger was called while a lock was held")
		require.NoError(t, s3Err)
		require.NoError(t, callbackErr)
		assert.Equal(t, int32(1), handler.calls.Load())
	})

	t.Run("ReadFile", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewTLSServer(http.NotFoundHandler())
		t.Cleanup(srv.Close)

		var fac *connection.Factory

		fac = connection.New(connection.WithReadFile(func(string) ([]byte, error) {
			assert.Equal(t, []string{"a"}, fac.Names())

			return certPEM(srv), nil
		}))
		require.NoError(t, fac.Set("a", testS3()))

		var setErr error

		require.True(t, finishes(func() {
			setErr = fac.Set("a", other(func(s *connection.S3Spec) { s.CABundleFile = testCAPath }))
		}), "ReadFile was called while a lock was held")
		require.NoError(t, setErr)
	})

	t.Run("observer", func(t *testing.T) {
		t.Parallel()

		var (
			fac   *connection.Factory
			calls atomic.Int32
		)

		obs := observerFunc(func(string, string, string) {
			calls.Add(1)

			_, err := fac.AWSConfig(context.Background(), "a")
			assert.NoError(t, err)
		})
		transport := newScripted(step{status: 200})
		fac = connection.New(connection.WithObserver(obs), connection.WithRoundTripper(transport))
		require.NoError(t, fac.Set("a", testS3()))

		client, err := fac.S3(t.Context(), "a")
		require.NoError(t, err)

		var headErr error

		require.True(t, finishes(func() { headErr = headObject(t, client) }))
		require.NoError(t, headErr)
		assert.Equal(t, 1, transport.count())
		assert.Equal(t, int32(1), calls.Load())
	})
}

type observerFunc func(conn, operation, result string)

func (f observerFunc) Observe(conn, operation, result string) { f(conn, operation, result) }

// TestFactory_AWSConfigSharesNothingMutable mutates every pointer, slice and map of a returned configuration
// and checks that a second call is not affected; only the HTTP client and the credentials provider are shared,
// on purpose.
func TestFactory_AWSConfigSharesNothingMutable(t *testing.T) {
	t.Parallel()

	fac := connection.New(connection.WithObserver(&recorder{}))
	require.NoError(t, fac.Set("a", testS3()))

	first, err := fac.AWSConfig(t.Context(), "a")
	require.NoError(t, err)

	second, err := fac.AWSConfig(t.Context(), "a")
	require.NoError(t, err)

	require.NotNil(t, first.BaseEndpoint)

	*first.BaseEndpoint = "http://tampered.test"

	third, err := fac.AWSConfig(t.Context(), "a")
	require.NoError(t, err)
	assert.Equal(t, "http://s3.test", aws.ToString(third.BaseEndpoint))
	assert.Equal(t, "http://s3.test", aws.ToString(second.BaseEndpoint))

	shared := map[string]bool{"HTTPClient": true, "Credentials": true, "Retryer": true}

	// every other pointer, slice or map field set by the factory must be a distinct copy
	valueA, valueB := reflect.ValueOf(second), reflect.ValueOf(third)

	for i := range valueA.NumField() {
		field := valueA.Type().Field(i)
		fieldA, fieldB := valueA.Field(i), valueB.Field(i)

		kind := fieldA.Kind()
		if kind != reflect.Pointer && kind != reflect.Map && kind != reflect.Slice {
			continue
		}

		if fieldA.IsNil() || shared[field.Name] {
			continue
		}

		assert.NotEqual(t, fieldA.UnsafePointer(), fieldB.UnsafePointer(),
			"%s aliases the cached configuration", field.Name)
	}
}
