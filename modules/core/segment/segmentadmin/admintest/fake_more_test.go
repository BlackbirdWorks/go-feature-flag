package admintest_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin/admintest"
)

func TestFake_editSet(t *testing.T) {
	t.Parallel()

	store := admintest.New()
	ctx := t.Context()

	added, locked, err := store.EditSet(ctx, lock, live, []string{"a", "b", "a"}, false)
	require.NoError(t, err)

	removed, _, err := store.EditSet(ctx, lock, live, []string{"a", "zz"}, true)
	require.NoError(t, err)

	_, _, err = store.EditSet(ctx, lock, live, []string{"b"}, true) // empties the set: the key goes
	require.NoError(t, err)

	store.Put(lock, token)

	_, refused, err := store.EditSet(ctx, lock, live, []string{"c"}, false)
	require.NoError(t, err)

	assert.Equal(t, []any{int64(2), false, int64(1), true, []string(nil)},
		[]any{added, locked, removed, refused, nonEmpty(store.Members(live))})
}

func nonEmpty(in []string) []string {
	if len(in) == 0 {
		return nil
	}

	return in
}

func TestFake_lostReplies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		call func(store *admintest.Fake) error
		name string
		op   string
	}{
		{name: "a batch", op: "SADD", call: func(store *admintest.Fake) error {
			_, _, err := store.StageAdd(t.Context(), lock, tmp, token, []string{"a"}, time.Hour, true)

			return err
		}},
		{name: "an edit", op: "EDIT", call: func(store *admintest.Fake) error {
			store.Delete(lock) // no import runs

			_, _, err := store.EditSet(t.Context(), lock, live, []string{"a"}, false)

			return err
		}},
		{name: "the swap", op: "SWAP", call: func(store *admintest.Fake) error {
			_, _, err := store.Commit(t.Context(), commitEmpty())

			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := admintest.New()
			_, _ = store.Lock(t.Context(), lock, token, time.Hour)
			store.LoseReply(tt.op, 1, false)

			require.ErrorIs(t, tt.call(store), admintest.ErrLostReply, "the call is applied and its reply is lost")
			require.NoError(t, tt.call(store), "only the nth call loses its reply")
		})
	}
}

func TestFake_failFrom(t *testing.T) {
	t.Parallel()

	store := admintest.New()
	store.FailFrom("PING", 2)

	require.NoError(t, store.Ping(t.Context()))
	require.ErrorIs(t, store.Ping(t.Context()), admintest.ErrInjected)
	require.ErrorIs(t, store.Ping(t.Context()), admintest.ErrInjected)
}

func TestFake_commitMore(t *testing.T) {
	t.Parallel()

	store := admintest.New()
	ctx := t.Context()

	_, _ = store.Lock(ctx, lock, token, time.Hour)
	_, staged, _ := store.StageAdd(ctx, lock, tmp, token, []string{"a"}, time.Hour, true)
	require.Equal(t, segmentadmin.Staged, staged)

	first, _, err := store.Commit(ctx, commitOf(1))
	require.NoError(t, err)

	again, _, err := store.Commit(ctx, commitOf(1))
	require.NoError(t, err)

	_, later, err := store.StageAdd(ctx, lock, tmp, token, []string{"b"}, time.Hour, false)
	require.NoError(t, err)

	store.WithoutUnlink()

	_, fell, err := store.Commit(ctx, &segmentadmin.Commit{
		Lock: lock, Tmp: tmp, Live: live, Old: old, Committed: "p:{s}:import:other", Token: token, Empty: true,
	})
	require.NoError(t, err)

	require.NoError(t, store.Unlock(ctx, lock, last, committed, token))

	assert.Equal(t, []any{segmentadmin.Committed, segmentadmin.AlreadyCommitted, segmentadmin.StageTempGone, true},
		[]any{first, again, later, fell})
	assert.NotContains(t, store.Keys(), committed, "the unlock removes the committed key of the run")
}

func TestFake_reads(t *testing.T) {
	t.Parallel()

	store := admintest.New()
	ctx := t.Context()

	require.NoError(t, store.SetValue(ctx, last, "v"))

	value, ok, err := store.Value(ctx, last)
	require.NoError(t, err)

	_, found, err := store.Value(ctx, "missing")
	require.NoError(t, err)

	assert.Equal(t, []any{"v", true, false}, []any{value, ok, found})
	assert.Zero(t, store.TTLOf("missing"))
}
