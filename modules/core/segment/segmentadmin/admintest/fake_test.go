package admintest_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin/admintest"
)

const (
	member1 = "k:1"
	member2 = "k:2"
	live    = "p:{s}"
	tmp     = "p:{s}:import:t:new"
	old     = "p:{s}:import:t:old"
	lock    = "p:{s}:import:lock"
	last    = "p:{s}:import:last"
	token   = "t"
)

func commitOf(want int64) *segmentadmin.Commit {
	return &segmentadmin.Commit{
		Lock: lock, Tmp: tmp, Live: live, Old: old, Last: last, Token: token, Want: want, OldTTL: time.Minute,
	}
}

func TestFake_commit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr    error
		prepare    func(ctx context.Context, store *admintest.Fake)
		commit     *segmentadmin.Commit
		name       string
		wantLive   []string
		wantOld    []string
		wantResult segmentadmin.CommitResult
	}{
		{
			name: "swaps and keeps the old set",
			prepare: func(ctx context.Context, store *admintest.Fake) {
				_, _ = store.SAdd(ctx, live, []string{"a"}, 0)
				_, _ = store.Lock(ctx, lock, token, time.Hour)
				_, _, _ = store.StageAdd(ctx, lock, tmp, token, []string{"b", "c"}, time.Hour)
			},
			commit: commitOf(2), wantLive: []string{"b", "c"}, wantOld: []string{"a"},
		},
		{
			name: "refuses a lock that is not the run's",
			prepare: func(ctx context.Context, store *admintest.Fake) {
				_, _ = store.SAdd(ctx, live, []string{"a"}, 0)
				_, _ = store.Lock(ctx, lock, "other", time.Hour)
			},
			commit: commitOf(1), wantResult: segmentadmin.LockLost, wantLive: []string{"a"},
		},
		{
			name: "refuses a missing temporary key",
			prepare: func(ctx context.Context, store *admintest.Fake) {
				_, _ = store.SAdd(ctx, live, []string{"a"}, 0)
				_, _ = store.Lock(ctx, lock, token, time.Hour)
			},
			commit: commitOf(1), wantResult: segmentadmin.TempGone, wantLive: []string{"a"},
		},
		{
			name: "refuses another cardinality",
			prepare: func(ctx context.Context, store *admintest.Fake) {
				_, _ = store.SAdd(ctx, live, []string{"a"}, 0)
				_, _ = store.Lock(ctx, lock, token, time.Hour)
				_, _, _ = store.StageAdd(ctx, lock, tmp, token, []string{"b"}, time.Hour)
			},
			commit: commitOf(2), wantResult: segmentadmin.CountMismatch, wantLive: []string{"a"},
		},
		{
			name: "an empty import removes the live set",
			prepare: func(ctx context.Context, store *admintest.Fake) {
				_, _ = store.SAdd(ctx, live, []string{"a"}, 0)
				_, _ = store.Lock(ctx, lock, token, time.Hour)
			},
			commit: commitOf(0),
		},
		{
			name:    "keys of two slots are refused as a cluster refuses them",
			prepare: func(context.Context, *admintest.Fake) {},
			commit: &segmentadmin.Commit{
				Lock: lock, Tmp: "elsewhere", Live: live, Old: old, Token: token, Want: 1,
			},
			wantResult: segmentadmin.LockLost, wantErr: admintest.ErrCrossSlot,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := admintest.New()
			tt.prepare(t.Context(), store)

			result, _, err := store.Commit(t.Context(), tt.commit)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantResult, result)
			assert.ElementsMatch(t, tt.wantLive, store.Members(live))
			assert.ElementsMatch(t, tt.wantOld, store.Members(old))
		})
	}
}

func TestFake_lock(t *testing.T) {
	t.Parallel()

	store := admintest.New()
	ctx := t.Context()

	first, err := store.Lock(ctx, lock, token, time.Hour)
	require.NoError(t, err)

	second, err := store.Lock(ctx, lock, "other", time.Hour)
	require.NoError(t, err)

	require.NoError(t, store.SetValue(ctx, last, tmp))
	require.NoError(t, store.Unlock(ctx, lock, last, "other"))

	holder, held, err := store.Value(ctx, lock)
	require.NoError(t, err)

	require.NoError(t, store.Unlock(ctx, lock, last, token))

	_, afterLock, _ := store.Value(ctx, lock)
	_, afterLast, _ := store.Value(ctx, last)

	assert.Equal(t, []any{true, false, token, true, false, false},
		[]any{first, second, holder, held, afterLock, afterLast})
}

func TestFake_stageAdd(t *testing.T) {
	t.Parallel()

	store := admintest.New()
	ctx := t.Context()

	_, _ = store.Lock(ctx, lock, token, time.Hour)

	added, held, err := store.StageAdd(ctx, lock, tmp, token, []string{"a", "a", "b"}, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, []any{int64(2), true, time.Minute}, []any{added, held, store.TTLOf(tmp)})

	added, held, err = store.StageAdd(ctx, lock, tmp, "other", []string{"c"}, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, []any{int64(0), false, []string{"a", "b"}}, []any{added, held, store.Members(tmp)})
}

func TestFake_commands(t *testing.T) {
	t.Parallel()

	store := admintest.New()
	ctx := t.Context()

	require.NoError(t, store.Ping(ctx))

	added, err := store.SAdd(ctx, live, []string{"a", "b", "c"}, time.Hour)
	require.NoError(t, err)

	removed, err := store.SRem(ctx, live, []string{"a", "zz"})
	require.NoError(t, err)

	created, err := store.SetKeys(ctx, []string{member1, member2, member1})
	require.NoError(t, err)

	counted, more, err := store.CountKeys(ctx, "k:", 1)
	require.NoError(t, err)

	card, err := store.SCard(ctx, live)
	require.NoError(t, err)

	exists, ttl, err := store.KeyTTL(ctx, live)
	require.NoError(t, err)

	gone, _, err := store.Unlink(ctx, live, "missing")
	require.NoError(t, err)

	assert.Equal(t, []any{int64(3), int64(1), int64(2), 1, true, int64(2), true, time.Hour, int64(1)},
		[]any{added, removed, created, counted, more, card, exists, ttl, gone})
	assert.Equal(t, []string{member1, member2}, store.Keys())
	assert.Contains(t, store.Calls(), "PING ")

	store.Delete(member1)
	store.Close()
	assert.Equal(t, []string{member2}, store.Keys())
}

func TestFake_failures(t *testing.T) {
	t.Parallel()

	store := admintest.New()
	store.Fail("SADD", 2)

	_, err := store.SAdd(t.Context(), live, []string{"a"}, 0)
	require.NoError(t, err)

	_, err = store.SAdd(t.Context(), live, []string{"b"}, 0)
	require.ErrorIs(t, err, admintest.ErrInjected)
	require.ErrorIs(t, err, segmentadmin.ErrStore)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, store.Ping(ctx), context.Canceled)

	store.WithoutUnlink()

	_, fell, err := store.Unlink(t.Context(), live)
	require.NoError(t, err)
	assert.True(t, fell)
}
