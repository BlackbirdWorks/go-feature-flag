//go:build docker

package redissegment_test

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmenttest"
	"github.com/blackbirdworks/semaphore/modules/core/segment/snapshot"
)

const seedChunk = 10_000

// snapEnv is a real store with a provider whose keys are private to one case.
type snapEnv struct {
	prov   *redissegment.Provider
	srv    *server
	prefix string
	bumps  int
}

func newSnapEnv(tb testing.TB, srv *server, id int64) *snapEnv {
	tb.Helper()

	env := &snapEnv{srv: srv, prefix: "snap" + strconv.FormatInt(id, 10) + ":"}
	segs := map[string]redissegment.SegmentOptions{}

	for _, name := range segmenttest.SnapshotSegments() {
		segs[name] = redissegment.SegmentOptions{Key: env.setKey(name), VersionKey: env.prefix + name + ":version"}
	}

	prov, err := redissegment.New(srv.client, &redissegment.Options{Segments: segs})
	require.NoError(tb, err)

	env.prov = prov

	return env
}

func (e *snapEnv) setKey(name string) string { return e.prefix + "{" + name + "}" }

// seed replaces the set and bumps the version key, as the writer of a segment does.
func (e *snapEnv) seed(name string, ids []string) error {
	ctx := context.Background()

	err := e.srv.client.Del(ctx, e.setKey(name)).Err()
	if err != nil {
		return err
	}

	for start := 0; start < len(ids); start += seedChunk {
		members := make([]any, 0, seedChunk)
		for _, id := range ids[start:min(start+seedChunk, len(ids))] {
			members = append(members, id)
		}

		err = e.srv.client.SAdd(ctx, e.setKey(name), members...).Err()
		if err != nil {
			return err
		}
	}

	e.bumps++

	return e.srv.client.Set(ctx, e.prefix+name+":version", "v"+strconv.Itoa(e.bumps), 0).Err()
}

//nolint:paralleltest // the suite measures the allocations of the whole process in one case, and the stores are shared
func TestSnapshotAgainstRealStores(t *testing.T) {
	for _, image := range []string{valkeyImage, redisImage} {
		t.Run(image, func(t *testing.T) { //nolint:paralleltest // sequential: see the test
			srv := startServerImage(t, image)
			ids := &atomic.Int64{}

			t.Run("snapshot source suite", func(t *testing.T) {
				segmenttest.RunSnapshotSource(t, func() (segmenttest.SnapshotFixture, error) {
					env := newSnapEnv(t, srv, ids.Add(1))

					return segmenttest.SnapshotFixture{Source: env.prov, Seed: env.seed, NoEmptySet: true}, nil
				})
			})

			t.Run("versioner suite", func(t *testing.T) {
				segmenttest.RunVersioner(t, func() (segmenttest.VersionerFixture, error) {
					env := newSnapEnv(t, srv, ids.Add(1))

					return segmenttest.VersionerFixture{Versioner: env.prov, Seed: env.seed}, nil
				})
			})

			t.Run("a holder follows the writer", func(t *testing.T) {
				env := newSnapEnv(t, srv, ids.Add(1))
				holder, err := snapshot.New("alpha", env.prov, snapshot.Options{})
				require.NoError(t, err)

				const members = 25_000

				big := make([]string, members)
				for i := range big {
					big[i] = "user-" + strconv.Itoa(i)
				}

				require.NoError(t, env.seed("alpha", big))
				require.NoError(t, holder.Refresh(context.Background()))
				assert.Equal(t, members, holder.Status().Members, "25 pages of SSCAN make one set")

				require.NoError(t, env.seed("alpha", []string{"only"}))
				require.NoError(t, holder.Refresh(context.Background()))
				assert.Equal(t, 1, holder.Status().Members, "the writer bumped the version, the holder pulled again")

				require.NoError(t, srv.client.Del(context.Background(), env.setKey("alpha")).Err())
				require.NoError(t, holder.Refresh(context.Background()), "the version did not move: nothing is pulled")
				assert.Equal(t, 1, holder.Status().Members)
			})
		})
	}
}

//nolint:paralleltest // sequential: one container for each image
func TestInitRefusesAUserWithoutTheSnapshotCommands(t *testing.T) {
	for _, image := range []string{valkeyImage, redisImage} {
		t.Run(image, func(t *testing.T) { //nolint:paralleltest // see above
			srv := startServerImage(t, image)
			ctx := context.Background()

			for user, commands := range map[string][]any{
				"lookup":   {"+ping", "+smismember", "+exists", "+get"},
				"snapshot": {"+ping", "+smismember", "+exists", "+get", "+sscan"},
			} {
				args := append([]any{"ACL", "SETUSER", user, "on", ">pw", "~*"}, commands...)
				require.NoError(t, srv.client.Do(ctx, args...).Err())
			}

			segs := map[string]redissegment.SegmentOptions{
				"alpha": {Key: "seg:{alpha}", VersionKey: "seg:alpha:version", Snapshot: true},
			}

			tests := []struct {
				user        string
				wantErrText string
			}{
				{user: "lookup", wantErrText: "the command SSCAN is not available"},
				{user: "snapshot"},
			}

			for _, tt := range tests {
				client := redis.NewClient(&redis.Options{
					Addr: srv.address, Username: tt.user, Password: "pw", ContextTimeoutEnabled: true, MaxRetries: -1,
				})

				t.Cleanup(func() { _ = client.Close() })

				prov, err := redissegment.New(client, &redissegment.Options{Segments: segs})
				require.NoError(t, err)

				err = prov.Init(ctx)
				if tt.wantErrText == "" {
					require.NoError(t, err, "the user %s may run every command", tt.user)

					continue
				}

				require.ErrorIs(t, err, redissegment.ErrInit)
				require.ErrorContains(t, err, tt.wantErrText)
			}
		})
	}
}
