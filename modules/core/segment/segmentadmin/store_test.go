package segmentadmin_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin/admintest"
)

type storeCase struct {
	run  func(ctx context.Context, store segmentadmin.Store) (string, error)
	name string
	want string
}

// storeCases are the behaviours that every Store must have, each as a function of the store that returns a text.
func storeCases() []storeCase {
	return []storeCase{
		{name: "ping", want: "ok", run: casePing},
		{name: "sadd counts the new members and sets the expiry", want: "2 1 true ttl=true", run: caseSAdd},
		{name: "srem and scard", want: "1 1", run: caseSRem},
		{name: "commit puts the new set live and keeps the old one", want: "0 1 true false", run: caseCommit},
		{name: "commit without a live key", want: "0 1", run: caseCommitNoLive},
		{name: "commit refused when the lock is not the run's", want: "1 live=3 old=false",
			run: caseCommitRefused(1, "other", false)},
		{name: "commit refused when the temporary key is gone", want: "2 live=3 old=false",
			run: caseCommitRefused(1, runToken, true)},
		{name: "commit refused when the cardinality differs", want: "3 live=3 old=false",
			run: caseCommitRefused(2, runToken, false)},
		{name: "the same swap sent twice is answered already committed", want: "0 then 4 live=1", run: caseCommitTwice},
		{name: "a batch after the first never recreates the temporary key", want: "later=2 exists=false again=1",
			run: caseStageLater},
		{
			name: "an edit is refused while the import lock is held",
			want: "2 false 1 refused=true card=1",
			run:  caseEdit,
		},
		{name: "a swap sent again after the lock was taken is still answered already committed",
			want: "0 then 4", run: caseCommitAfterTakeover},
		{name: "the lock is idempotent by its token", want: "first=true again=true other=false", run: caseLockAgain},
		{
			name: "a claim of a run without the lock removes nothing",
			want: "foreign=false stale=true owned=true after=false " + tmpKey,
			run:  caseClaim,
		},
		{name: "commit of an empty import removes the live key", want: "0 false", run: caseCommitEmpty},
		{
			name: "lock is taken once and released by its holder only",
			want: "true false t1 locked=false last=false",
			run:  caseLock,
		},
		{
			name: "a batch is refused when the lock is not the run's",
			want: "0 result=1 exists=false",
			run:  caseStageLost,
		},
		{name: "unlink counts what existed", want: "1 false", run: caseUnlink},
		{name: "values", want: "v true  false", run: caseValues},
		{name: "member keys are created once and counted by prefix", want: "2 1 2 false 1 true", run: caseKeys},
		{name: "a missing key has no ttl", want: "false 0", run: caseMissing},
	}
}

func casePing(ctx context.Context, store segmentadmin.Store) (string, error) {
	err := store.Ping(ctx)

	return "ok", err
}

func caseSAdd(ctx context.Context, store segmentadmin.Store) (string, error) {
	first, err := store.SAdd(ctx, liveKey, []string{"a", "b"}, time.Hour)
	if err != nil {
		return "", err
	}

	second, err := store.SAdd(ctx, liveKey, []string{"b", "c"}, 0)
	if err != nil {
		return "", err
	}

	exists, ttl, err := store.KeyTTL(ctx, liveKey)

	return fmt.Sprintf("%d %d %t ttl=%t", first, second, exists, ttl > 0), err
}

func caseSRem(ctx context.Context, store segmentadmin.Store) (string, error) {
	_, err := store.SAdd(ctx, liveKey, []string{"a", "b"}, 0)
	if err != nil {
		return "", err
	}

	removed, err := store.SRem(ctx, liveKey, []string{"a", "zz"})
	if err != nil {
		return "", err
	}

	card, err := store.SCard(ctx, liveKey)

	return fmt.Sprintf("%d %d", removed, card), err
}

func tokenCommit(want int64, token string, empty bool) *segmentadmin.Commit {
	return &segmentadmin.Commit{
		Lock:      lockKey,
		Tmp:       tmpKey,
		Live:      liveKey,
		Old:       oldKey,
		Committed: committedKey,
		Source:    sourceKey,
		Token:     token,
		Want:      want,
		OldTTL:    time.Hour,
		Empty:     empty,
	}
}

