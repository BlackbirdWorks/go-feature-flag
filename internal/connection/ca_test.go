package connection_test

import (
	"errors"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/connection"
)

var errBundleGone = errors.New("the bundle file is gone")

// bundleFiles is an in-memory file system for CA bundles that counts the reads and can lose its files.
type bundleFiles struct {
	files map[string][]byte
	reads atomic.Int32
	mu    sync.Mutex
}

func newBundleFiles(content []byte) *bundleFiles {
	return &bundleFiles{files: map[string][]byte{testCAPath: content}}
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

// set replaces the content of the bundle; nil removes the file.
func (b *bundleFiles) set(data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if data == nil {
		delete(b.files, testCAPath)

		return
	}

	b.files[testCAPath] = data
}

// headAt sends a HeadObject to the endpoint, whatever the endpoint of the connection.
func headAt(t *testing.T, client *s3.Client, endpoint string) error {
	t.Helper()

	_, err := client.HeadObject(t.Context(), headInputFor(), func(opts *s3.Options) {
		opts.BaseEndpoint = aws.String(endpoint)
	})

	return err
}

func headInputFor() *s3.HeadObjectInput {
	return &s3.HeadObjectInput{Bucket: aws.String("b"), Key: aws.String("k")}
}

// TestFactory_CABundleFileTrust uses two servers signed by two CAs that do not trust each other, and real
// TLS requests: a connection trusts the authority of its bundle, and only that one. It fails if the bundle
// were ignored (nothing is reached) or if every authority were trusted (both servers are reached).
func TestFactory_CABundleFileTrust(t *testing.T) {
	t.Parallel()

	caA, caB := newTestCA(t, "CA A"), newTestCA(t, "CA B")
	srvA, srvB := caA.startServer(t, okHandler()), caB.startServer(t, okHandler())

	tests := []struct {
		name           string
		bundle         []byte
		wantObsKind    []string
		wantAUntrusted bool // false: the request succeeds
		wantBUntrusted bool
	}{
		{
			name:           "bundle of A",
			bundle:         caA.pem,
			wantObsKind:    []string{resOK, resError},
			wantAUntrusted: false,
			wantBUntrusted: true,
		},
		{
			name:           "bundle of B",
			bundle:         caB.pem,
			wantObsKind:    []string{resError, resOK},
			wantAUntrusted: true,
			wantBUntrusted: false,
		},
		{
			name:        "both authorities in the bundle",
			bundle:      slices.Concat(caA.pem, caB.pem),
			wantObsKind: []string{resOK, resOK},
		},
		{
			name:           "no bundle: system roots only",
			bundle:         nil,
			wantObsKind:    []string{resError, resError},
			wantAUntrusted: true,
			wantBUntrusted: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := &recorder{}
			files := newBundleFiles(tt.bundle)
			fac := connection.New(connection.WithObserver(rec), connection.WithReadFile(files.read))
			require.NoError(t, fac.Set(connC, other(func(spec *connection.S3Spec) {
				spec.Endpoint = srvA.URL
				spec.Retry.MaxAttempts = 1

				if tt.bundle != nil {
					spec.CABundleFile = testCAPath
				}
			})))

			client, err := fac.S3(t.Context(), connC)
			require.NoError(t, err)

			errA, errB := headAt(t, client, srvA.URL), headAt(t, client, srvB.URL)

			for _, check := range []struct {
				err           error
				wantUntrusted bool
			}{{errA, tt.wantAUntrusted}, {errB, tt.wantBUntrusted}} {
				if !check.wantUntrusted {
					require.NoError(t, check.err)
				} else {
					requireUntrustedCertError(t, check.err)
				}
			}

			observed := rec.all()
			got := make([]string, 0, len(observed))

			for _, o := range observed {
				got = append(got, o.Result)
			}

			assert.Equal(t, tt.wantObsKind, got)
		})
	}
}

