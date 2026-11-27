package sqsexporter

import (
	"context"
	"errors"
	"io"
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

var errUnmarshalable = errors.New("this event cannot be encoded")

// unencodableEvent is an event whose JSON encoding fails.
type unencodableEvent struct{ exporter.FeatureEvent }

func (unencodableEvent) MarshalJSON() ([]byte, error) { return nil, errUnmarshalable }

// signedRequest is what the fake SQS endpoint saw of one request.
type signedRequest struct {
	Target   string
	KeyID    string
	Region   string
	QueueURL string
}

var (
	credentialScope = regexp.MustCompile(`Credential=([^/]+)/[^/]+/([^/]+)/`)
	queueInBody     = regexp.MustCompile(`"QueueUrl":"([^"]*)"`)
)

// TestExporter_BuildsItsClientFromTheGivenConfiguration: without an injected SQS service the exporter builds its
// client from the configuration it was given (the connection's), so the request goes to that endpoint, signed
// with that configuration's region and credentials, and the default chain is not loaded.
func TestExporter_BuildsItsClientFromTheGivenConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		region       string
		keyID        string
		queueURL     string
		wantRequests []signedRequest
	}{
		{
			name: "connection A", region: "eu-west-1", keyID: "AKIDA", queueURL: "http://sqs.local/queue/a",
			wantRequests: []signedRequest{
				{
					Target:   "AmazonSQS.SendMessage",
					KeyID:    "AKIDA",
					Region:   "eu-west-1",
					QueueURL: "http://sqs.local/queue/a",
				},
			},
		},
		{
			name: "connection B", region: "us-east-2", keyID: "AKIDB", queueURL: "http://sqs.local/queue/b",
			wantRequests: []signedRequest{
				{
					Target:   "AmazonSQS.SendMessage",
					KeyID:    "AKIDB",
					Region:   "us-east-2",
					QueueURL: "http://sqs.local/queue/b",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				mu   sync.Mutex
				seen []signedRequest
			)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)

				got := signedRequest{Target: r.Header.Get("X-Amz-Target")}
				if match := credentialScope.FindStringSubmatch(r.Header.Get("Authorization")); match != nil {
					got.KeyID, got.Region = match[1], match[2]
				}

				if match := queueInBody.FindSubmatch(body); match != nil {
					got.QueueURL = string(match[1])
				}

				mu.Lock()

				seen = append(seen, got)
				mu.Unlock()

				w.Header().Set("Content-Type", "application/x-amz-json-1.0")
				_, err = w.Write([]byte(`{}`))
				assert.NoError(t, err)
			}))
			t.Cleanup(srv.Close)

			loads := 0
			exp := &Exporter{
				QueueURL: tt.queueURL,
				AwsConfig: &aws.Config{
					Region:       tt.region,
					BaseEndpoint: aws.String(srv.URL),
					Credentials:  credentials.NewStaticCredentialsProvider(tt.keyID, "secret", ""),
				},
				loadConfig: func(context.Context) (aws.Config, error) {
					loads++

					return aws.Config{}, errNoChain
				},
			}

			event := &exporter.FeatureEvent{
				Kind:         kindFeature,
				Key:          "flag-a",
				UserKey:      "user-1",
				CreationDate: 1617970547,
			}
			require.NoError(t, exp.Export(t.Context(), nil, []exporter.ExportableEvent{event}))

			assert.Equal(t, 0, loads, "the default chain is not loaded")

			mu.Lock()
			defer mu.Unlock()

			assert.Equal(t, tt.wantRequests, seen)
		})
	}
}

func TestExporter_EncodingErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		event        exporter.ExportableEvent
		wantErr      error
		name         string
		wantErrText  string
		wantMessages int
	}{
		{
			name: "an event that cannot be encoded is reported and nothing is sent", event: &unencodableEvent{},
			wantErr: errUnmarshalable, wantErrText: "marshal SQS event", wantMessages: 0,
		},
		{
			name: "an encodable event is sent", event: &exporter.FeatureEvent{Kind: kindFeature, Key: "k"},
			wantMessages: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			service := &SQSSendMessageAPIMock{}
			exp := &Exporter{QueueURL: queueX, AwsConfig: &aws.Config{}, sqsService: service}

			err := exp.Export(t.Context(), nil, []exporter.ExportableEvent{tt.event})

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.ErrorContains(t, err, tt.wantErrText)
			} else {
				require.NoError(t, err)
			}

			assert.Len(t, service.messages, tt.wantMessages)
		})
	}
}
