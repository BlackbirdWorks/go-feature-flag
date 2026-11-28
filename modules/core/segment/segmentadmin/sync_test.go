package segmentadmin_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin/admintest"
)

func syncSource(objs *admintest.Objects) *segmentadmin.ObjectSource {
	return segmentadmin.NewObjectSource(objs, segmentadmin.ObjectRef{Bucket: bucket, Key: dirMember, Prefix: true}, 0)
}

func countOf(calls []string, want string) int {
	total := 0

	for _, call := range calls {
		if strings.HasPrefix(call, want) {
			total++
		}
	}

	return total
}

// TestSyncRound runs rounds of a sync in sequence: each row is a scenario and the outcomes of its rounds.
func TestSyncRound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		prepare      func(ctx context.Context, store *admintest.Fake, objs *admintest.Objects) context.Context
		between      func(store *admintest.Fake, objs *admintest.Objects)
		name         string
		wantOutcomes []segmentadmin.Outcome
		wantLive     []string
		wantSwaps    int
		wantFailed   bool
	}{
		{
			name:         "the first run imports, the second finds the source unchanged",
			wantOutcomes: []segmentadmin.Outcome{segmentadmin.OutcomeChanged, segmentadmin.OutcomeUnchanged},
			wantLive:     []string{"a1", "b1"}, wantSwaps: 1,
		},
		{
			name: "a changed object is imported once",
			between: func(_ *admintest.Fake, objs *admintest.Objects) {
				objs.Put(keyB, []byte("b2\n"), false)
			},
			wantOutcomes: []segmentadmin.Outcome{
				segmentadmin.OutcomeChanged, segmentadmin.OutcomeChanged, segmentadmin.OutcomeUnchanged,
			},
			wantLive: []string{"a1", "b2"}, wantSwaps: 2,
		},
		{
			name: "a source that equals the stored fingerprint is not imported",
			prepare: func(ctx context.Context, store *admintest.Fake, objs *admintest.Objects) context.Context {
				fp, err := syncSource(objs).Fingerprint(ctx)
				if err != nil {
					return ctx
				}

				store.Put(sourceKey, fp)

				return ctx
			},
			wantOutcomes: []segmentadmin.Outcome{segmentadmin.OutcomeUnchanged},
			wantLive:     []string{memberJunk},
		},
		{
			name: "a set that another import replaced is imported again",
			between: func(store *admintest.Fake, _ *admintest.Objects) {
				_, err := segmentadmin.Import(context.Background(), store, vipTarget(),
					segmentadmin.NewSliceSource([]string{"z"}), segmentadmin.ImportOptions{})
				if err != nil {
					store.Put(sourceKey, "import failed")
				}
			},
			wantOutcomes: []segmentadmin.Outcome{segmentadmin.OutcomeChanged, segmentadmin.OutcomeChanged},
			wantLive:     []string{"a1", "b1"}, wantSwaps: 3,
		},
		{
			name: "another import holds the lock",
			prepare: func(ctx context.Context, store *admintest.Fake, _ *admintest.Objects) context.Context {
				store.Put(lockKey, otherToken)

				return ctx
			},
			wantOutcomes: []segmentadmin.Outcome{segmentadmin.OutcomeSkipped},
			wantLive:     []string{memberJunk},
		},
		{
			name: "a failed read keeps the previous set",
			prepare: func(ctx context.Context, _ *admintest.Fake, objs *admintest.Objects) context.Context {
				objs.BreakAfter(keyB, 1)

				return ctx
			},
			wantOutcomes: []segmentadmin.Outcome{segmentadmin.OutcomeFailed},
			wantLive:     []string{memberJunk}, wantFailed: true,
		},
		{
			name: "a failed listing",
			prepare: func(ctx context.Context, _ *admintest.Fake, objs *admintest.Objects) context.Context {
				objs.FailHeads(1)

				return ctx
			},
			wantOutcomes: []segmentadmin.Outcome{segmentadmin.OutcomeFailed},
			wantLive:     []string{memberJunk}, wantFailed: true,
		},
		{
			name: "a failed read of the fingerprint",
			prepare: func(ctx context.Context, store *admintest.Fake, _ *admintest.Objects) context.Context {
				store.Fail("GET", 1)

				return ctx
			},
			wantOutcomes: []segmentadmin.Outcome{segmentadmin.OutcomeFailed},
			wantLive:     []string{memberJunk}, wantFailed: true,
		},
		{
			name: "a cancel during the import stops before the swap",
			prepare: func(ctx context.Context, _ *admintest.Fake, objs *admintest.Objects) context.Context {
				stop, cancel := context.WithCancel(ctx)

				objs.OnGet(func(key string) {
					if key == keyB {
						cancel()
					}
				})

				return stop
			},
			wantOutcomes: []segmentadmin.Outcome{segmentadmin.OutcomeFailed},
			wantLive:     []string{memberJunk}, wantFailed: true,
		},
		{
			name: "a lost reply of the swap still records the fingerprint once",
			prepare: func(ctx context.Context, store *admintest.Fake, _ *admintest.Objects) context.Context {
				store.LoseReply("SWAP", 1, false)

				return ctx
			},
			wantOutcomes: []segmentadmin.Outcome{segmentadmin.OutcomeChanged, segmentadmin.OutcomeUnchanged},
			wantLive:     []string{"a1", "b1"}, wantSwaps: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, objs := admintest.New(), admintest.NewObjects(1)
			_, err := store.SAdd(t.Context(), liveKey, []string{memberJunk}, 0)
			require.NoError(t, err)

			objs.Put(keyA, []byte("a1\n"), false)
			objs.Put(keyB, []byte("b1\n"), false)

			ctx := t.Context()
			if tt.prepare != nil {
				ctx = tt.prepare(ctx, store, objs)
			}

			var logs bytes.Buffer

			opts := &segmentadmin.SyncOptions{Log: slog.New(slog.NewTextHandler(&logs, nil))}

			var got []segmentadmin.Outcome

			for i := range tt.wantOutcomes {
				if i == 1 && tt.between != nil {
					tt.between(store, objs)
				}

				before := len(store.Calls())
				round := segmentadmin.SyncRound(ctx, store, vipTarget(), syncSource(objs), opts)
				got = append(got, round.Outcome)

				assert.Equal(t, round.Outcome == segmentadmin.OutcomeFailed, round.Err != nil)

				if round.Outcome == segmentadmin.OutcomeUnchanged {
					assert.Equal(
						t,
						[]string{"GET " + sourceKey},
						store.Calls()[before:],
						"an unchanged round writes nothing",
					)
				}
			}

			assert.Equal(t, tt.wantOutcomes, got)
			assert.Equal(t, tt.wantLive, store.Members(liveKey))
			assert.Equal(t, tt.wantSwaps, countOf(store.Calls(), "SWAP"))
			assert.Equal(t, tt.wantFailed, strings.Contains(logs.String(), "level=WARN"))
			assert.Equal(t, len(tt.wantOutcomes), strings.Count(logs.String(), "segment sync round"))
			assert.NotContains(t, logs.String(), "a1")
		})
	}
}

// TestSync_backoff checks the pauses of the loop: the interval, doubled for each failure in a row up to a cap, and the
// interval again after a good round.
func TestSync_backoff(t *testing.T) {
	t.Parallel()

	store, objs := admintest.New(), admintest.NewObjects(1)
	objs.Put(keyA, []byte("a1\n"), false)
	objs.BreakAfter(keyA, 1)

	ctx, cancel := context.WithCancel(t.Context())

	var delays []time.Duration

	opts := &segmentadmin.SyncOptions{
		Interval: time.Minute,
		Wait: func(ctx context.Context, delay time.Duration) error {
			delays = append(delays, delay)

			switch len(delays) {
			case 7:
				objs.BreakAfter(keyA, 1<<20) // the source recovers
			case 9:
				cancel()
			}

			return ctx.Err()
		},
	}

	segmentadmin.Sync(ctx, store, vipTarget(), syncSource(objs), opts)

	m := time.Minute
	assert.Equal(t, []time.Duration{m, 2 * m, 4 * m, 8 * m, 16 * m, 16 * m, 16 * m, m, m}, delays)
	assert.Equal(t, []string{"a1"}, store.Members(liveKey))
}