// TestFactory_CABundleRotation is one stateful scenario (a sequence, not independent cases): the bundle file
// changes from CA A to CA B. Without a reload the existing client keeps trusting A only; after Replace the
// rebuilt client trusts B only. It fails if the lazy build or the transport re-read the file, and if the
// reload kept the old client.
func TestFactory_CABundleRotation(t *testing.T) {
	t.Parallel()

	caA, caB := newTestCA(t, "CA A"), newTestCA(t, "CA B")
	srvA, srvB := caA.startServer(t, okHandler()), caB.startServer(t, okHandler())

	files := newBundleFiles(caA.pem)
	fac := connection.New(connection.WithReadFile(files.read))
	spec := func() *connection.S3Spec {
		return other(func(spec *connection.S3Spec) {
			spec.Endpoint = srvA.URL
			spec.CABundleFile = testCAPath
			spec.Retry.MaxAttempts = 1
		})
	}

	require.NoError(t, fac.Set(connC, spec()))

	before, err := fac.S3(t.Context(), connC)
	require.NoError(t, err)
	require.NoError(t, headAt(t, before, srvA.URL))
	requireUntrustedCertError(t, headAt(t, before, srvB.URL))

	// the file changes, nobody reloads: nothing changes
	files.set(caB.pem)

	same, err := fac.S3(t.Context(), connC)
	require.NoError(t, err)
	assert.Same(t, before, same)
	require.NoError(t, headAt(t, same, srvA.URL))
	requireUntrustedCertError(t, headAt(t, same, srvB.URL))

	// the reload rebuilds the client with the new authority
	require.NoError(t, fac.Replace(map[string]connection.Spec{connC: spec()}))

	after, err := fac.S3(t.Context(), connC)
	require.NoError(t, err)
	assert.NotSame(t, before, after)
	require.NoError(t, headAt(t, after, srvB.URL))
	requireUntrustedCertError(t, headAt(t, after, srvA.URL))
	assert.Equal(t, map[string]int{connC: 2}, fac.Builds())
}

// TestFactory_CABundleIsReadOnceAtSet is one stateful scenario: the bundle is read once per Set, never by the
// lazy build, even when the file is gone by then; an unreadable file refuses the Set and keeps the client.
func TestFactory_CABundleIsReadOnceAtSet(t *testing.T) {
	t.Parallel()

	authority := newTestCA(t, "CA")
	srv := authority.startServer(t, okHandler())
	files := newBundleFiles(authority.pem)
	rec := &recorder{}
	fac := connection.New(connection.WithReadFile(files.read), connection.WithObserver(rec))
	spec := func() *connection.S3Spec {
		return other(func(spec *connection.S3Spec) {
			spec.Endpoint = srv.URL
			spec.CABundleFile = testCAPath
			spec.Retry.MaxAttempts = 1
		})
	}

	require.NoError(t, fac.Set(connC, spec()))
	assert.Equal(t, int32(1), files.reads.Load(), "one read per Set")

	files.set(nil) // gone before the first use

	client, err := fac.S3(t.Context(), connC)
	require.NoError(t, err)
	require.NoError(t, headObject(t, client))
	assert.Equal(t, int32(1), files.reads.Load(), "the lazy build does not read the file")
	assert.Equal(t, []observation{{connC, opHead, resOK}}, rec.all())

	// same content again: one more read, the client is reused
	files.set(authority.pem)
	require.NoError(t, fac.Set(connC, spec()))
	assert.Equal(t, int32(2), files.reads.Load())
	assert.Equal(t, map[string]int{connC: 1}, fac.Builds())

	again, err := fac.S3(t.Context(), connC)
	require.NoError(t, err)
	assert.Same(t, client, again)

	// an unreadable file refuses the Set and keeps the working client
	files.set(nil)
	require.ErrorIs(t, fac.Set(connC, spec()), connection.ErrInvalidSpec)
	assert.Equal(t, int32(3), files.reads.Load())

	kept, err := fac.S3(t.Context(), connC)
	require.NoError(t, err)
	assert.Same(t, client, kept)
}

func TestFactory_CABundleFileValidation(t *testing.T) {
	t.Parallel()

	good := newTestCA(t, "CA").pem

	tests := []struct {
		files   map[string][]byte
		name    string
		path    string
		wantErr bool
	}{
		{name: "valid bundle", files: map[string][]byte{testCAPath: good}, path: testCAPath},
		{name: "missing file", files: map[string][]byte{}, path: testCAPath, wantErr: true},
		{name: "empty file", files: map[string][]byte{testCAPath: {}}, path: testCAPath, wantErr: true},
		{
			name: "no certificate in the file", files: map[string][]byte{testCAPath: []byte("not pem")},
			path: testCAPath, wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fac := connection.New(connection.WithReadFile(func(path string) ([]byte, error) {
				data, ok := tt.files[path]
				if !ok {
					return nil, os.ErrNotExist
				}

				return data, nil
			}))

			err := fac.Set(connC, other(func(spec *connection.S3Spec) { spec.CABundleFile = tt.path }))

			if !tt.wantErr {
				require.NoError(t, err)
				assert.Equal(t, []string{connC}, fac.Names())

				return
			}

			require.ErrorIs(t, err, connection.ErrInvalidSpec)

			var specErr *connection.SpecError

			require.ErrorAs(t, err, &specErr)
			assert.Equal(t, "caBundleFile", specErr.Field)
			assert.Equal(t, connC, specErr.Connection)
			assert.Empty(t, fac.Names())
		})
	}
}
