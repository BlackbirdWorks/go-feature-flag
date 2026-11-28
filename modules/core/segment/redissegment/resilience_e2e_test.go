//go:build docker

package redissegment_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/clock"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmenttest"
)

// seed loads the data of the suite into the server in the layout of the provider. The factory of the suite runs on
// the goroutine of each case, so a failure is returned and never asserted on the test of a parent.
func seed(
	srv *server,
	tag string,
	data segmenttest.Data,
	layout redissegment.Layout,
) (map[string]redissegment.SegmentOptions, error) {
	ctx := context.Background()
	segs := map[string]redissegment.SegmentOptions{}

	for name, ids := range data {
		if layout == redissegment.LayoutSet {
			key := "contract:" + tag + ":{" + name + "}"
			segs[name] = redissegment.SegmentOptions{Key: key}

			err := srv.client.Del(ctx, key).Err()
			if err != nil {
				return nil, fmt.Errorf("seed %s: %w", key, err)
			}

			args := make([]any, len(ids))
			for i, id := range ids {
				args[i] = id
			}

			err = srv.client.SAdd(ctx, key, args...).Err()
			if err != nil {
				return nil, fmt.Errorf("seed %s: %w", key, err)
			}

			continue
		}

		prefix := "contract:" + tag + ":" + name + ":"
		segs[name] = redissegment.SegmentOptions{Layout: redissegment.LayoutKeys, Key: prefix}

		_, err := srv.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for _, id := range ids {
				pipe.Set(ctx, prefix+id, "1", 0)
			}

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("seed %s: %w", prefix, err)
		}
	}

	return segs, nil
}

// conformanceTimeout bounds one lookup under loaded conformance runs: a cold pool in TestE2EConformance can
// pay DialTimeout (1s) + initConn ReadTimeout (1s) + up to 2 pipelines (2 * ReadTimeout = 2s) = 4s client
// worst-case, plus 1s scheduling headroom under 51 parallel subtests, strictly below segmenttest's 10s bound.
const conformanceTimeout = 5 * time.Second

func TestE2EConformance(t *testing.T) {
	t.Parallel()

	srv := startServer(t)

	var cases atomic.Int64

	tests := []struct {
		opts   *redissegment.Options
		name   string
		layout redissegment.Layout
	}{
		{name: "set layout", layout: redissegment.LayoutSet},
		{
			name:   "set layout atomic",
			layout: redissegment.LayoutSet,
			opts:   &redissegment.Options{MissingKey: redissegment.MissingUnknown, Atomic: true},
		},
		{name: nameKeysLayout, layout: redissegment.LayoutKeys},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			segmenttest.Run(t, func(data segmenttest.Data) (segmenttest.Fixture, error) {
				// each case has its own client, hook and key space on the shared server
				caseSrv := &server{container: srv.container, address: srv.address, fault: &faultHook{}}
				caseSrv.pipelines = &caseSrv.fault.count
				caseSrv.client = caseSrv.newClient(t, srv.address)

				opts := &redissegment.Options{MissingKey: redissegment.MissingUnknown}

				if tt.opts != nil {
					*opts = *tt.opts
				}

				if opts.Timeout == 0 {
					opts.Timeout = conformanceTimeout
				}

				// a key space of its own: the cases run in parallel
				segs, seedErr := seed(caseSrv, strconv.FormatInt(cases.Add(1), 10), data, tt.layout)
				if seedErr != nil {
					return segmenttest.Fixture{}, seedErr
				}

				opts.Segments = segs

				prov, err := redissegment.New(caseSrv.client, opts)
				if err != nil {
					return segmenttest.Fixture{}, err
				}

				closeCase := func() {
					caseSrv.fault.Release()
					_ = caseSrv.client.Close()
				}

				return segmenttest.Fixture{
					Provider: prov,
					Missing:  segment.MissingUnknown,
					Backend:  caseSrv.fault,
					Close:    closeCase,
				}, nil
			}, nil)
		})
	}
}

func TestE2EAtomicSetSwapNeverReadsAMissingKey(t *testing.T) {
	t.Parallel()

	srv := startServer(t)
	ctx := context.Background()
	live := "atomic:{beta}"

	require.NoError(t, srv.client.SAdd(ctx, live, "stable").Err())

	prov, err := redissegment.New(srv.client, &redissegment.Options{MissingKey: redissegment.MissingUnknown,
		Atomic: true, Timeout: 5 * time.Second,
		Segments: map[string]redissegment.SegmentOptions{segBeta: {Key: live}},
	})
	require.NoError(t, err)

	stop := make(chan struct{})

	var wg sync.WaitGroup

	const readers = 4

	got := make([][]segment.State, readers)
	errs := make([]error, readers)

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

				if errs[worker] != nil || got[worker][1] != segment.NotMember {
					return
				}
			}
		})
	}

	for i := range 100 {
		tmp := live + ":tmp"

		require.NoError(t, srv.client.SAdd(ctx, tmp, "stable", "gen"+strconv.Itoa(i)).Err())
		require.NoError(t, srv.client.Rename(ctx, tmp, live).Err())
	}

	close(stop)
	wg.Wait()

	for worker := range readers {
		require.NoError(t, errs[worker])
		assert.Equal(t, []segment.State{segment.Member, segment.NotMember}, got[worker])
	}
}

