package segmentadmin_test

import (
	"context"
	"fmt"
	"strconv"
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
		{name: "swap puts the new set live and keeps the old one", want: "1 true false", run: caseSwap},
		{name: "swap without a live key", want: "1", run: caseSwapNoLive},
		{name: "swap without a temporary key fails", want: "failed", run: caseSwapNoTemp},
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

func caseSwap(ctx context.Context, store segmentadmin.Store) (string, error) {
	_, err := store.SAdd(ctx, liveKey, []string{"a", "b", "c"}, 0)
	if err != nil {
		return "", err
	}

	_, err = store.SAdd(ctx, tmpKey, []string{"b"}, time.Hour)
	if err != nil {
		return "", err
	}

	err = store.Swap(ctx, liveKey, tmpKey, oldKey, time.Hour)
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

	return fmt.Sprintf("%d %t %t", live, oldExists && oldTTL > 0, liveTTL > 0), err
}

func caseSwapNoLive(ctx context.Context, store segmentadmin.Store) (string, error) {
	_, err := store.SAdd(ctx, tmpKey, []string{"b"}, time.Hour)
	if err != nil {
		return "", err
	}

	err = store.Swap(ctx, liveKey, tmpKey, oldKey, time.Hour)
	if err != nil {
		return "", err
	}

	card, err := store.SCard(ctx, liveKey)

	return strconv.FormatInt(card, 10), err
}

func caseSwapNoTemp(ctx context.Context, store segmentadmin.Store) (string, error) {
	outcome := "swapped"

	err := store.Swap(ctx, liveKey, tmpKey, oldKey, time.Hour)
	if err != nil {
		outcome = "failed"
	}

	return outcome, nil
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
			if err != nil {
				return // valkey-go refuses a client at construction when nothing answers
			}

			t.Cleanup(store.Close)
			require.NoError(t, store.Ping(t.Context()))

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			require.ErrorIs(t, store.Ping(ctx), segmentadmin.ErrStore)
			_, err = store.SAdd(ctx, liveKey, []string{"a"}, time.Hour)
			require.ErrorIs(t, err, segmentadmin.ErrStore)
			require.ErrorIs(t, store.Swap(ctx, liveKey, tmpKey, oldKey, time.Hour), segmentadmin.ErrStore)
			_, _, err = store.CountKeys(ctx, memberPrefix, 10)
			require.ErrorIs(t, err, segmentadmin.ErrStore)
		})
	}
}
