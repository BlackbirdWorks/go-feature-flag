package kinesisexporter

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
)

// signedRequest is what the fake endpoint saw of one request.
type signedRequest struct {
	Target string
	KeyID  string
	Region string
}

var credentialScope = regexp.MustCompile(`Credential=([^/]+)/[^/]+/([^/]+)/`)

// fakeEndpoint answers every request with reply and records the target, the access key id and the region of the
// signature of each request.
func fakeEndpoint(t *testing.T, contentType, reply string) (string, func() []signedRequest) {
	t.Helper()

	var (
		mu   sync.Mutex
		seen []signedRequest
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		assert.NoError(t, err)

		got := signedRequest{Target: r.Header.Get("X-Amz-Target")}
		if match := credentialScope.FindStringSubmatch(r.Header.Get("Authorization")); match != nil {
			got.KeyID, got.Region = match[1], match[2]
		}

		mu.Lock()

		seen = append(seen, got)
		mu.Unlock()

		w.Header().Set("Content-Type", contentType)
		_, err = w.Write([]byte(reply))
		assert.NoError(t, err)
	}))
	t.Cleanup(srv.Close)

	return srv.URL, func() []signedRequest {
		mu.Lock()
		defer mu.Unlock()

		return append([]signedRequest(nil), seen...)
	}
}

// connectionConfig is the configuration of a connection: its own endpoint, region and static credentials.
func connectionConfig(endpoint, region, keyID string) *aws.Config {
	return &aws.Config{
		Region: region, BaseEndpoint: aws.String(endpoint),
		Credentials: credentials.NewStaticCredentialsProvider(keyID, "secret", ""),
	}
}

// TestExporter_ClientIsBuiltFromTheGivenConfiguration: the client that the exporter builds sends to the endpoint
// of the given configuration, signed with its region and credentials, and the default chain is not loaded.
func TestExporter_ClientIsBuiltFromTheGivenConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		region        string
		keyID         string
		wantRequests  []signedRequest
		wantChainLoad int
	}{
		{
			name: "connection A", region: "eu-west-1", keyID: "AKIDA",
			wantRequests: []signedRequest{{Target: "Kinesis_20131202.PutRecords", KeyID: "AKIDA", Region: "eu-west-1"}},
		},
		{
			name: "connection B", region: "us-east-2", keyID: "AKIDB",
			wantRequests: []signedRequest{{Target: "Kinesis_20131202.PutRecords", KeyID: "AKIDB", Region: "us-east-2"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			endpoint, seen := fakeEndpoint(t, "application/x-amz-json-1.1",
				`{"FailedRecordCount":0,"Records":[{"SequenceNumber":"1","ShardId":"s"}]}`)

			loads := 0
			exp := &Exporter{
				AwsConfig: connectionConfig(endpoint, tt.region, tt.keyID),
				Settings:  NewSettings(WithStreamName("stream")),
				loadConfig: func(context.Context) (aws.Config, error) {
					loads++

					return aws.Config{}, errNoChain
				},
			}

			err := exp.Export(t.Context(), slog.New(slog.DiscardHandler), []exporter.ExportableEvent{
				&exporter.FeatureEvent{Kind: "feature", Key: "flag-a", UserKey: "user-1", CreationDate: 1617970547},
			})

			require.NoError(t, err)
			assert.Equal(t, tt.wantChainLoad, loads)
			assert.Equal(t, tt.wantRequests, seen())
		})
	}
}
