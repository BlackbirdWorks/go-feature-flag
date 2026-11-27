package kinesisexporter

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
)

// sequenceLoader returns a loader whose nth call (from 0) fails with steps[n] when it is not nil, and counts its calls.
func sequenceLoader(steps []error, loads *atomic.Int32) func(context.Context) (aws.Config, error) {
	return func(context.Context) (aws.Config, error) {
		n := int(loads.Add(1)) - 1
		if n < len(steps) && steps[n] != nil {
			return aws.Config{}, steps[n]
		}

		return aws.Config{Region: regionChain}, nil
	}
}

// TestExporter_InitialisationIsRetriedUntilItSucceeds: a first load of the configuration that fails must not make
// the later calls report success without a client.
func TestExporter_InitialisationIsRetriedUntilItSucceeds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		steps     []error
		wantErrs  []error
		wantSent  int
		wantLoads int32
		wantReady bool
	}{
		{
			name: "a failing first load is retried and then works", steps: []error{errNoChain, nil},
			wantErrs: []error{errNoChain, nil}, wantLoads: 2, wantSent: 1, wantReady: true,
		},
		{
			name: "a load that keeps failing keeps reporting the error", steps: []error{errNoChain, errNoChain},
			wantErrs: []error{errNoChain, errNoChain}, wantLoads: 2, wantSent: 0, wantReady: false,
		},
		{
			name: "a working load happens once", steps: nil,
			wantErrs: []error{nil, nil}, wantLoads: 1, wantSent: 2, wantReady: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var loads atomic.Int32

			exp, sent := newInitTestExporter(sequenceLoader(tt.steps, &loads))

			for i, wantErr := range tt.wantErrs {
				err := callInit(t, exp)

				if wantErr != nil {
					require.ErrorIs(t, err, wantErr, "call %d", i)

					continue
				}

				require.NoError(t, err, "call %d", i)
			}

			assert.Equal(t, tt.wantLoads, loads.Load())
			assert.Equal(t, tt.wantSent, sent(), "what was sent")
			assert.Equal(t, tt.wantReady, ready(exp))
		})
	}
}

// TestExporter_ConcurrentCallsInitialiseOnce: many callers at once load the configuration exactly once.
func TestExporter_ConcurrentCallsInitialiseOnce(t *testing.T) {
	t.Parallel()

	const callers = 16

	var loads atomic.Int32

	exp, sent := newInitTestExporter(sequenceLoader(nil, &loads))
	errs := make([]error, callers)

	var wg sync.WaitGroup

	for i := range callers {
		wg.Go(func() { errs[i] = callInit(t, exp) })
	}

	wg.Wait()

	assert.Equal(t, make([]error, callers), errs)
	assert.Equal(t, int32(1), loads.Load(), "one successful initialisation")
	assert.Equal(t, callers, sent())
}

// countingSender counts the records it is given.
type countingSender struct{ records atomic.Int32 }

func (c *countingSender) SendMessages(
	_ context.Context, msgs *kinesis.PutRecordsInput,
) (*kinesis.PutRecordsOutput, error) {
	c.records.Add(int32(len(msgs.Records)))

	accepted := make([]types.PutRecordsResultEntry, len(msgs.Records))
	for i := range accepted {
		accepted[i] = types.PutRecordsResultEntry{SequenceNumber: aws.String("1"), ShardId: aws.String("shard")}
	}

	return &kinesis.PutRecordsOutput{FailedRecordCount: aws.Int32(0), Records: accepted}, nil
}

func newInitTestExporter(load func(context.Context) (aws.Config, error)) (*Exporter, func() int) {
	sender := &countingSender{}
	exp := &Exporter{sender: sender, loadConfig: load, Settings: NewSettings(WithStreamName("s"))}

	return exp, func() int { return int(sender.records.Load()) }
}

func callInit(t *testing.T, exp *Exporter) error {
	t.Helper()

	return exp.Export(t.Context(), slog.New(slog.DiscardHandler), []exporter.ExportableEvent{
		&exporter.FeatureEvent{Kind: "feature", Key: "flag-a", UserKey: "user-1", CreationDate: 1617970547},
	})
}

func ready(exp *Exporter) bool {
	exp.initMu.Lock()
	defer exp.initMu.Unlock()

	return exp.initDone
}