func TestE2EInitStatusExists(t *testing.T) {
	t.Parallel()

	srv := startServer(t)
	ctx := context.Background()

	require.NoError(t, srv.client.SAdd(ctx, "life:{beta}", "alice").Err())

	prov, err := redissegment.New(srv.client, &redissegment.Options{MissingKey: redissegment.MissingUnknown,
		Segments: map[string]redissegment.SegmentOptions{segBeta: {Key: "life:{beta}"}, segGone: {Key: "life:{gone}"}},
	})
	require.NoError(t, err)

	require.NoError(t, prov.Init(ctx))
	assert.Equal(t, redissegment.Status{Ready: true}, prov.Status())

	got, err := prov.Exists(ctx, []string{segBeta, segGone, "unconfigured"})
	require.NoError(t, err)
	assert.Equal(t, []segment.State{segment.Member, segment.NotMember, segment.Unknown}, got)
}

// outageProxy forwards to an address. Its listener stays open from the start: while it is down it accepts and
// closes at once and drops the connections that it forwards, so the client sees a store that resets.
type outageProxy struct {
	listener net.Listener
	conns    map[net.Conn]struct{}
	upstream string
	wg       sync.WaitGroup
	mu       sync.Mutex
	down     atomic.Bool
}

func newOutageProxy(t *testing.T, upstream string) *outageProxy {
	t.Helper()

	lc := net.ListenConfig{}

	listener, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	proxy := &outageProxy{listener: listener, upstream: upstream, conns: map[net.Conn]struct{}{}}

	proxy.wg.Go(proxy.accept)

	t.Cleanup(func() {
		_ = listener.Close()

		proxy.dropAll()
		proxy.wg.Wait()
	})

	return proxy
}

func (p *outageProxy) addr() string { return p.listener.Addr().String() }

func (p *outageProxy) track(conn net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.conns[conn] = struct{}{}
}

func (p *outageProxy) dropAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for conn := range p.conns {
		_ = conn.Close()
		delete(p.conns, conn)
	}
}

func (p *outageProxy) setDown(down bool) {
	p.down.Store(down)

	if down {
		p.dropAll()
	}
}

func (p *outageProxy) accept() {
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}

		if p.down.Load() {
			_ = client.Close()

			continue
		}

		var dialer net.Dialer

		upstream, dialErr := dialer.DialContext(context.Background(), "tcp", p.upstream)
		if dialErr != nil {
			_ = client.Close()

			continue
		}

		p.track(client)
		p.track(upstream)

		p.wg.Go(func() {
			_, _ = io.Copy(upstream, client)
			_ = upstream.Close()
		})
		p.wg.Go(func() {
			_, _ = io.Copy(client, upstream)
			_ = client.Close()
		})
	}
}

// breakerCooldown is ten minutes of the stepping clock (see TestMain), which is 600000 reads.
const breakerCooldown = 10 * time.Minute

// This test is serial (no t.Parallel): it steps the clock of the package, a global that every call reads. A serial
// test runs while the parallel ones are paused, and they resume after it, so no test observes its steps while it
// asserts, and its steps are over before the others start. The other tests do not step the clock themselves.
//
//nolint:paralleltest // serial on purpose: it steps the shared clock
func TestE2EBreakerOpensWithoutDialingAndRecovers(t *testing.T) {
	srv := startServer(t)
	ctx := context.Background()

	require.NoError(t, srv.client.SAdd(ctx, "breaker:{beta}", "alice").Err())

	proxy := newOutageProxy(t, srv.address)
	client := srv.newClient(t, proxy.addr())

	inner, err := redissegment.New(client, &redissegment.Options{MissingKey: redissegment.MissingUnknown,
		Timeout:  2 * time.Second,
		Segments: map[string]redissegment.SegmentOptions{segBeta: {Key: "breaker:{beta}"}},
	})
	require.NoError(t, err)

	counters := &resolver.Counters{}
	res, err := resolver.New(inner, &resolver.Options{
		Observer: counters,
		Segments: map[string]resolver.SegmentOptions{
			segBeta: {NoCache: true, BreakerFailures: 3, BreakerCooldown: breakerCooldown},
		},
	})
	require.NoError(t, err)

	call := func() ([]segment.State, error) {
		lookups := []segment.Lookup{lk(segBeta, idAlice)}
		callErr := res.Contains(ctx, lookups)

		return states(lookups), callErr
	}

	got, err := call()
	require.NoError(t, err)
	require.Equal(t, []segment.State{segment.Member}, got)

	proxy.setDown(true)

	for range 3 {
		got, err = call()
		require.ErrorIs(t, err, redissegment.ErrUnavailable)
		assert.Equal(t, []segment.State{segment.Unknown}, got)
	}

	sent := srv.pipelines.Load()

	for range 5 {
		got, err = call()
		require.ErrorIs(t, err, resolver.ErrCircuitOpen)
		assert.Equal(t, []segment.State{segment.Unknown}, got)
	}

	assert.Equal(t, sent, srv.pipelines.Load(), "an open breaker sends nothing to the store")
	assert.Equal(t, resolver.BreakerOpen, res.Status().Segments[segBeta].Breaker)

	proxy.setDown(false)

	// The clock is shared with the tests that run in parallel and steps on every read. The cool-down is long, so
	// that no other test can reach it while the breaker is asserted open, and the test steps past it by reading.
	for range breakerCooldown/callStep + 1 {
		clock.Now()
	}

	got, err = call()
	require.NoError(t, err, "the probe after the cool-down reaches the store that is back")
	assert.Equal(t, []segment.State{segment.Member}, got)
	assert.Equal(t, resolver.BreakerClosed, res.Status().Segments[segBeta].Breaker)

	stats := counters.Snapshot()
	assert.Equal(t, uint64(1), stats.BreakerOpened)
	assert.Equal(t, uint64(1), stats.BreakerClosed)
	assert.Equal(t, uint64(5), stats.ShortCircuited)
}

