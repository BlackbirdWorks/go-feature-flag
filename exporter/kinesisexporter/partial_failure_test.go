package kinesisexporter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thomaspoignant/go-feature-flag/exporter"
	"github.com/thomaspoignant/go-feature-flag/utils/fflog"
)

// scriptedSender answers each PutRecords call using fail(callIndex, recordIndex) which returns
// the error code to reject the record with ("" means accepted).
type scriptedSender struct {
	mu    sync.Mutex
	calls [][]string // data of the records of each call
	fail  func(call, rec int, data string) string
}

func (s *scriptedSender) SendMessages(
	_ context.Context, in *kinesis.PutRecordsInput,
) (*kinesis.PutRecordsOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	call := len(s.calls)
	datas := make([]string, 0, len(in.Records))
	out := &kinesis.PutRecordsOutput{}
	failed := int32(0)
	for i, r := range in.Records {
		datas = append(datas, string(r.Data))
		res := types.PutRecordsResultEntry{SequenceNumber: aws.String("1")}
		if code := s.fail(call, i, string(r.Data)); code != "" {
			res = types.PutRecordsResultEntry{
				ErrorCode: aws.String(code), ErrorMessage: aws.String("rejected"),
			}
			failed++
		}
		out.Records = append(out.Records, res)
	}
	out.FailedRecordCount = &failed
	s.calls = append(s.calls, datas)
	return out, nil
}

func testEvents(n int) []exporter.ExportableEvent {
	evs := make([]exporter.ExportableEvent, n)
	for i := range evs {
		evs[i] = &exporter.FeatureEvent{Kind: "feature", Key: fmt.Sprintf("flag-%d", i)}
	}
	return evs
}

func encoded(t *testing.T, idx ...int) []string {
	t.Helper()
	out := make([]string, 0, len(idx))
	for _, i := range idx {
		b, err := json.Marshal(testEvents(i + 1)[i])
		require.NoError(t, err)
		out = append(out, string(b))
	}
	return out
}

func newTestExporter(s MessageSender, sleep func(context.Context, time.Duration) error) *Exporter {
	return &Exporter{
		sender:   s,
		sleep:    sleep,
		Settings: NewSettings(WithStreamName("s")),
	}
}

func noSleep(context.Context, time.Duration) error { return nil }

func TestExporter_PutRecordsFailures(t *testing.T) {
	t.Parallel()
	logger := &fflog.FFLogger{LeveledLogger: slog.Default()}

	t.Run("all succeed", func(t *testing.T) {
		t.Parallel()
		s := &scriptedSender{fail: func(int, int, string) string { return "" }}
		err := newTestExporter(s, noSleep).Export(context.Background(), logger, testEvents(3))
		require.NoError(t, err)
		assert.Equal(t, [][]string{encoded(t, 0, 1, 2)}, s.calls)
	})

	t.Run("empty input", func(t *testing.T) {
		t.Parallel()
		s := &scriptedSender{fail: func(int, int, string) string { return "" }}
		require.NoError(t, newTestExporter(s, noSleep).Export(context.Background(), logger, nil))
		assert.Empty(t, s.calls)
	})

	t.Run("partial failure then success resends only failed records once", func(t *testing.T) {
		t.Parallel()
		failing := map[string]bool{encoded(t, 1)[0]: true, encoded(t, 3)[0]: true}
		s := &scriptedSender{fail: func(call, _ int, data string) string {
			if call == 0 && failing[data] {
				return "ProvisionedThroughputExceededException"
			}
			return ""
		}}
		var sleeps []time.Duration
		sleep := func(_ context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil }
		err := newTestExporter(s, sleep).Export(context.Background(), logger, testEvents(4))
		require.NoError(t, err)
		assert.Equal(t, [][]string{encoded(t, 0, 1, 2, 3), encoded(t, 1, 3)}, s.calls)
		assert.Len(t, sleeps, 1)
	})

	t.Run("persistent failure returns counts and codes", func(t *testing.T) {
		t.Parallel()
		s := &scriptedSender{fail: func(_, i int, _ string) string {
			switch i {
			case 0:
				return "InternalFailure"
			case 1:
				return "ProvisionedThroughputExceededException"
			}
			return ""
		}}
		err := newTestExporter(s, noSleep).Export(context.Background(), logger, testEvents(3))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "2 of 3 records failed")
		assert.Contains(t, err.Error(), "InternalFailure")
		assert.Contains(t, err.Error(), "ProvisionedThroughputExceededException")
		require.Len(t, s.calls, maxAttempts)
		assert.Equal(t, encoded(t, 0, 1), s.calls[1])
		assert.Equal(t, encoded(t, 0, 1), s.calls[2])
	})

	t.Run("context cancelled during backoff", func(t *testing.T) {
		t.Parallel()
		s := &scriptedSender{fail: func(int, int, string) string { return "InternalFailure" }}
		ctx, cancel := context.WithCancel(context.Background())
		sleep := func(ctx context.Context, _ time.Duration) error {
			cancel()
			return ctx.Err()
		}
		err := newTestExporter(s, sleep).Export(ctx, logger, testEvents(2))
		require.ErrorIs(t, err, context.Canceled)
		assert.Len(t, s.calls, 1)
	})

	t.Run("real backoff honours context", func(t *testing.T) {
		t.Parallel()
		s := &scriptedSender{fail: func(int, int, string) string { return "InternalFailure" }}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := newTestExporter(s, nil).Export(ctx, logger, testEvents(1))
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestExporter_ChunksPutRecordsCalls(t *testing.T) {
	t.Parallel()
	logger := &fflog.FFLogger{LeveledLogger: slog.Default()}
	tests := []struct {
		name   string
		events int
		want   []int
	}{
		{"exactly 500", 500, []int{500}},
		{"501", 501, []int{500, 1}},
		{"1200", 1200, []int{500, 500, 200}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &scriptedSender{fail: func(int, int, string) string { return "" }}
			require.NoError(t, newTestExporter(s, noSleep).Export(context.Background(), logger, testEvents(tt.events)))
			got := make([]int, 0, len(s.calls))
			for _, c := range s.calls {
				got = append(got, len(c))
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestChunkRecords_ByteLimit(t *testing.T) {
	t.Parallel()
	big := make([]byte, 900*1024)
	key := "k"
	recs := make([]types.PutRecordsRequestEntry, 0, 7)
	for range 7 {
		recs = append(recs, types.PutRecordsRequestEntry{Data: big, PartitionKey: &key})
	}
	chunks := chunkRecords(recs)
	require.Len(t, chunks, 2)
	assert.Len(t, chunks[0], 5)
	assert.Len(t, chunks[1], 2)
}
