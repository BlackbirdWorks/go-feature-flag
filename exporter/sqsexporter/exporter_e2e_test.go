//go:build docker

package sqsexporter_test

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/exporter/sqsexporter"
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

func createQueue(t *testing.T, client *sqs.Client) string {
	t.Helper()
	out, err := client.CreateQueue(
		t.Context(),
		&sqs.CreateQueueInput{QueueName: aws.String(gopherstack.UniqueName(t, "queue"))},
	)
	require.NoError(t, err)

	return aws.ToString(out.QueueUrl)
}

type received struct {
	event   *exporter.FeatureEvent
	emitter string
}

// pollTimeout bounds one ReceiveMessage call (long poll of one second): a call that hangs is an error of the
// emulator that the test reports, not a silent wait for the whole deadline.
const pollTimeout = 10 * time.Second

// receiveAll polls the queue until want messages were received and deleted, and fails with what it saw (polls, empty
// polls, the last error of the API) when the deadline passes.
func receiveAll(t *testing.T, client *sqs.Client, queueURL string, want int) []received {
	t.Helper()

	var (
		got      []received
		polls    int
		empty    int
		lastErr  error
		deadline = time.Now().Add(30 * time.Second)
	)

	for len(got) < want && time.Now().Before(deadline) {
		polls++

		ctx, cancel := context.WithTimeout(t.Context(), pollTimeout)
		out, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl: aws.String(queueURL), MaxNumberOfMessages: 10, WaitTimeSeconds: 1,
			MessageAttributeNames: []string{"All"},
		})

		cancel()

		if err != nil {
			lastErr = err

			continue
		}

		if len(out.Messages) == 0 {
			empty++
		}

		for _, msg := range out.Messages {
			var e *exporter.FeatureEvent
			require.NoError(t, json.Unmarshal([]byte(aws.ToString(msg.Body)), &e))

			r := received{event: e}
			if a, ok := msg.MessageAttributes["emitter"]; ok {
				r.emitter = aws.ToString(a.StringValue)
			}

			got = append(got, r)
			_, deleteMessageErr := client.DeleteMessage(
				t.Context(),
				&sqs.DeleteMessageInput{QueueUrl: aws.String(queueURL), ReceiptHandle: msg.ReceiptHandle},
			)
			require.NoError(t, deleteMessageErr)
		}
	}

	require.Len(t, got, want, "after %d polls (%d empty), last error: %v", polls, empty, lastErr)

	return got
}

func events(n int) []exporter.ExportableEvent {
	res := make([]exporter.ExportableEvent, 0, n)
	for i := range n {
		res = append(res, &exporter.FeatureEvent{
			Kind:         "feature",
			ContextKind:  "user",
			UserKey:      fmt.Sprintf("user-%04d", i),
			CreationDate: int64(1617970547 + i),
			Key:          "my-flag",
			Variation:    "enabled",
			Value:        fmt.Sprintf("value-%d", i),
			Source:       "SERVER",
		})
	}

	return res
}

func TestE2E_Export(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Connect(t)

	tests := []struct {
		name         string
		nbEvents     int
		nbCalls      int
		wantReceived int
	}{
		{name: "one event", nbEvents: 1, nbCalls: 1, wantReceived: 1},
		{name: "25 events in one call (more than one receive)", nbEvents: 25, nbCalls: 1, wantReceived: 25},
		{name: "25 events in five calls", nbEvents: 25, nbCalls: 5, wantReceived: 25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := srv.MustAwsConfig(t)
			client := sqs.NewFromConfig(cfg)
			queueURL := createQueue(t, client)
			exp := &sqsexporter.Exporter{QueueURL: queueURL, AwsConfig: &cfg}

			all := events(tt.nbEvents)

			per := (len(all) + tt.nbCalls - 1) / tt.nbCalls
			for i := 0; i < len(all); i += per {
				require.NoError(
					t,
					exp.Export(context.Background(), slog.New(slog.DiscardHandler), all[i:min(i+per, len(all))]),
				)
			}

			got := receiveAll(t, client, queueURL, tt.nbEvents)
			require.Len(t, got, tt.wantReceived)
			slices.SortFunc(got, func(a, b received) int { return cmp.Compare(a.event.UserKey, b.event.UserKey) })

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
	srv := gopherstack.Connect(t)

	tests := []struct {
		name     string
		queueURL func(t *testing.T) string
		wantErr  string
	}{
		{
			name:     "empty queue url",
			queueURL: func(*testing.T) string { return "" },
			wantErr:  "impossible to init SQS exporter: QueueURL is a mandatory parameter",
		},
		{
			name: "unknown queue",
			queueURL: func(t *testing.T) string {
				t.Helper()

				return srv.Endpoint + "/000000000000/" + gopherstack.UniqueName(t, "noqueue")
			},
			wantErr: "AWS.SimpleQueueService.NonExistentQueue: The specified queue does not exist.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := srv.MustAwsConfig(t)
			e := &sqsexporter.Exporter{QueueURL: tt.queueURL(t), AwsConfig: &cfg}
			require.ErrorContains(
				t,
				e.Export(context.Background(), slog.New(slog.DiscardHandler), events(1)),
				tt.wantErr,
			)
		})
	}
}

func TestE2E_Export_FifoQueue(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Connect(t)
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
	require.ErrorContains(t, e.Export(context.Background(), slog.New(slog.DiscardHandler), events(1)),
		"InvalidParameterValue: The request must contain the parameter MessageGroupId.")
}
