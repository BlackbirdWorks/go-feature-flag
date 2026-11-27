package s3exporterv2

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
)

// TestExport_UsesInjectedS3Client checks that a ready-made S3 client is used as it is: the request reaches
// the endpoint of that client, and the exporter builds no other client.
func TestExport_UsesInjectedS3Client(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		paths []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)

		mu.Lock()
		defer mu.Unlock()

		paths = append(paths, r.Method+" "+r.URL.Path)

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := s3.New(s3.Options{
		Region:       testRegion,
		BaseEndpoint: aws.String(srv.URL),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider("id", "secret", ""),
	})

	exp := &Exporter{
		S3Client: client,
		Bucket:   "injected-bucket",
		S3Path:   "events",
		Format:   formatJSON,
		Filename: testFile,
	}
	err := exp.Export(context.Background(), slog.New(slog.DiscardHandler), []exporter.ExportableEvent{
		&exporter.FeatureEvent{Kind: "feature", Key: "k"},
	})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()

	assert.Equal(t, []string{"PUT /injected-bucket/events/f.json"}, paths)
	assert.Nil(t, exp.AwsConfig, "no default AWS configuration is loaded when a client is injected")
}

const (
	formatJSON = "json"
	testRegion = "us-east-1"
	testFile   = "f.json"
)

// s3Endpoint starts a test server that answers every request with 200 and records "METHOD path".
func s3Endpoint(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()

	var (
		mu    sync.Mutex
		paths []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)

		mu.Lock()
		defer mu.Unlock()

		paths = append(paths, r.Method+" "+r.URL.Path)

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()

		return slices.Clone(paths)
	}
}

// oneEvent returns a batch of one event.
func oneEvent() []exporter.ExportableEvent {
	return []exporter.ExportableEvent{&exporter.FeatureEvent{Kind: "feature", Key: "k"}}
}

// TestExport_BuildsItsClientFromTheAwsConfig checks the path where the exporter builds the client from the
// configuration it was given, with the client options.
func TestExport_BuildsItsClientFromTheAwsConfig(t *testing.T) {
	t.Parallel()

	srv, requests := s3Endpoint(t)

	exp := &Exporter{
		AwsConfig: &aws.Config{
			Region: testRegion, BaseEndpoint: aws.String(srv.URL),
			Credentials: credentials.NewStaticCredentialsProvider("id", "secret", ""),
		},
		S3ClientOptions: []func(*s3.Options){func(opts *s3.Options) { opts.UsePathStyle = true }},
		Bucket:          "cfg-bucket", S3Path: "p", Format: formatJSON, Filename: testFile,
	}

	require.NoError(t, exp.Export(context.Background(), slog.New(slog.DiscardHandler), oneEvent()))
	assert.Equal(t, []string{"PUT /cfg-bucket/p/f.json"}, requests())
}

// TestExport_LoadsTheDefaultConfigWithoutOne checks the fallback of an exporter built with neither a client nor a
// configuration: the default chain, here pointed to a test server through its environment.
func TestExport_LoadsTheDefaultConfigWithoutOne(t *testing.T) {
	srv, requests := s3Endpoint(t)

	t.Setenv("AWS_ENDPOINT_URL_S3", srv.URL)
	t.Setenv("AWS_REGION", testRegion)
	t.Setenv("AWS_ACCESS_KEY_ID", "id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	exp := &Exporter{
		S3ClientOptions: []func(*s3.Options){func(opts *s3.Options) { opts.UsePathStyle = true }},
		Bucket:          "env-bucket", S3Path: "p", Format: formatJSON, Filename: testFile,
	}

	require.NoError(t, exp.Export(context.Background(), slog.New(slog.DiscardHandler), oneEvent()))
	assert.Equal(t, []string{"PUT /env-bucket/p/f.json"}, requests())
	assert.NotNil(t, exp.AwsConfig, "the configuration that was loaded is kept")
}

// TestExport_Errors checks the errors before the upload. Each case sets the environment it needs.
func TestExport_Errors(t *testing.T) {
	tests := []struct {
		client  *s3.Client
		env     map[string]string
		name    string
		wantErr string
	}{
		{
			name:    "the default configuration cannot be loaded",
			env:     map[string]string{"AWS_CA_BUNDLE": "/does/not/exist.pem"},
			wantErr: "impossible to init S3 exporter",
		},
		{
			name: "no temporary directory", env: map[string]string{"TMPDIR": "/does/not/exist"},
			client:  s3.New(s3.Options{Region: testRegion}),
			wantErr: "create temp dir for S3 export",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			exp := &Exporter{Bucket: "b", Format: formatJSON, S3Client: tt.client}

			err := exp.Export(context.Background(), slog.New(slog.DiscardHandler), oneEvent())
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
