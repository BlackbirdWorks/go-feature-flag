//go:build docker

package valkeysegment_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	testcontainerredis "github.com/testcontainers/testcontainers-go/modules/redis"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
)

const valkeyImage = "docker.io/valkey/valkey:8-alpine"

type server struct {
	container *testcontainerredis.RedisContainer
	client    *redis.Client
	pipelines *atomic.Int64
}

// pipelineCounter counts the pipelines that the client sends: one pipeline is one round trip.
type pipelineCounter struct{ cnt *atomic.Int64 }

func (pipelineCounter) DialHook(next redis.DialHook) redis.DialHook { return next }

func (pipelineCounter) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }

func (c pipelineCounter) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		c.cnt.Add(1)

		return next(ctx, cmds)
	}
}

func startServer(t *testing.T) *server {
	t.Helper()

	ctx := context.Background()

	container, err := testcontainerredis.Run(ctx, valkeyImage)
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err)

	address, err := container.Endpoint(ctx, "")
	require.NoError(t, err)

	pipelines := &atomic.Int64{}
	client := redis.NewClient(&redis.Options{
		Addr:        address,
		MaxRetries:  -1,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	})
	client.AddHook(pipelineCounter{cnt: pipelines})
	t.Cleanup(func() { _ = client.Close() })

	return &server{container: container, client: client, pipelines: pipelines}
}

func (srv *server) commandCalls(t *testing.T, name string) int64 {
	t.Helper()

	info, err := srv.client.Info(context.Background(), "commandstats").Result()
	require.NoError(t, err)

	for line := range strings.SplitSeq(info, "\n") {
		prefix := "cmdstat_" + name + ":calls="
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
			calls, _, _ := strings.Cut(rest, ",")
			parsed, parseErr := strconv.ParseInt(calls, 10, 64)
			require.NoError(t, parseErr)

			return parsed
		}
	}

	return 0
}

func e2eProvider(t *testing.T, srv *server, opts valkeysegment.Options) *valkeysegment.Provider {
	t.Helper()

	if opts.Segments == nil {
		opts.Segments = map[string]valkeysegment.SegmentOptions{
			segBeta: {}, "flags": {Layout: valkeysegment.LayoutKeys, Key: flagPrefix},
		}
	}

	prov, err := valkeysegment.New(srv.client, opts)
	require.NoError(t, err)

	return prov
}

func TestE2EContains(t *testing.T) {
	t.Parallel()

	srv := startServer(t)
	ctx := context.Background()

	require.NoError(t, srv.client.SAdd(ctx, "semaphore:segment:{beta}", idAlice, "carol").Err())
	require.NoError(t, srv.client.Set(ctx, "flag:alice", "1", 0).Err())

	tests := []struct {
		name       string
		opts       valkeysegment.Options
		lookups    []segment.Lookup
		wantStates []segment.State
	}{
		{
			name:       "member and not member",
			lookups:    []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")},
			wantStates: []segment.State{segment.Member, segment.NotMember},
		},
		{
			name:       "missing key is unknown",
			lookups:    []segment.Lookup{lk(segGhost, idAlice)},
			opts:       valkeysegment.Options{Segments: map[string]valkeysegment.SegmentOptions{segGhost: {}}},
			wantStates: []segment.State{segment.Unknown},
		},
		{
			name:    "missing key is not member on request",
			lookups: []segment.Lookup{lk(segGhost, idAlice)},
			opts: valkeysegment.Options{
				MissingKey: valkeysegment.MissingNotMember,
				Segments:   map[string]valkeysegment.SegmentOptions{segGhost: {}},
			},
			wantStates: []segment.State{segment.NotMember},
		},
		{
			name:       "keys layout",
			lookups:    []segment.Lookup{lk("flags", idAlice), lk("flags", "zed")},
			wantStates: []segment.State{segment.Member, segment.NotMember},
		},
		{
			name:       "set and keys in one call",
			lookups:    []segment.Lookup{lk("flags", idAlice), lk(segBeta, "carol"), lk(segBeta, "x")},
			wantStates: []segment.State{segment.Member, segment.Member, segment.NotMember},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prov := e2eProvider(t, srv, tt.opts)

			require.NoError(t, prov.Contains(ctx, tt.lookups))
			assert.Equal(t, tt.wantStates, states(tt.lookups))
		})
	}
}

