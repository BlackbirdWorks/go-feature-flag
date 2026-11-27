//go:build docker

package valkeysegment_test

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/clock"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmenttest"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

// seed loads the data of the suite into the server in the layout of the provider.
func seed(
	t *testing.T,
	srv *server,
	tag string,
	data segmenttest.Data,
	layout valkeysegment.Layout,
) map[string]valkeysegment.SegmentOptions {
	t.Helper()

	ctx := context.Background()
	segs := map[string]valkeysegment.SegmentOptions{}

	for name, ids := range data {
		if layout == valkeysegment.LayoutSet {
			key := "contract:" + tag + ":{" + name + "}"
			segs[name] = valkeysegment.SegmentOptions{Key: key}

			require.NoError(t, srv.client.Del(ctx, key).Err())

			args := make([]any, len(ids))
			for i, id := range ids {
				args[i] = id
			}

			require.NoError(t, srv.client.SAdd(ctx, key, args...).Err())

			continue
		}

		prefix := "contract:" + tag + ":" + name + ":"
		segs[name] = valkeysegment.SegmentOptions{Layout: valkeysegment.LayoutKeys, Key: prefix}

		for _, id := range ids {
			require.NoError(t, srv.client.Set(ctx, prefix+id, "1", 0).Err())
		}
	}

	return segs
}

func TestE2EConformance(t *testing.T) {
	t.Parallel()

	srv := startServer(t)

	var cases atomic.Int64

	tests := []struct {
		name   string
		layout valkeysegment.Layout
		opts   valkeysegment.Options
	}{
		{name: "set layout", layout: valkeysegment.LayoutSet},
		{name: "set layout atomic", layout: valkeysegment.LayoutSet, opts: valkeysegment.Options{Atomic: true}},
		{name: nameKeysLayout, layout: valkeysegment.LayoutKeys},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			segmenttest.Run(t, func(data segmenttest.Data) (segmenttest.Fixture, error) {
				// each case has its own client, hook and key space on the shared server
				caseSrv := &server{container: srv.container, address: srv.address, fault: &faultHook{}}
				caseSrv.pipelines = &caseSrv.fault.count
				caseSrv.client = caseSrv.newClient(t, srv.address)

				opts := tt.opts
				opts.Segments = seed(
					t,
					caseSrv,
					strconv.FormatInt(cases.Add(1), 10),
					data,
					tt.layout,
				) // a key space of its own: the cases run in parallel

				prov, err := valkeysegment.New(caseSrv.client, opts)
				if err != nil {
					return segmenttest.Fixture{}, err
				}

				return segmenttest.Fixture{Provider: prov, Backend: caseSrv.fault, Close: caseSrv.fault.Release}, nil
			})
		})
	}
}

func TestE2EAtomicSetSwapNeverReadsAMissingKey(t *testing.T) {
	t.Parallel()

	srv := startServer(t)
	ctx := context.Background()
	live := "atomic:{beta}"

	require.NoError(t, srv.client.SAdd(ctx, live, "stable").Err())

	prov, err := valkeysegment.New(srv.client, valkeysegment.Options{
		Atomic: true, Timeout: 5 * time.Second,
		Segments: map[string]valkeysegment.SegmentOptions{segBeta: {Key: live}},
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

	prov, err := valkeysegment.New(srv.client, valkeysegment.Options{
		Segments: map[string]valkeysegment.SegmentOptions{segBeta: {Key: "life:{beta}"}, segGone: {Key: "life:{gone}"}},
	})
	require.NoError(t, err)

	require.NoError(t, prov.Init(ctx))
	assert.Equal(t, valkeysegment.Status{Ready: true}, prov.Status())

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

func TestE2EBreakerOpensWithoutDialingAndRecovers(t *testing.T) {
	t.Parallel()

	srv := startServer(t)
	ctx := context.Background()

	require.NoError(t, srv.client.SAdd(ctx, "breaker:{beta}", "alice").Err())

	proxy := newOutageProxy(t, srv.address)
	client := srv.newClient(t, proxy.addr())

	inner, err := valkeysegment.New(client, valkeysegment.Options{
		Timeout:  2 * time.Second,
		Segments: map[string]valkeysegment.SegmentOptions{segBeta: {Key: "breaker:{beta}"}},
	})
	require.NoError(t, err)

	counters := &resolver.Counters{}
	res, err := resolver.New(inner, &resolver.Options{
		Observer: counters,
		Segments: map[string]resolver.SegmentOptions{
			segBeta: {NoCache: true, BreakerFailures: 3, BreakerCooldown: 50 * time.Millisecond},
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
		require.ErrorIs(t, err, valkeysegment.ErrUnavailable)
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

	for range 200 { // the clock steps on every read: this is more than the cool-down
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

	inner, err := valkeysegment.New(srv.client, valkeysegment.Options{
		Timeout:  5 * time.Second,
		Segments: map[string]valkeysegment.SegmentOptions{segBeta: {Key: "res:{beta}"}},
	})
	require.NoError(t, err)

	res, err := resolver.New(inner, &resolver.Options{})
	require.NoError(t, err)

	// warm the pool: a new connection sends its handshake in pipelines
	require.NoError(t, res.Contains(ctx, []segment.Lookup{lk(segBeta, "warm")}))

	before := srv.pipelines.Load()
	entered := srv.fault.Hang()

	const callers = 8

	var wg sync.WaitGroup

	got := make([][]segment.State, callers)
	errs := make([]error, callers)

	wg.Go(func() {
		lookups := []segment.Lookup{lk(segBeta, idAlice)}
		errs[0] = res.Contains(ctx, lookups)
		got[0] = states(lookups)
	})

	<-entered // the first caller's pipeline is at the client

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
			prov, err := valkeysegment.New(srv.client, valkeysegment.Options{
				Atomic: atomicTx, Timeout: 5 * time.Second,
				Segments: map[string]valkeysegment.SegmentOptions{segBeta: {Key: "bench:{beta}"}},
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
