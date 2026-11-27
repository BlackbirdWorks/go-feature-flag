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

// sentRequest is what the fake SQS endpoint saw of one request.
type sentRequest struct {
	Target string
	KeyID  string
	Body   string
}

var credentialKey = regexp.MustCompile(`Credential=([^/]+)/`)

// TestExporter_BuildsItsClientFromTheGivenConfiguration: without an injected SQS service the exporter builds its
// client from the configuration it was given (the connection's), so the request goes to that endpoint, signed
// with that configuration's credentials, and the default chain is not loaded.
func TestExporter_BuildsItsClientFromTheGivenConfiguration(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		seen []sentRequest
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)

		keyID := ""
		if match := credentialKey.FindStringSubmatch(r.Header.Get("Authorization")); match != nil {
			keyID = match[1]
		}

		mu.Lock()

		seen = append(seen, sentRequest{Target: r.Header.Get("X-Amz-Target"), KeyID: keyID, Body: string(body)})
		mu.Unlock()

		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		_, err = w.Write([]byte(`{}`))
		assert.NoError(t, err)
	}))
	t.Cleanup(srv.Close)

	var loads int

	exp := &Exporter{
		QueueURL: "http://sqs.local/queue/own",
		AwsConfig: &aws.Config{
			Region:       regionConnection,
			BaseEndpoint: aws.String(srv.URL),
			Credentials:  credentials.NewStaticCredentialsProvider("AKIDCONNECTION", "secret", ""),
		},
		loadConfig: func(_ context.Context) (aws.Config, error) {
			loads++

			return aws.Config{}, errNoChain
		},
	}

	event := &exporter.FeatureEvent{Kind: "feature", Key: "flag-a", UserKey: "user-1", CreationDate: 1617970547}
	require.NoError(t, exp.Export(t.Context(), nil, []exporter.ExportableEvent{event}))

	assert.Equal(t, 0, loads, "the default chain is not loaded")

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, seen, 1)
	assert.Equal(t, "AmazonSQS.SendMessage", seen[0].Target)
	assert.Equal(t, "AKIDCONNECTION", seen[0].KeyID, "signed with the credentials of the given configuration")
	assert.Contains(t, seen[0].Body, `"QueueUrl":"http://sqs.local/queue/own"`)
}

func TestExporter_EncodingErrorIsReported(t *testing.T) {
	t.Parallel()

	service := &SQSSendMessageAPIMock{}
	exp := &Exporter{QueueURL: "http://sqs.local/queue/x", AwsConfig: &aws.Config{}, sqsService: service}

	err := exp.Export(t.Context(), nil, []exporter.ExportableEvent{&unencodableEvent{}})

	require.ErrorIs(t, err, errUnmarshalable)
	require.ErrorContains(t, err, "marshal SQS event")
	assert.Empty(t, service.messages, "nothing is sent")
}
