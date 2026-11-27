//go:build docker

package valkeysegment_test

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/modules/core/segment/segmenttest"
	"github.com/blackbirdworks/semaphore/modules/core/segment/snapshot"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

const (
	seedChunk    = 10_000
	userLookup   = "lookup"
	userSnapshot = "snapshot"
)

// snapEnv is a real store with a provider whose keys are private to one case.
type snapEnv struct {
	prov   *valkeysegment.Provider
	writer valkey.Client
	prefix string
	bumps  int
}

func newSnapEnv(tb testing.TB, srv *e2eServer, id int64) *snapEnv {
	tb.Helper()

	env := &snapEnv{writer: seed(tb, srv.defaultOption()), prefix: "snap" + strconv.FormatInt(id, 10) + ":"}
	segs := map[string]valkeysegment.SegmentOptions{}

	for _, name := range segmenttest.SnapshotSegments() {
		segs[name] = valkeysegment.SegmentOptions{Key: env.setKey(name), VersionKey: env.prefix + name + ":version"}
	}

	prov, err := valkeysegment.Dial(srv.defaultOption(), &valkeysegment.Options{Segments: segs})
	require.NoError(tb, err)

	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), e2eTimeout)
		defer cancel()

		_ = prov.Shutdown(ctx)
	})

	env.prov = prov

	return env
}

func (e *snapEnv) setKey(name string) string { return e.prefix + "{" + name + "}" }

// seed replaces the set and bumps the version key, as the writer of a segment does.
func (e *snapEnv) seed(name string, ids []string) error {
	ctx := context.Background()

	err := e.writer.Do(ctx, e.writer.B().Del().Key(e.setKey(name)).Build()).Error()
	if err != nil {
		return err
	}

	for start := 0; start < len(ids); start += seedChunk {
		err = e.writer.Do(ctx, e.writer.B().Sadd().Key(e.setKey(name)).
			Member(ids[start:min(start+seedChunk, len(ids))]...).Build()).Error()
		if err != nil {
			return err
		}
	}

	e.bumps++

	return e.writer.Do(ctx, e.writer.B().Set().Key(e.prefix+name+":version").Value("v"+strconv.Itoa(e.bumps)).Build()).
		Error()
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
				holder, err := snapshot.New(snapAlpha, env.prov, snapshot.Options{})
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

				require.NoError(
					t,
					env.writer.Do(context.Background(), env.writer.B().Del().Key(env.setKey("alpha")).Build()).Error(),
				)
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
			admin := seed(t, srv.defaultOption())
			ctx := context.Background()

			for user, commands := range map[string][]string{
				userLookup:   {"+ping", "+smismember", "+exists", "+get"},
				userSnapshot: {"+ping", "+smismember", "+exists", "+get", "+sscan"},
			} {
				require.NoError(t, admin.Do(ctx,
					admin.B().Arbitrary("ACL", "SETUSER", user, "on", ">pw", "~*").Args(commands...).Build()).Error())
			}

			segs := map[string]valkeysegment.SegmentOptions{
				snapAlpha: {Key: "seg:{alpha}", VersionKey: "seg:alpha:version", Snapshot: true},
			}

			tests := []struct {
				user        string
				wantErrText string
			}{
				{user: userLookup, wantErrText: "the command SSCAN is not available"},
				{user: userSnapshot},
			}

			for _, tt := range tests {
				opt := srv.defaultOption()
				opt.Username, opt.Password = tt.user, "pw"

				prov, err := valkeysegment.Dial(opt, &valkeysegment.Options{Segments: segs})
				require.NoError(t, err)

				t.Cleanup(func() {
					shutdownCtx, cancel := context.WithTimeout(context.Background(), e2eTimeout)
					defer cancel()

					_ = prov.Shutdown(shutdownCtx)
				})

				err = prov.Init(ctx)
				if tt.wantErrText == "" {
					require.NoError(t, err, "the user %s may run every command", tt.user)

					continue
				}

				require.ErrorIs(t, err, valkeysegment.ErrInit)
				require.ErrorContains(t, err, tt.wantErrText)
			}
		})
	}
}