// stage takes the lock and writes the members of a temporary key, as an import does.
func stage(ctx context.Context, store segmentadmin.Store, members ...string) error {
	ok, err := store.Lock(ctx, lockKey, runToken, time.Hour)
	if err != nil || !ok {
		return fmt.Errorf("lock: %t: %w", ok, err)
	}

	_, result, err := store.StageAdd(ctx, lockKey, tmpKey, runToken, members, time.Hour, true)
	if err != nil || result != segmentadmin.Staged {
		return fmt.Errorf("stage: %d: %w", result, err)
	}

	return nil
}

func caseCommit(ctx context.Context, store segmentadmin.Store) (string, error) {
	_, err := store.SAdd(ctx, liveKey, []string{"a", "b", "c"}, 0)
	if err != nil {
		return "", err
	}

	err = stage(ctx, store, "b")
	if err != nil {
		return "", err
	}

	result, _, err := store.Commit(ctx, tokenCommit(1, runToken, false))
	if err != nil {
		return "", err
	}

	live, err := store.SCard(ctx, liveKey)
	if err != nil {
		return "", err
	}

	oldExists, oldTTL, err := store.KeyTTL(ctx, oldKey)
	if err != nil {
		return "", err
	}

	_, liveTTL, err := store.KeyTTL(ctx, liveKey)

	return fmt.Sprintf("%d %d %t %t", result, live, oldExists && oldTTL > 0, liveTTL > 0), err
}

func caseCommitNoLive(ctx context.Context, store segmentadmin.Store) (string, error) {
	err := stage(ctx, store, "b")
	if err != nil {
		return "", err
	}

	result, _, err := store.Commit(ctx, tokenCommit(1, runToken, false))
	if err != nil {
		return "", err
	}

	card, err := store.SCard(ctx, liveKey)

	return fmt.Sprintf("%d %d", result, card), err
}

// caseCommitRefused runs a commit that must not change anything and returns its result and the live cardinality.
func caseCommitRefused(want int64, token string, drop bool) func(context.Context, segmentadmin.Store) (string, error) {
	return func(ctx context.Context, store segmentadmin.Store) (string, error) {
		_, err := store.SAdd(ctx, liveKey, []string{"a", "b", "c"}, 0)
		if err != nil {
			return "", err
		}

		err = stage(ctx, store, "b")
		if err != nil {
			return "", err
		}

		if drop {
			_, _, err = store.Unlink(ctx, tmpKey)
			if err != nil {
				return "", err
			}
		}

		result, _, err := store.Commit(ctx, tokenCommit(want, token, false))
		if err != nil {
			return "", err
		}

		live, err := store.SCard(ctx, liveKey)
		if err != nil {
			return "", err
		}

		oldExists, _, err := store.KeyTTL(ctx, oldKey)

		return fmt.Sprintf("%d live=%d old=%t", result, live, oldExists), err
	}
}

// caseCommitTwice sends the swap of one run twice, as a client does after a lost reply.
func caseCommitTwice(ctx context.Context, store segmentadmin.Store) (string, error) {
	_, err := store.SAdd(ctx, liveKey, []string{"a", "b", "c"}, 0)
	if err != nil {
		return "", err
	}

	err = stage(ctx, store, "b")
	if err != nil {
		return "", err
	}

	first, _, err := store.Commit(ctx, tokenCommit(1, runToken, false))
	if err != nil {
		return "", err
	}

	second, _, err := store.Commit(ctx, tokenCommit(1, runToken, false))
	if err != nil {
		return "", err
	}

	live, err := store.SCard(ctx, liveKey)

	return fmt.Sprintf("%d then %d live=%d", first, second, live), err
}