func TestE2EResolverCachesAndCoalescesOnARealServer(t *testing.T) {
	t.Parallel()

	srv := startServer(t)
	ctx := context.Background()

	require.NoError(t, srv.client.SAdd(ctx, "res:{beta}", "alice").Err())

	inner, err := redissegment.New(srv.client, &redissegment.Options{MissingKey: redissegment.MissingUnknown,
		Timeout:  5 * time.Second,
		Segments: map[string]redissegment.SegmentOptions{segBeta: {Key: "res:{beta}"}},
	})
	require.NoError(t, err)

	res, err := resolver.New(inner, &resolver.Options{})
	require.NoError(t, err)

	// warm the pool: a new connection sends its handshake in pipelines
	require.NoError(t, res.Contains(ctx, []segment.Lookup{lk(segBeta, "warm")}))

	before := srv.pipelines.Load()
	entered := srv.fault.Hang()
	t.Cleanup(srv.fault.Release) // a failed assertion must not leave the callers waiting

	const callers = 8

	var wg sync.WaitGroup

	got := make([][]segment.State, callers)
	errs := make([]error, callers)

	wg.Go(func() {
		lookups := []segment.Lookup{lk(segBeta, idAlice)}
		errs[0] = res.Contains(ctx, lookups)
		got[0] = states(lookups)
	})

	// the first caller's pipeline is at the client
	select {
	case <-entered:
	case <-boundedCtx(t).Done():
		require.FailNow(t, "the pipeline of the first caller never reached the client")
	}

	for caller := 1; caller < callers; caller++ {
		wg.Go(func() {
			lookups := []segment.Lookup{lk(segBeta, idAlice)}
			errs[caller] = res.Contains(ctx, lookups)
			got[caller] = states(lookups)
		})
	}

	require.Eventually(
		t,
		func() bool { return res.Status().Segments[segBeta].Joined == callers-1 },
		10*time.Second,
		time.Millisecond,
	)

	srv.fault.Release()
	wg.Wait()

	for caller := range callers {
		require.NoError(t, errs[caller])
		assert.Equal(t, []segment.State{segment.Member}, got[caller])
	}

	assert.Equal(t, int64(1), srv.pipelines.Load()-before, "eight callers, one round trip")

	again := []segment.Lookup{lk(segBeta, idAlice)}
	require.NoError(t, res.Contains(ctx, again))
	assert.Equal(t, int64(1), srv.pipelines.Load()-before, "the next call is served from the cache")
}

func BenchmarkE2EContains(b *testing.B) {
	srv := startServer(b)
	ctx := context.Background()

	require.NoError(b, srv.client.SAdd(ctx, "bench:{beta}", "id0", "id1", "id2", "id3").Err())

	for _, atomicTx := range []bool{false, true} {
		b.Run("atomic="+strconv.FormatBool(atomicTx), func(b *testing.B) {
			prov, err := redissegment.New(srv.client, &redissegment.Options{MissingKey: redissegment.MissingUnknown,
				Atomic: atomicTx, Timeout: 5 * time.Second,
				Segments: map[string]redissegment.SegmentOptions{segBeta: {Key: "bench:{beta}"}},
			})
			require.NoError(b, err)

			lookups := []segment.Lookup{lk(segBeta, "id0"), lk(segBeta, "id1"), lk(segBeta, "nope"), lk(segBeta, "id3")}

			b.ReportAllocs()

			for b.Loop() {
				_ = prov.Contains(ctx, lookups)
			}
		})
	}
}
