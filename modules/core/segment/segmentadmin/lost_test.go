package segmentadmin_test

import (
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
)

// TestImport_lostReply applies a script on the server and drops its reply, for each client and its retry paths: the
// set is replaced and the version is bumped exactly once, whichever call lost its reply.
func TestImport_lostReply(t *testing.T) {
	t.Parallel()

	valkeyStore := func(addr string) segmentadmin.Store {
		client, err := valkey.NewClient(valkey.ClientOption{
			InitAddress: []string{addr}, ForceSingleClient: true, DisableCache: true,
		})
		require.NoError(t, err)

		return segmentadmin.NewValkey(client)
	}
	redisStore := func(retries int) func(string) segmentadmin.Store {
		return func(addr string) segmentadmin.Store {
			return segmentadmin.NewRedis(redis.NewClient(&redis.Options{Addr: addr, MaxRetries: retries}))
		}
	}

	tests := []struct {
		store func(addr string) segmentadmin.Store
		name  string
		eval  int
	}{
		{name: "valkey-go, batch", store: valkeyStore, eval: 1},
		{name: "valkey-go, swap", store: valkeyStore, eval: 2},
		{name: "go-redis without its retries, batch", store: redisStore(-1), eval: 1},
		{name: "go-redis without its retries, swap", store: redisStore(-1), eval: 2},
		{name: "go-redis with its retries, batch", store: redisStore(3), eval: 1},
		{name: "go-redis with its retries, swap", store: redisStore(3), eval: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := startStub(t, false)
			store := tt.store(srv.addr())
			t.Cleanup(store.Close)

			_, err := store.SAdd(t.Context(), liveKey, []string{"old"}, 0)
			require.NoError(t, err)

			srv.dropReplyOfEval(tt.eval)

			reader, err := segmentadmin.NewReader(strings.NewReader("a\nb\n"), segmentadmin.ReaderOptions{})
			require.NoError(t, err)

			opts := segmentadmin.ImportOptions{Token: runToken, Wait: instant}
			_, err = segmentadmin.Import(t.Context(), store, vipTarget(), reader, opts)
			require.NoError(t, err)

			card, err := store.SCard(t.Context(), liveKey)
			require.NoError(t, err)
			assert.Equal(t, int64(2), card, "the live set is the new one")
			assert.Equal(t, 1, srv.writes(versionKey), "the version is bumped exactly once")

			member, _, err := store.Value(t.Context(), versionKey)
			require.NoError(t, err)
			assert.NotEmpty(t, member)
		})
	}
}