// caseStageLater sends a batch after the first one found the temporary key gone, and a first batch again.
func caseStageLater(ctx context.Context, store segmentadmin.Store) (string, error) {
	err := stage(ctx, store, "a", "b")
	if err != nil {
		return "", err
	}

	_, _, err = store.Unlink(ctx, tmpKey)
	if err != nil {
		return "", err
	}

	_, later, err := store.StageAdd(ctx, lockKey, tmpKey, runToken, []string{"c"}, time.Hour, false)
	if err != nil {
		return "", err
	}

	exists, _, err := store.KeyTTL(ctx, tmpKey)
	if err != nil {
		return "", err
	}

	again, _, err := store.StageAdd(ctx, lockKey, tmpKey, runToken, []string{"c"}, time.Hour, true)

	return fmt.Sprintf("later=%d exists=%t again=%d", later, exists, again), err
}

// caseEdit edits the live set, then again while the import lock is held.
func caseEdit(ctx context.Context, store segmentadmin.Store) (string, error) {
	added, locked, err := store.EditSet(ctx, lockKey, liveKey, []string{"a", "b"}, false)
	if err != nil {
		return "", err
	}

	removed, _, err := store.EditSet(ctx, lockKey, liveKey, []string{"a", "zz"}, true)
	if err != nil {
		return "", err
	}

	_, err = store.Lock(ctx, lockKey, runToken, time.Hour)
	if err != nil {
		return "", err
	}

	_, refused, err := store.EditSet(ctx, lockKey, liveKey, []string{"c"}, false)
	if err != nil {
		return "", err
	}

	card, err := store.SCard(ctx, liveKey)

	return fmt.Sprintf("%d %t %d refused=%t card=%d", added, locked, removed, refused, card), err
}

// caseCommitAfterTakeover sends the swap of a run again after its lock was taken by another run: the answer is still
// that the swap was applied, because the token is checked in the committed key before the lock.
func caseCommitAfterTakeover(ctx context.Context, store segmentadmin.Store) (string, error) {
	err := stage(ctx, store, "b")
	if err != nil {
		return "", err
	}

	first, _, err := store.Commit(ctx, tokenCommit(1, runToken, false))
	if err != nil {
		return "", err
	}

	err = store.SetValue(ctx, lockKey, "other") // the lock is taken
	if err != nil {
		return "", err
	}

	again, _, err := store.Commit(ctx, tokenCommit(1, runToken, false))

	return fmt.Sprintf("%d then %d", first, again), err
}

// caseLockAgain takes the lock twice with the token, then with another one, and claims the temporary key.
func caseLockAgain(ctx context.Context, store segmentadmin.Store) (string, error) {
	first, err := store.Lock(ctx, lockKey, runToken, time.Hour)
	if err != nil {
		return "", err
	}

	again, err := store.Lock(ctx, lockKey, runToken, time.Hour)
	if err != nil {
		return "", err
	}

	other, err := store.Lock(ctx, lockKey, "other", time.Hour)

	return fmt.Sprintf("first=%t again=%t other=%t", first, again, other), err
}

// caseClaim claims with the lock, removing a stale key, then claims without it and removes nothing.
func caseClaim(ctx context.Context, store segmentadmin.Store) (string, error) {
	stale := liveKey + ":import:dead:new"

	_, err := store.SAdd(ctx, stale, []string{memberJunk}, time.Hour)
	if err != nil {
		return "", err
	}

	_, err = store.Lock(ctx, lockKey, runToken, time.Hour)
	if err != nil {
		return "", err
	}

	foreign, err := store.Claim(ctx, lockKey, lastKey, stale, tmpKey, "other") // not the holder: nothing happens
	if err != nil {
		return "", err
	}

	staleThere, _, err := store.KeyTTL(ctx, stale)
	if err != nil {
		return "", err
	}

	owned, err := store.Claim(ctx, lockKey, lastKey, stale, tmpKey, runToken)
	if err != nil {
		return "", err
	}

	staleAfter, _, err := store.KeyTTL(ctx, stale)
	if err != nil {
		return "", err
	}

	last, _, err := store.Value(ctx, lastKey)

	return fmt.Sprintf("foreign=%t stale=%t owned=%t after=%t %s", foreign, staleThere, owned, staleAfter, last), err
}

