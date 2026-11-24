//go:build docker

package sqsexporter_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/exporter/sqsexporter"
	"github.com/blackbirdworks/semaphore/testutils/gopherstack"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

func createQueue(t *testing.T, client *sqs.Client) string {
	t.Helper()
	out, err := client.CreateQueue(t.Context(), &sqs.CreateQueueInput{QueueName: aws.String(gopherstack.UniqueName(t, "queue"))})
	require.NoError(t, err)

	return aws.ToString(out.QueueUrl)
}

type received struct {
	event   exporter.FeatureEvent
	emitter string
}

func receiveAll(t *testing.T, client *sqs.Client, queueURL string, want int) []received {
	t.Helper()
	var got []received
	require.Eventually(t, func() bool {
		out, err := client.ReceiveMessage(t.Context(), &sqs.ReceiveMessageInput{
			QueueUrl: aws.String(queueURL), MaxNumberOfMessages: 10, WaitTimeSeconds: 1,
			MessageAttributeNames: []string{"All"},
		})
		if err != nil {
			return false
		}
		for _, m := range out.Messages {
			var e exporter.FeatureEvent
			require.NoError(t, json.Unmarshal([]byte(aws.ToString(m.Body)), &e))
			r := received{event: e}
			if a, ok := m.MessageAttributes["emitter"]; ok {
				r.emitter = aws.ToString(a.StringValue)
			}
			got = append(got, r)
			_, err := client.DeleteMessage(t.Context(), &sqs.DeleteMessageInput{QueueUrl: aws.String(queueURL), ReceiptHandle: m.ReceiptHandle})
			require.NoError(t, err)
		}

		return len(got) >= want
	}, 30*time.Second, 100*time.Millisecond, "got %d/%d messages", len(got), want)

	return got
}

func events(n int) []exporter.ExportableEvent {
	res := make([]exporter.ExportableEvent, 0, n)
	for i := range n {
		res = append(res, exporter.FeatureEvent{
			Kind: "feature", ContextKind: "user", UserKey: fmt.Sprintf("user-%04d", i), CreationDate: int64(1617970547 + i),
			Key: "my-flag", Variation: "enabled", Value: fmt.Sprintf("value-%d", i), Source: "SERVER",
		})
	}

	return res
}

func TestE2E_Export(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Shared(t)

	tests := []struct {
		name     string
		nbEvents int
		nbCalls  int
	}{
		{name: "one event", nbEvents: 1, nbCalls: 1},
		{name: "25 events in one call (more than one receive)", nbEvents: 25, nbCalls: 1},
		{name: "25 events in five calls", nbEvents: 25, nbCalls: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := srv.MustAwsConfig(t)
			client := sqs.NewFromConfig(cfg)
			queueURL := createQueue(t, client)
			e := &sqsexporter.Exporter{QueueURL: queueURL, AwsConfig: &cfg}

			all := events(tt.nbEvents)
			per := (len(all) + tt.nbCalls - 1) / tt.nbCalls
			for i := 0; i < len(all); i += per {
				require.NoError(t, e.Export(context.Background(), &fflog.FFLogger{}, all[i:min(i+per, len(all))]))
			}

			got := receiveAll(t, client, queueURL, tt.nbEvents)
			require.Len(t, got, tt.nbEvents)
			sort.Slice(got, func(i, j int) bool { return got[i].event.UserKey < got[j].event.UserKey })
			for i, g := range got {
				assert.Equal(t, fmt.Sprintf("user-%04d", i), g.event.UserKey)
				assert.Equal(t, "my-flag", g.event.Key)
				assert.Equal(t, fmt.Sprintf("value-%d", i), g.event.Value)
				assert.Equal(t, "enabled", g.event.Variation)
				assert.Equal(t, "GO Feature Flag", g.emitter)
			}
		})
	}
}

func TestE2E_Export_Errors(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Shared(t)

	tests := []struct {
		name     string
		queueURL func(t *testing.T) string
	}{
		{name: "empty queue url", queueURL: func(*testing.T) string { return "" }},
		{
			name: "unknown queue",
			queueURL: func(t *testing.T) string {
				return srv.Endpoint + "/000000000000/" + gopherstack.UniqueName(t, "noqueue")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := srv.MustAwsConfig(t)
			e := &sqsexporter.Exporter{QueueURL: tt.queueURL(t), AwsConfig: &cfg}
			require.Error(t, e.Export(context.Background(), &fflog.FFLogger{}, events(1)))
		})
	}
}

func TestE2E_Export_FifoQueue(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Shared(t)
	cfg := srv.MustAwsConfig(t)
	client := sqs.NewFromConfig(cfg)
	out, err := client.CreateQueue(t.Context(), &sqs.CreateQueueInput{
		QueueName:  aws.String(gopherstack.UniqueName(t, "fifo") + ".fifo"),
		Attributes: map[string]string{string(sqstypes.QueueAttributeNameFifoQueue): "true"},
	})
	require.NoError(t, err)
	// The exporter sets neither MessageGroupId nor deduplication id: AWS rejects such messages on a
	// FIFO queue (MissingParameter), so FIFO queues are not supported by this exporter.
	e := &sqsexporter.Exporter{QueueURL: aws.ToString(out.QueueUrl), AwsConfig: &cfg}
	require.Error(t, e.Export(context.Background(), &fflog.FFLogger{}, events(1)))
}
