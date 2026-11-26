package s3exporterv2

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
		Region:       "us-east-1",
		BaseEndpoint: aws.String(srv.URL),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider("id", "secret", ""),
	})

	exp := &Exporter{S3Client: client, Bucket: "injected-bucket", S3Path: "events", Format: "json", Filename: "f.json"}
	err := exp.Export(context.Background(), slog.New(slog.DiscardHandler), []exporter.ExportableEvent{
		&exporter.FeatureEvent{Kind: "feature", Key: "k"},
	})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()

	assert.Equal(t, []string{"PUT /injected-bucket/events/f.json"}, paths)
	assert.Nil(t, exp.AwsConfig, "no default AWS configuration is loaded when a client is injected")
}