func TestE2EOneRoundTrip(t *testing.T) {
	t.Parallel()

	srv := startServer(t)
	ctx := context.Background()

	require.NoError(t, srv.client.SAdd(ctx, "semaphore:segment:{beta}", "id3").Err())

	lookups := make([]segment.Lookup, 8)
	for i := range lookups {
		lookups[i] = lk(segBeta, "id"+strconv.Itoa(i))
	}

	prov := e2eProvider(t, srv, valkeysegment.Options{})

	// a new connection sends its handshake in pipelines: warm the pool with one call before counting
	require.NoError(t, prov.Contains(ctx, []segment.Lookup{lk(segBeta, "warm")}))

	beforePipelines := srv.pipelines.Load()
	beforeSMIs := srv.commandCalls(t, "smismember")

	require.NoError(t, prov.Contains(ctx, lookups))

	assert.Equal(t, int64(1), srv.pipelines.Load()-beforePipelines)
	assert.Equal(t, int64(1), srv.commandCalls(t, "smismember")-beforeSMIs)

	want := make([]segment.State, 8)
	for i := range want {
		want[i] = segment.NotMember
	}

	want[3] = segment.Member

	assert.Equal(t, want, states(lookups))
}

func TestE2EServerStopped(t *testing.T) {
	t.Parallel()

	srv := startServer(t)
	ctx := context.Background()

	require.NoError(t, srv.client.SAdd(ctx, "semaphore:segment:{beta}", idAlice).Err())

	prov := e2eProvider(t, srv, valkeysegment.Options{Timeout: 2 * time.Second})
	before := []segment.Lookup{lk(segBeta, idAlice)}
	require.NoError(t, prov.Contains(ctx, before))
	require.Equal(t, []segment.State{segment.Member}, states(before))

	require.NoError(t, srv.container.Stop(ctx, nil))

	lookups := []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")}
	err := prov.Contains(ctx, lookups)

	var lookupErr *valkeysegment.LookupError

	require.ErrorAs(t, err, &lookupErr)
	assert.Contains(
		t,
		[]valkeysegment.Class{valkeysegment.ClassUnavailable, valkeysegment.ClassTimeout},
		lookupErr.Class,
	)
	assert.Equal(t, 2, lookupErr.Failed)
	assert.Equal(t, []segment.State{segment.Unknown, segment.Unknown}, states(lookups))
}

func TestE2ETimeout(t *testing.T) {
	t.Parallel()

	// a server that accepts and never answers
	lc := net.ListenConfig{}

	listener, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var conns sync.WaitGroup

	done := make(chan struct{})

	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}

			conns.Go(func() {
				<-done

				_ = conn.Close()
			})
		}
	}()

	t.Cleanup(func() {
		close(done)

		_ = listener.Close()

		conns.Wait()
	})

	client := redis.NewClient(&redis.Options{
		Addr: listener.Addr().String(), MaxRetries: -1, Protocol: 2, DisableIdentity: true,
		ReadTimeout: time.Minute, DialTimeout: time.Minute,
	})

	t.Cleanup(func() { _ = client.Close() })

	prov, err := valkeysegment.New(client, valkeysegment.Options{
		Timeout:  50 * time.Millisecond,
		Segments: map[string]valkeysegment.SegmentOptions{segBeta: {}},
	})
	require.NoError(t, err)

	lookups := []segment.Lookup{lk(segBeta, idAlice)}

	callErr := prov.Contains(context.Background(), lookups)

	require.ErrorIs(t, callErr, valkeysegment.ErrTimeout)
	assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))
}

