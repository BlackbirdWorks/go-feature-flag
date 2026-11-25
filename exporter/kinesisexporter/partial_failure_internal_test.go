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

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	internalFailure                        = "InternalFailure"
	provisionedThroughputExceededException = "ProvisionedThroughputExceededException"
)

// scriptedSender answers each PutRecords call using fail(callIndex, recordIndex) which returns
// the error code to reject the record with ("" means accepted).
type scriptedSender struct {
	fail  func(call, rec int, data string) string
	calls [][]string // data of the records of each call
	mu    sync.Mutex
}

func (s *scriptedSender) SendMessages(
	_ context.Context, input *kinesis.PutRecordsInput,
) (*kinesis.PutRecordsOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	call := len(s.calls)
	datas := make([]string, 0, len(input.Records))
	out := &kinesis.PutRecordsOutput{}
	failed := int32(0)

	for i, r := range input.Records {
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
		evs[i] = &exporter.FeatureEvent{Kind: fixtures.KindFeature, Key: fmt.Sprintf("flag-%d", i)}
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

	logger := slog.Default()

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
		sender := &scriptedSender{fail: func(call, _ int, data string) string {
			if call == 0 && failing[data] {
				return provisionedThroughputExceededException
			}

			return ""
		}}

		var sleeps []time.Duration

		sleep := func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)

			return nil
		}
		err := newTestExporter(sender, sleep).Export(context.Background(), logger, testEvents(4))
		require.NoError(t, err)
		assert.Equal(t, [][]string{encoded(t, 0, 1, 2, 3), encoded(t, 1, 3)}, sender.calls)
		assert.Len(t, sleeps, 1)
	})

	t.Run("persistent failure returns counts and codes", func(t *testing.T) {
		t.Parallel()

		sender := &scriptedSender{fail: func(_, i int, _ string) string {
			switch i {
			case 0:
				return internalFailure
			case 1:
				return provisionedThroughputExceededException
			}

			return ""
		}}
		err := newTestExporter(sender, noSleep).Export(context.Background(), logger, testEvents(3))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "2 of 3 records failed")
		assert.Contains(t, err.Error(), internalFailure)
		assert.Contains(t, err.Error(), provisionedThroughputExceededException)
		require.Len(t, sender.calls, maxAttempts)
		assert.Equal(t, encoded(t, 0, 1), sender.calls[1])
		assert.Equal(t, encoded(t, 0, 1), sender.calls[2])
	})

	t.Run("context cancelled during backoff", func(t *testing.T) {
		t.Parallel()

		sender := &scriptedSender{fail: func(int, int, string) string { return internalFailure }}
		ctx, cancel := context.WithCancel(context.Background())
		sleep := func(ctx context.Context, _ time.Duration) error {
			cancel()

			return ctx.Err()
		}
		err := newTestExporter(sender, sleep).Export(ctx, logger, testEvents(2))
		require.ErrorIs(t, err, context.Canceled)
		assert.Len(t, sender.calls, 1)
	})

	t.Run("real backoff honours context", func(t *testing.T) {
		t.Parallel()

		s := &scriptedSender{fail: func(int, int, string) string { return internalFailure }}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := newTestExporter(s, nil).Export(ctx, logger, testEvents(1))
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestExporter_ChunksPutRecordsCalls(t *testing.T) {
	t.Parallel()

	logger := slog.Default()

	tests := []struct {
		name   string
		want   []int
		events int
	}{
		{name: "exactly 500", events: 500, want: []int{500}},
		{name: "501", events: 501, want: []int{500, 1}},
		{name: "1200", events: 1200, want: []int{500, 500, 200}},
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

// responseSender replies to each PutRecords call with respond(callIndex, request).
type responseSender struct {
	respond func(call int, in *kinesis.PutRecordsInput) (*kinesis.PutRecordsOutput, error)
	calls   [][]string
	mu      sync.Mutex
}

func (s *responseSender) SendMessages(
	_ context.Context, input *kinesis.PutRecordsInput,
) (*kinesis.PutRecordsOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	call := len(s.calls)

	datas := make([]string, 0, len(input.Records))
	for _, r := range input.Records {
		datas = append(datas, string(r.Data))
	}

	s.calls = append(s.calls, datas)

	return s.respond(call, input)
}

func TestExporter_InconsistentPutRecordsResponses(t *testing.T) {
	t.Parallel()

	logger := slog.Default()
	ok := types.PutRecordsResultEntry{SequenceNumber: aws.String("1")}
	bad := types.PutRecordsResultEntry{ErrorCode: aws.String(internalFailure)}

	tests := []struct {
		name string
		// first is the response to the first call, later calls accept everything.
		first func(in *kinesis.PutRecordsInput) *kinesis.PutRecordsOutput
		// wantCalls are the indexes of the events sent on each call.
		wantCalls [][]int
	}{
		{
			name: "failure count omitted but entry rejected",
			first: func(*kinesis.PutRecordsInput) *kinesis.PutRecordsOutput {
				return &kinesis.PutRecordsOutput{Records: []types.PutRecordsResultEntry{ok, bad}}
			},
			wantCalls: [][]int{{0, 1}, {1}},
		},
		{
			name: "failure count zero but entry rejected",
			first: func(*kinesis.PutRecordsInput) *kinesis.PutRecordsOutput {
				return &kinesis.PutRecordsOutput{
					FailedRecordCount: aws.Int32(0),
					Records:           []types.PutRecordsResultEntry{bad, ok},
				}
			},
			wantCalls: [][]int{{0, 1}, {0}},
		},
		{
			name: "entry with neither sequence number nor error code",
			first: func(*kinesis.PutRecordsInput) *kinesis.PutRecordsOutput {
				return &kinesis.PutRecordsOutput{
					FailedRecordCount: aws.Int32(0),
					Records:           []types.PutRecordsResultEntry{ok, {}},
				}
			},
			wantCalls: [][]int{{0, 1}, {1}},
		},
		{
			name: "positive failure count but all entries accepted",
			first: func(*kinesis.PutRecordsInput) *kinesis.PutRecordsOutput {
				return &kinesis.PutRecordsOutput{
					FailedRecordCount: aws.Int32(1),
					Records:           []types.PutRecordsResultEntry{ok, ok},
				}
			},
			wantCalls: [][]int{{0, 1}},
		},
		{
			name: "fewer results than records resends everything",
			first: func(*kinesis.PutRecordsInput) *kinesis.PutRecordsOutput {
				return &kinesis.PutRecordsOutput{
					FailedRecordCount: aws.Int32(0),
					Records:           []types.PutRecordsResultEntry{ok},
				}
			},
			wantCalls: [][]int{{0, 1}, {0, 1}},
		},
		{
			name: "more results than records resends everything",
			first: func(*kinesis.PutRecordsInput) *kinesis.PutRecordsOutput {
				return &kinesis.PutRecordsOutput{
					FailedRecordCount: aws.Int32(0),
					Records:           []types.PutRecordsResultEntry{ok, ok, ok},
				}
			},
			wantCalls: [][]int{{0, 1}, {0, 1}},
		},
		{
			name: "no results at all resends everything",
			first: func(*kinesis.PutRecordsInput) *kinesis.PutRecordsOutput {
				return &kinesis.PutRecordsOutput{}
			},
			wantCalls: [][]int{{0, 1}, {0, 1}},
		},
		{
			name:      "nil output resends everything",
			first:     func(*kinesis.PutRecordsInput) *kinesis.PutRecordsOutput { return nil },
			wantCalls: [][]int{{0, 1}, {0, 1}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sender := &responseSender{
				respond: func(call int, req *kinesis.PutRecordsInput) (*kinesis.PutRecordsOutput, error) {
					if call == 0 {
						return tt.first(req), nil
					}

					out := &kinesis.PutRecordsOutput{FailedRecordCount: aws.Int32(0)}
					for range req.Records {
						out.Records = append(out.Records, ok)
					}

					return out, nil
				},
			}
			err := newTestExporter(sender, noSleep).Export(context.Background(), logger, testEvents(2))
			require.NoError(t, err)

			want := make([][]string, 0, len(tt.wantCalls))
			for _, idx := range tt.wantCalls {
				want = append(want, encoded(t, idx...))
			}

			assert.Equal(t, want, sender.calls)
		})
	}
}

func TestExporter_UnverifiableResponsesNeverAcknowledged(t *testing.T) {
	t.Parallel()

	logger := slog.Default()
	s := &responseSender{respond: func(int, *kinesis.PutRecordsInput) (*kinesis.PutRecordsOutput, error) {
		return nil, nil //nolint:nilnil // the test feeds the exporter an unverifiable (nil) response on purpose
	}}
	err := newTestExporter(s, noSleep).Export(context.Background(), logger, testEvents(2))
	require.Error(t, err)
	assert.Len(t, s.calls, maxAttempts)
}
