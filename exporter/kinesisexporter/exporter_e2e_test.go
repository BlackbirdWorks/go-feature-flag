//go:build docker

package kinesisexporter_test

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
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/exporter/kinesisexporter"
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

const (
	// waitDeadline is the deadline of a whole wait, and callTimeout bounds one call of the emulator: a call that hangs
	// is stopped and reported, and no call runs past the deadline.
	waitDeadline = 30 * time.Second
	callTimeout  = 10 * time.Second
)

func createStream(t *testing.T, client *kinesis.Client, shards int32) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), waitDeadline)
	defer cancel()

	name := gopherstack.UniqueName(t, "stream")

	callCtx, cancelCall := context.WithTimeout(ctx, callTimeout)
	_, err := client.CreateStream(callCtx, &kinesis.CreateStreamInput{
		StreamName: aws.String(name), ShardCount: aws.Int32(shards),
	})

	cancelCall()
	require.NoError(t, err)

	rep := gopherstack.Poll(ctx, callTimeout, 100*time.Millisecond, func(ctx context.Context) (bool, error) {
		out, describeStreamErr := client.DescribeStream(ctx, &kinesis.DescribeStreamInput{StreamName: aws.String(name)})
		if describeStreamErr != nil {
			return false, describeStreamErr
		}

		return out.StreamDescription.StreamStatus == types.StreamStatusActive, nil
	})
	require.True(t, rep.Done, "stream %s not ACTIVE %s", name, rep)

	return name
}

// readAll reads every shard of the stream from the beginning until want records are received. When the deadline
// passes it fails with what the polling saw: the polls and the last error of the API.
func readAll(t *testing.T, client *kinesis.Client, stream string, want int) []*exporter.FeatureEvent {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), waitDeadline)
	defer cancel()

	callCtx, cancelCall := context.WithTimeout(ctx, callTimeout)
	desc, err := client.DescribeStream(callCtx, &kinesis.DescribeStreamInput{StreamName: aws.String(stream)})

	cancelCall()
	require.NoError(t, err)

	iterators := map[string]*string{}

	for _, shard := range desc.StreamDescription.Shards {
		iterCtx, cancelIter := context.WithTimeout(ctx, callTimeout)
		iter, getShardIteratorErr := client.GetShardIterator(iterCtx, &kinesis.GetShardIteratorInput{
			StreamName:        aws.String(stream),
			ShardId:           shard.ShardId,
			ShardIteratorType: types.ShardIteratorTypeTrimHorizon,
		})

		cancelIter()
		require.NoError(t, getShardIteratorErr)

		iterators[aws.ToString(shard.ShardId)] = iter.ShardIterator
	}

	var got []*exporter.FeatureEvent

	rep := gopherstack.Poll(ctx, callTimeout, 200*time.Millisecond, func(ctx context.Context) (bool, error) {
		for id, it := range iterators {
			out, getRecordsErr := client.GetRecords(
				ctx,
				&kinesis.GetRecordsInput{ShardIterator: it, Limit: aws.Int32(10000)},
			)
			if getRecordsErr != nil {
				return false, getRecordsErr
			}

			iterators[id] = out.NextShardIterator
			for _, r := range out.Records {
				var e *exporter.FeatureEvent
				require.NoError(t, json.Unmarshal(r.Data, &e))
				got = append(got, e)
			}
		}

		return len(got) >= want, nil
	})

	// The message is built here, after the polling: it carries the real counts.
	require.GreaterOrEqual(t, len(got), want, "records read %s", rep)

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
		partitionKey kinesisexporter.PartitionKeyFunc
		name         string
		nbEvents     int
		nbCalls      int // the events are split in this number of Export calls
		wantReceived int
		shards       int32
		byArn        bool
	}{
		{name: "one event", shards: 1, nbEvents: 1, nbCalls: 1, wantReceived: 1},
		{name: "ten events in one call", shards: 1, nbEvents: 10, nbCalls: 1, wantReceived: 10},
		{name: "ten events in two calls", shards: 1, nbEvents: 10, nbCalls: 2, wantReceived: 10},
		{name: "max PutRecords size (500 records)", shards: 1, nbEvents: 500, nbCalls: 1, wantReceived: 500},
		{name: "stream addressed by ARN", shards: 1, nbEvents: 3, nbCalls: 1, byArn: true, wantReceived: 3},
		{
			name: "custom partition key spreads on two shards", shards: 2, nbEvents: 20, nbCalls: 1,
			partitionKey: func(_ context.Context, e exporter.ExportableEvent) string {
				if fe, ok := e.(*exporter.FeatureEvent); ok {
					return fe.UserKey
				}

				return ""
			},
			wantReceived: 20,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := srv.MustAwsConfig(t)
			client := kinesis.NewFromConfig(cfg)
			stream := createStream(t, client, tt.shards)

			opts := []kinesisexporter.Option{kinesisexporter.WithStreamName(stream)}
			if tt.byArn {
				desc, err := client.DescribeStream(
					t.Context(),
					&kinesis.DescribeStreamInput{StreamName: aws.String(stream)},
				)
				require.NoError(t, err)

				opts = []kinesisexporter.Option{
					kinesisexporter.WithStreamArn(aws.ToString(desc.StreamDescription.StreamARN)),
				}
			}

			if tt.partitionKey != nil {
				opts = append(opts, kinesisexporter.WithPartitionKey(tt.partitionKey))
			}

			exp := &kinesisexporter.Exporter{AwsConfig: &cfg, Settings: kinesisexporter.NewSettings(opts...)}

			all := events(tt.nbEvents)

			per := (len(all) + tt.nbCalls - 1) / tt.nbCalls
			for i := 0; i < len(all); i += per {
				require.NoError(
					t,
					exp.Export(context.Background(), slog.New(slog.DiscardHandler), all[i:min(i+per, len(all))]),
				)
			}

			got := readAll(t, client, stream, tt.nbEvents)
			require.Len(t, got, tt.wantReceived)
			slices.SortFunc(got, func(a, b *exporter.FeatureEvent) int { return cmp.Compare(a.UserKey, b.UserKey) })

			for i, g := range got {
				assert.Equal(t, fmt.Sprintf("user-%04d", i), g.UserKey)
				assert.Equal(t, "my-flag", g.Key)
				assert.Equal(t, fmt.Sprintf("value-%d", i), g.Value)
				assert.Equal(t, "enabled", g.Variation)
			}
		})
	}
}

