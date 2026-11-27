//go:build docker

package redissegment_test

import (
	"context"
	"errors"
	"maps"
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
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
)

const (
	valkeyImage = "docker.io/valkey/valkey:8-alpine"
	redisImage  = "docker.io/redis:7-alpine"
)

type server struct {
	container *testcontainerredis.RedisContainer
	client    *redis.Client
	pipelines *atomic.Int64
	fault     *faultHook
	address   string
}

var errInjected = errors.New("injected store failure")

// faultHook counts the pipelines that the client sends (one pipeline is one round trip) and injects the faults of
// the conformance suite between the client and the real server.
type faultHook struct {
	gate    chan struct{}
	entered chan struct{}
	failSeg map[string]bool
	count   atomic.Int64
	mu      sync.Mutex
	failed  bool
}

func (*faultHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (*faultHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }

func (h *faultHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.count.Add(1)

		h.mu.Lock()
		gate, entered, failed := h.gate, h.entered, h.failed
		failSeg := maps.Clone(h.failSeg)
		h.mu.Unlock()

		if entered != nil {
			entered <- struct{}{}
		}

		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		if failed {
			return errInjected
		}

		err := next(ctx, cmds)

		for _, cmd := range cmds {
			args := cmd.Args()
			if len(args) < 2 {
				continue
			}

			key := keyOf(args)
			for seg := range failSeg {
				if strings.Contains(key, seg) {
					cmd.SetErr(errInjected)
				}
			}
		}

		return err
	}
}

func (h *faultHook) Fail() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.failed = true
}

func (h *faultHook) FailSegment(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.failSeg == nil {
		h.failSeg = map[string]bool{}
	}

	h.failSeg[name] = true
}

func (h *faultHook) Hang() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.gate, h.entered = make(chan struct{}), make(chan struct{}, 64)

	return h.entered
}

func (h *faultHook) Release() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.gate != nil {
		close(h.gate)

		h.gate = nil
	}
}

func (h *faultHook) Heal() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.failed = false
	h.failSeg = nil
}

func startServer(tb testing.TB) *server {
	tb.Helper()

	return startServerImage(tb, valkeyImage)
}

func startServerImage(tb testing.TB, image string) *server {
	tb.Helper()

	ctx := context.Background()

	container, err := testcontainerredis.Run(ctx, image)
	testcontainers.CleanupContainer(tb, container)
	require.NoError(tb, err)

	address, err := container.Endpoint(ctx, "")
	require.NoError(tb, err)

	hook := &faultHook{}
	srv := &server{container: container, fault: hook, pipelines: &hook.count, address: address}
	srv.client = srv.newClient(tb, address)

	return srv
}

// newClient returns a client of the address with the fault hook, closed at the end of the test.
func (srv *server) newClient(tb testing.TB, address string) *redis.Client {
	tb.Helper()

	client := redis.NewClient(&redis.Options{
		Addr:                  address,
		ContextTimeoutEnabled: true,
		MaxRetries:            -1,
		DialTimeout:           time.Second,
		ReadTimeout:           time.Second,
	})
	client.AddHook(srv.fault)
	tb.Cleanup(func() { _ = client.Close() })

	return client
}

func (srv *server) commandCalls(tb testing.TB, name string) int64 {
	tb.Helper()

	info, err := srv.client.Info(context.Background(), "commandstats").Result()
	require.NoError(tb, err)

	for line := range strings.SplitSeq(info, "\n") {
		prefix := "cmdstat_" + name + ":calls="
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
			calls, _, _ := strings.Cut(rest, ",")
			parsed, parseErr := strconv.ParseInt(calls, 10, 64)
			require.NoError(tb, parseErr)

			return parsed
		}
	}

	return 0
}

func e2eProvider(t *testing.T, srv *server, opts *redissegment.Options) *redissegment.Provider {
	t.Helper()

	if opts == nil {
		opts = &redissegment.Options{}
	}

	if opts.Segments == nil {
		opts.Segments = map[string]redissegment.SegmentOptions{
			segBeta: {}, "flags": {Layout: redissegment.LayoutKeys, Key: flagPrefix},
		}
	}

	prov, err := redissegment.New(srv.client, opts)
	require.NoError(t, err)

	return prov
}