func caseCommitEmpty(ctx context.Context, store segmentadmin.Store) (string, error) {
	_, err := store.SAdd(ctx, liveKey, []string{"a"}, 0)
	if err != nil {
		return "", err
	}

	ok, err := store.Lock(ctx, lockKey, runToken, time.Hour)
	if err != nil || !ok {
		return "", fmt.Errorf("lock: %t: %w", ok, err)
	}

	result, _, err := store.Commit(ctx, tokenCommit(0, runToken, true))
	if err != nil {
		return "", err
	}

	exists, _, err := store.KeyTTL(ctx, liveKey)

	return fmt.Sprintf("%d %t", result, exists), err
}

func caseLock(ctx context.Context, store segmentadmin.Store) (string, error) {
	first, err := store.Lock(ctx, lockKey, runToken, time.Hour)
	if err != nil {
		return "", err
	}

	second, err := store.Lock(ctx, lockKey, "other", time.Hour)
	if err != nil {
		return "", err
	}

	err = store.SetValue(ctx, lastKey, tmpKey)
	if err != nil {
		return "", err
	}

	err = store.Unlock(ctx, lockKey, lastKey, committedKey, "other") // not the holder: nothing is removed
	if err != nil {
		return "", err
	}

	held, _, err := store.Value(ctx, lockKey)
	if err != nil {
		return "", err
	}

	err = store.Unlock(ctx, lockKey, lastKey, committedKey, runToken)
	if err != nil {
		return "", err
	}

	_, stillLocked, err := store.Value(ctx, lockKey)
	if err != nil {
		return "", err
	}

	_, stillLast, err := store.Value(ctx, lastKey)

	return fmt.Sprintf("%t %t %s locked=%t last=%t", first, second, held, stillLocked, stillLast), err
}

func caseStageLost(ctx context.Context, store segmentadmin.Store) (string, error) {
	ok, err := store.Lock(ctx, lockKey, "other", time.Hour)
	if err != nil || !ok {
		return "", fmt.Errorf("lock: %t: %w", ok, err)
	}

	added, result, err := store.StageAdd(ctx, lockKey, tmpKey, runToken, []string{"a"}, time.Hour, true)
	if err != nil {
		return "", err
	}

	exists, _, err := store.KeyTTL(ctx, tmpKey)

	return fmt.Sprintf("%d result=%d exists=%t", added, result, exists), err
}

func caseUnlink(ctx context.Context, store segmentadmin.Store) (string, error) {
	_, err := store.SAdd(ctx, liveKey, []string{"a"}, 0)
	if err != nil {
		return "", err
	}

	removed, fell, err := store.Unlink(ctx, liveKey, oldKey)

	return fmt.Sprintf("%d %t", removed, fell), err
}

func caseValues(ctx context.Context, store segmentadmin.Store) (string, error) {
	err := store.SetValue(ctx, versionKey, "v")
	if err != nil {
		return "", err
	}

	val, ok, err := store.Value(ctx, versionKey)
	if err != nil {
		return "", err
	}

	none, found, err := store.Value(ctx, versionKey+"x")

	return fmt.Sprintf("%s %t %s %t", val, ok, none, found), err
}

func caseKeys(ctx context.Context, store segmentadmin.Store) (string, error) {
	first, err := store.SetKeys(ctx, []string{memberPrefix + "a", memberPrefix + "b"})
	if err != nil {
		return "", err
	}

	again, err := store.SetKeys(ctx, []string{memberPrefix + "a", memberPrefix + "c"})
	if err != nil {
		return "", err
	}

	count, more, err := store.CountKeys(ctx, memberPrefix, 10)
	if err != nil {
		return "", err
	}

	limited, over, err := store.CountKeys(ctx, memberPrefix, 1)

	return fmt.Sprintf("%d %d %d %t %d %t", first, again, count-1, more, limited, over), err
}

func caseMissing(ctx context.Context, store segmentadmin.Store) (string, error) {
	exists, ttl, err := store.KeyTTL(ctx, liveKey)

	return fmt.Sprintf("%t %d", exists, ttl), err
}

