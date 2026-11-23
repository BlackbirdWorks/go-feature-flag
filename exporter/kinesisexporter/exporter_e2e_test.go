//go:build docker

package kinesisexporter_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thomaspoignant/go-feature-flag/exporter"
	"github.com/thomaspoignant/go-feature-flag/exporter/kinesisexporter"
	"github.com/thomaspoignant/go-feature-flag/testutils/gopherstack"
	"github.com/thomaspoignant/go-feature-flag/utils/fflog"
)

func createStream(t *testing.T, client *kinesis.Client, shards int32) string {
	t.Helper()
	name := gopherstack.UniqueName(t, "stream")
	_, err := client.CreateStream(t.Context(), &kinesis.CreateStreamInput{
		StreamName: aws.String(name), ShardCount: aws.Int32(shards),
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		out, err := client.DescribeStream(t.Context(), &kinesis.DescribeStreamInput{StreamName: aws.String(name)})
		return err == nil && out.StreamDescription.StreamStatus == types.StreamStatusActive
	}, 30*time.Second, 100*time.Millisecond, "stream not ACTIVE")
	return name
}

// readAll reads every shard of the stream from the beginning until want records are received.
func readAll(t *testing.T, client *kinesis.Client, stream string, want int) []exporter.FeatureEvent {
	t.Helper()
	desc, err := client.DescribeStream(t.Context(), &kinesis.DescribeStreamInput{StreamName: aws.String(stream)})
	require.NoError(t, err)
	iterators := map[string]*string{}
	for _, s := range desc.StreamDescription.Shards {
		it, err := client.GetShardIterator(t.Context(), &kinesis.GetShardIteratorInput{
			StreamName: aws.String(stream), ShardId: s.ShardId, ShardIteratorType: types.ShardIteratorTypeTrimHorizon,
		})
		require.NoError(t, err)
		iterators[aws.ToString(s.ShardId)] = it.ShardIterator
	}
	var got []exporter.FeatureEvent
	require.Eventually(t, func() bool {
		for id, it := range iterators {
			out, err := client.GetRecords(t.Context(), &kinesis.GetRecordsInput{ShardIterator: it, Limit: aws.Int32(10000)})
			if err != nil {
				return false
			}
			iterators[id] = out.NextShardIterator
			for _, r := range out.Records {
				var e exporter.FeatureEvent
				require.NoError(t, json.Unmarshal(r.Data, &e))
				got = append(got, e)
			}
		}
		return len(got) >= want
	}, 30*time.Second, 200*time.Millisecond, "got %d/%d records", len(got), want)
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
		name         string
		shards       int32
		nbEvents     int
		nbCalls      int // the events are split in this number of Export calls
		byArn        bool
		partitionKey kinesisexporter.PartitionKeyFunc
	}{
		{name: "one event", shards: 1, nbEvents: 1, nbCalls: 1},
		{name: "ten events in one call", shards: 1, nbEvents: 10, nbCalls: 1},
		{name: "ten events in two calls", shards: 1, nbEvents: 10, nbCalls: 2},
		{name: "max PutRecords size (500 records)", shards: 1, nbEvents: 500, nbCalls: 1},
		{name: "stream addressed by ARN", shards: 1, nbEvents: 3, nbCalls: 1, byArn: true},
		{
			name: "custom partition key spreads on two shards", shards: 2, nbEvents: 20, nbCalls: 1,
			partitionKey: func(_ context.Context, e exporter.ExportableEvent) string {
				return e.(exporter.FeatureEvent).UserKey
			},
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
				desc, err := client.DescribeStream(t.Context(), &kinesis.DescribeStreamInput{StreamName: aws.String(stream)})
				require.NoError(t, err)
				opts = []kinesisexporter.Option{kinesisexporter.WithStreamArn(aws.ToString(desc.StreamDescription.StreamARN))}
			}
			if tt.partitionKey != nil {
				opts = append(opts, kinesisexporter.WithPartitionKey(tt.partitionKey))
			}
			e := &kinesisexporter.Exporter{AwsConfig: &cfg, Settings: kinesisexporter.NewSettings(opts...)}

			all := events(tt.nbEvents)
			per := (len(all) + tt.nbCalls - 1) / tt.nbCalls
			for i := 0; i < len(all); i += per {
				require.NoError(t, e.Export(context.Background(), &fflog.FFLogger{}, all[i:min(i+per, len(all))]))
			}

			got := readAll(t, client, stream, tt.nbEvents)
			require.Len(t, got, tt.nbEvents)
			sort.Slice(got, func(i, j int) bool { return got[i].UserKey < got[j].UserKey })
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
	srv := gopherstack.Shared(t)

	tests := []struct {
		name     string
		settings func(t *testing.T) *kinesisexporter.Settings
		nbEvents int
	}{
		{
			name: "unknown stream",
			settings: func(t *testing.T) *kinesisexporter.Settings {
				return kinesisexporter.NewSettings(kinesisexporter.WithStreamName(gopherstack.UniqueName(t, "nostream")))
			},
			nbEvents: 1,
		},
		{
			name:     "no stream name nor ARN",
			settings: func(*testing.T) *kinesisexporter.Settings { return kinesisexporter.NewSettings() },
			nbEvents: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := srv.MustAwsConfig(t)
			e := &kinesisexporter.Exporter{AwsConfig: &cfg, Settings: tt.settings(t)}
			require.Error(t, e.Export(context.Background(), &fflog.FFLogger{}, events(tt.nbEvents)))
		})
	}
}

// PutRecords accepts at most 500 records per call on AWS and the exporter sends everything it
// receives in one call: bigger batches are the caller's responsibility (the exporter is not bulk,
// so the library flushes events one by one).
func TestE2E_Export_MoreThanOnePutRecordsCall(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Shared(t)
	cfg := srv.MustAwsConfig(t)
	client := kinesis.NewFromConfig(cfg)
	stream := createStream(t, client, 1)
	e := &kinesisexporter.Exporter{AwsConfig: &cfg, Settings: kinesisexporter.NewSettings(kinesisexporter.WithStreamName(stream))}
	err := e.Export(context.Background(), &fflog.FFLogger{}, events(501))
	if err == nil {
		t.Skip("emulator accepted 501 records in one PutRecords call, AWS rejects it (limit is 500)")
	}
	assert.Contains(t, err.Error(), "send:")
}