func TestE2ESwapWhileLookups(t *testing.T) {
	t.Parallel()

	srv := startServer(t)
	ctx := context.Background()
	live := "semaphore:segment:{beta}"

	require.NoError(t, srv.client.SAdd(ctx, live, "stable", "gen0").Err())

	prov := e2eProvider(t, srv, valkeysegment.Options{Timeout: 5 * time.Second})

	const swaps = 100

	stop := make(chan struct{})

	const readers = 4

	var wg sync.WaitGroup

	errs := make([]error, readers)
	got := make([][]segment.State, readers)

	for worker := range readers {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}

				lookups := []segment.Lookup{lk(segBeta, "stable"), lk(segBeta, "absent")}

				errs[worker] = prov.Contains(ctx, lookups)
				got[worker] = states(lookups)

				// the swap is atomic: a reader never sees a half-built set or a missing key
				if errs[worker] != nil || got[worker][0] != segment.Member || got[worker][1] != segment.NotMember {
					return
				}
			}
		})
	}

	for i := range swaps {
		tmp := "semaphore:segment:{beta}:tmp"

		require.NoError(t, srv.client.Del(ctx, tmp).Err())
		require.NoError(t, srv.client.SAdd(ctx, tmp, "stable", "gen"+strconv.Itoa(i+1)).Err())
		require.NoError(t, srv.client.Rename(ctx, tmp, live).Err())
	}

	close(stop)
	wg.Wait()

	for worker := range readers {
		require.NoError(t, errs[worker])
		assert.Equal(t, []segment.State{segment.Member, segment.NotMember}, got[worker])
	}
}

const e2eFlags = `beta-feature:
  variations:
    enabled: true
    disabled: false
  targeting:
    - name: beta
      query: targetingKey in segment("beta")
      variation: enabled
  defaultRule:
    variation: disabled

deny-list:
  variations:
    allowed: true
    denied: false
  targeting:
    - name: deny
      query: targetingKey in segment("blocked")
      onError: match
      variation: denied
  defaultRule:
    variation: allowed
`

func TestE2EEngine(t *testing.T) {
	t.Parallel()

	srv := startServer(t)
	ctx := context.Background()

	require.NoError(t, srv.client.SAdd(ctx, "semaphore:segment:{beta}", idAlice).Err())
	require.NoError(t, srv.client.SAdd(ctx, "semaphore:segment:{blocked}", "mallory").Err())

	provider, err := valkeysegment.New(srv.client, valkeysegment.Options{
		Timeout: 2 * time.Second,
		Segments: map[string]valkeysegment.SegmentOptions{
			segBeta: {}, "blocked": {},
		},
	})
	require.NoError(t, err)

	catalog, err := segment.NewCatalog(
		segment.Definition{Name: segBeta, Type: segment.TypeValkey},
		segment.Definition{Name: "blocked", Type: segment.TypeValkey},
	)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "flags.yaml")
	require.NoError(t, os.WriteFile(path, []byte(e2eFlags), 0o600))

	client, err := ffclient.New(&ffclient.Config{
		PollingInterval: time.Hour,
		Retriever:       &fileretriever.Retriever{Path: path},
		Segments:        catalog,
		SegmentProvider: provider,
	})
	require.NoError(t, err)
	t.Cleanup(client.Close)

	eval := func(flag, key string) (bool, segment.Status) {
		got, evalErr := client.BoolVariationDetails(flag, ffcontext.NewEvaluationContext(key), false)
		assert.NoError(t, evalErr)

		return got.Value, got.SegmentStatus
	}

	// the server is up
	value, status := eval("beta-feature", idAlice)
	assert.True(t, value, "a member matches the rule")
	assert.Equal(t, segment.StatusHealthy, status)

	value, status = eval("beta-feature", "zed")
	assert.False(t, value, "a non member gets the default")
	assert.Equal(t, segment.StatusHealthy, status)

	value, status = eval("deny-list", "mallory")
	assert.False(t, value, "a blocked user is denied")
	assert.Equal(t, segment.StatusHealthy, status)

	// the server is down
	require.NoError(t, srv.container.Stop(ctx, nil))

	value, status = eval("beta-feature", idAlice)
	assert.False(t, value, "the rule is skipped when the store is down")
	assert.Equal(t, segment.StatusStoreError, status)

	value, status = eval("deny-list", "zed")
	assert.False(t, value, "onError: match denies when the store is down")
	assert.Equal(t, segment.StatusStoreError, status)
}