func runStoreCases(t *testing.T, newStore func(t *testing.T) segmentadmin.Store) {
	t.Helper()

	for _, tt := range storeCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newStore(t)
			t.Cleanup(store.Close)

			got, err := tt.run(t.Context(), store)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewValkey(t *testing.T) {
	t.Parallel()

	runStoreCases(t, func(t *testing.T) segmentadmin.Store {
		t.Helper()

		srv := startStub(t, false)
		client, err := valkey.NewClient(valkey.ClientOption{
			InitAddress: []string{srv.addr()}, ForceSingleClient: true, DisableCache: true,
		})
		require.NoError(t, err)

		return segmentadmin.NewValkey(client)
	})
}

func TestNewRedis(t *testing.T) {
	t.Parallel()

	runStoreCases(t, func(t *testing.T) segmentadmin.Store {
		t.Helper()

		srv := startStub(t, false)

		return segmentadmin.NewRedis(redis.NewClient(&redis.Options{Addr: srv.addr(), MaxRetries: -1}))
	})
}

func TestNew(t *testing.T) {
	t.Parallel()

	runStoreCases(t, func(t *testing.T) segmentadmin.Store {
		t.Helper()

		return admintest.New()
	})
}

func TestStore_unlinkFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		newStore func(t *testing.T, addr string) segmentadmin.Store
		name     string
	}{
		{name: "valkey", newStore: func(t *testing.T, addr string) segmentadmin.Store {
			t.Helper()

			client, err := valkey.NewClient(valkey.ClientOption{
				InitAddress: []string{addr}, ForceSingleClient: true, DisableCache: true,
			})
			require.NoError(t, err)

			return segmentadmin.NewValkey(client)
		}},
		{name: "redis", newStore: func(_ *testing.T, addr string) segmentadmin.Store {
			return segmentadmin.NewRedis(redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1}))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := startStub(t, true)
			store := tt.newStore(t, srv.addr())
			t.Cleanup(store.Close)

			_, err := store.SAdd(t.Context(), liveKey, []string{"a"}, 0)
			require.NoError(t, err)

			removed, fell, err := store.Unlink(t.Context(), liveKey)
			require.NoError(t, err)
			assert.Equal(t, int64(1), removed)
			assert.True(t, fell)
		})
	}
}

func TestStore_serverDown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		newStore func(addr string) (segmentadmin.Store, error)
		name     string
	}{
		{name: "valkey", newStore: func(addr string) (segmentadmin.Store, error) {
			client, err := valkey.NewClient(valkey.ClientOption{
				InitAddress: []string{addr}, ForceSingleClient: true, DisableCache: true,
			})
			if err != nil {
				return nil, fmt.Errorf("client: %w", err)
			}

			return segmentadmin.NewValkey(client), nil
		}},
		{name: "redis", newStore: func(addr string) (segmentadmin.Store, error) {
			return segmentadmin.NewRedis(redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})), nil
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := startStub(t, false)
			addr := srv.addr()

			store, err := tt.newStore(addr)
			require.NoError(t, err, "the stub answers, so the client is built")

			t.Cleanup(store.Close)
			require.NoError(t, store.Ping(t.Context()))

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			require.ErrorIs(t, store.Ping(ctx), segmentadmin.ErrStore)
			_, err = store.SAdd(ctx, liveKey, []string{"a"}, time.Hour)
			require.ErrorIs(t, err, segmentadmin.ErrStore)

			_, _, stageErr := store.StageAdd(ctx, lockKey, tmpKey, runToken, []string{"a"}, time.Hour, true)
			require.ErrorIs(t, stageErr, segmentadmin.ErrStore)

			_, _, commitErr := store.Commit(ctx, tokenCommit(1, runToken, false))
			require.ErrorIs(t, commitErr, segmentadmin.ErrStore)

			_, locked := store.Lock(ctx, lockKey, runToken, time.Hour)
			require.ErrorIs(t, locked, segmentadmin.ErrStore)
			require.ErrorIs(t, store.Unlock(ctx, lockKey, lastKey, committedKey, runToken), segmentadmin.ErrStore)
			_, _, err = store.CountKeys(ctx, memberPrefix, 10)
			require.ErrorIs(t, err, segmentadmin.ErrStore)
		})
	}
}