func TestE2EContains(t *testing.T) {
	t.Parallel()

	for _, image := range []string{valkeyImage, redisImage} {
		t.Run(image, func(t *testing.T) {
			t.Parallel()

			e2eContains(t, image)
		})
	}
}

func e2eContains(t *testing.T, image string) {
	t.Helper()

	srv := startServerImage(t, image)
	ctx := context.Background()

	require.NoError(t, srv.client.SAdd(ctx, "semaphore:segment:{beta}", idAlice, "carol").Err())
	require.NoError(t, srv.client.Set(ctx, keyFlagAlice, "1", 0).Err())

	tests := []struct {
		opts       *redissegment.Options
		name       string
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
			opts:       &redissegment.Options{Segments: map[string]redissegment.SegmentOptions{segGhost: {}}},
			wantStates: []segment.State{segment.Unknown},
		},
		{
			name:    "missing key is not member on request",
			lookups: []segment.Lookup{lk(segGhost, idAlice)},
			opts: &redissegment.Options{
				MissingKey: redissegment.MissingNotMember,
				Segments:   map[string]redissegment.SegmentOptions{segGhost: {}},
			},
			wantStates: []segment.State{segment.NotMember},
		},
		{
			name:       nameKeysLayout,
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

	prov := e2eProvider(t, srv, &redissegment.Options{})

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

	prov := e2eProvider(t, srv, &redissegment.Options{Timeout: 2 * time.Second})
	before := []segment.Lookup{lk(segBeta, idAlice)}
	require.NoError(t, prov.Contains(ctx, before))
	require.Equal(t, []segment.State{segment.Member}, states(before))

	require.NoError(t, srv.container.Stop(ctx, nil))

	lookups := []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")}
	err := prov.Contains(ctx, lookups)

	var lookupErr *redissegment.LookupError

	require.ErrorAs(t, err, &lookupErr)
	assert.Contains(
		t,
		[]redissegment.Class{redissegment.ClassUnavailable, redissegment.ClassTimeout},
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
	acceptDone := make(chan struct{})

	go func() {
		defer close(acceptDone)

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

		// the accept loop calls conns.Go: wait for it to end before conns.Wait (a WaitGroup Add that is not ordered
		// before Wait is a data race)
		<-acceptDone
		conns.Wait()
	})

	client := redis.NewClient(&redis.Options{
		Addr: listener.Addr().String(), MaxRetries: -1, Protocol: 2, DisableIdentity: true,
		ContextTimeoutEnabled: true,
		ReadTimeout:           time.Minute, DialTimeout: time.Minute,
	})

	t.Cleanup(func() { _ = client.Close() })

	prov, err := redissegment.New(client, &redissegment.Options{
		Timeout:  50 * time.Millisecond,
		Segments: map[string]redissegment.SegmentOptions{segBeta: {}},
	})
	require.NoError(t, err)

	lookups := []segment.Lookup{lk(segBeta, idAlice)}

	callErr := prov.Contains(context.Background(), lookups)

	require.ErrorIs(t, callErr, redissegment.ErrTimeout)
	assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))
}

func TestE2ESwapWhileLookups(t *testing.T) {
	t.Parallel()

	srv := startServer(t)
	ctx := context.Background()
	live := "semaphore:segment:{beta}"

	require.NoError(t, srv.client.SAdd(ctx, live, "stable", "gen0").Err())

	prov := e2eProvider(t, srv, &redissegment.Options{Timeout: 5 * time.Second})

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

	provider, err := redissegment.New(srv.client, &redissegment.Options{
		Timeout: 2 * time.Second,
		Segments: map[string]redissegment.SegmentOptions{
			segBeta: {}, "blocked": {},
		},
	})
	require.NoError(t, err)

	catalog, err := segment.NewCatalog(
		segment.Definition{Name: segBeta, Type: segment.TypeRedis},
		segment.Definition{Name: "blocked", Type: segment.TypeRedis},
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