func TestE2E_Export_Errors(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Connect(t)

	tests := []struct {
		settings func(t *testing.T) *kinesisexporter.Settings
		name     string
		wantErr  string
		nbEvents int
	}{
		{
			name: "unknown stream",
			settings: func(t *testing.T) *kinesisexporter.Settings {
				t.Helper()

				return kinesisexporter.NewSettings(
					kinesisexporter.WithStreamName(gopherstack.UniqueName(t, "nostream")),
				)
			},
			nbEvents: 1,
			wantErr:  "ResourceNotFoundException: Stream not found.",
		},
		{
			name:     "no stream name nor ARN",
			settings: func(*testing.T) *kinesisexporter.Settings { return kinesisexporter.NewSettings() },
			nbEvents: 1,
			wantErr:  "send: no StreamName or StreamArn provided",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := srv.MustAwsConfig(t)
			e := &kinesisexporter.Exporter{AwsConfig: &cfg, Settings: tt.settings(t)}
			require.ErrorContains(
				t,
				e.Export(context.Background(), slog.New(slog.DiscardHandler), events(tt.nbEvents)),
				tt.wantErr,
			)
		})
	}
}

// PutRecords accepts at most 500 records per call on AWS: the exporter splits bigger batches
// into several calls, so 501 events must all be delivered.
func TestE2E_Export_MoreThanOnePutRecordsCall(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Connect(t)
	cfg := srv.MustAwsConfig(t)
	client := kinesis.NewFromConfig(cfg)
	stream := createStream(t, client, 1)
	e := &kinesisexporter.Exporter{
		AwsConfig: &cfg,
		Settings:  kinesisexporter.NewSettings(kinesisexporter.WithStreamName(stream)),
	}
	require.NoError(t, e.Export(context.Background(), slog.New(slog.DiscardHandler), events(501)))
	assert.Len(t, readAll(t, client, stream, 501), 501)
}
