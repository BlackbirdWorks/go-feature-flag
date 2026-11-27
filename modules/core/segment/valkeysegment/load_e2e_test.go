//go:build docker

package valkeysegment_test

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

const (
	loadIDSpace   = 400 // ids m0..m399, the even ones are members
	loadTotal     = 12000
	loadMargin    = time.Second // the most that a call may take beyond its timeout
	loadSteadyTTL = 5 * time.Second
	loadSlowTTL   = 100 * time.Millisecond
	loadPauseMS   = "300"
	loadRecovery  = 200
)

// loadSize is the batch size of a call: 1, 10 or 100 ids.
func loadSize(n int) int { return []int{1, 10, 100}[n%3] }

// loadProvider builds a provider of one type and mode over a store.
type loadProvider struct {
	build func(tb testing.TB, srv *e2eServer, timeout time.Duration) (segment.Provider, func())
	name  string
}

func loadProviders() []loadProvider {
	valkeyMode := func(mode valkeysegment.Pipelining) loadProvider {
		return loadProvider{
			name: "valkey/" + string(mode),
			build: func(tb testing.TB, srv *e2eServer, timeout time.Duration) (segment.Provider, func()) {
				tb.Helper()

				opt := srv.option(plainDial)
				opt.PipelineMultiplex = 0 // the default of valkey-go: the load uses several connections

				prov, err := valkeysegment.Dial(opt, &valkeysegment.Options{
					Timeout: timeout, Pipelining: mode, Segments: loadSegments(),
				})
				require.NoError(tb, err)

				return prov, func() { shutdownProvider(tb, prov) }
			},
		}
	}

	return []loadProvider{
		{
			name: "redis",
			build: func(tb testing.TB, srv *e2eServer, timeout time.Duration) (segment.Provider, func()) {
				tb.Helper()

				prov, err := redissegment.Dial(
					&redis.UniversalOptions{Addrs: []string{srv.address}},
					&redissegment.Options{
						Timeout: timeout,
						Segments: map[string]redissegment.SegmentOptions{
							segBeta: {Key: keyBeta}, segFlags: {Layout: redissegment.LayoutKeys, Key: flagPrefix},
						},
					},
				)
				require.NoError(tb, err)

				return prov, func() { shutdownProvider(tb, prov) }
			},
		},
		valkeyMode(valkeysegment.PipeliningAlways),
		valkeyMode(valkeysegment.PipeliningAuto),
		valkeyMode(valkeysegment.PipeliningOff),
	}
}

func loadSegments() map[string]valkeysegment.SegmentOptions {
	return map[string]valkeysegment.SegmentOptions{
		segBeta: {Key: keyBeta}, segFlags: {Layout: valkeysegment.LayoutKeys, Key: flagPrefix},
	}
}

func shutdownProvider(tb testing.TB, prov segment.Provider) {
	tb.Helper()

	stopper, ok := prov.(segment.Shutdowner)
	require.True(tb, ok)

	ctx, cancel := context.WithTimeout(context.Background(), shutdownBound)
	defer cancel()

	assert.NoError(tb, stopper.Shutdown(ctx))
}

func idOf(n int) string { return "m" + strconv.Itoa(n) }

// seedLoad loads the members m0, m2, ... of both layouts.
func seedLoad(tb testing.TB, client valkey.Client) {
	tb.Helper()

	members := make([]string, 0, loadIDSpace/2)
	sets := make([]valkey.Completed, 0, loadIDSpace/2)
	builder := client.B()

	for n := 0; n < loadIDSpace; n += 2 {
		members = append(members, idOf(n))
		sets = append(sets, builder.Set().Key(flagPrefix+idOf(n)).Value("1").Build())
	}

	load(tb, client, keyBeta, members...)

	for _, res := range client.DoMulti(context.Background(), sets...) {
		require.NoError(tb, res.Error())
	}
}

// loadStats is what a run of the load saw.
type loadStats struct {
	classes map[string]int
	latency []time.Duration
	elapsed time.Duration
	wrong   int64
	unknown int64
	failed  int64
	maxCall time.Duration
	allocs  uint64
	calls   int64
	lookups int64
}

func classOf(err error) string {
	if vErr, ok := errors.AsType[*valkeysegment.LookupError](err); ok {
		return vErr.Class.String()
	}

	if rErr, ok := errors.AsType[*redissegment.LookupError](err); ok {
		return rErr.Class.String()
	}

	return "other"
}

// runLoad makes total calls over the goroutines, with mixed batch sizes and both layouts, and checks every answer
// against the seeded membership. atMid runs once, on the goroutine that makes the call after a third of them.
func runLoad(tb testing.TB, prov segment.Provider, goroutines, total int, atMid func()) *loadStats {
	tb.Helper()

	var (
		wrong, unknown, failed, done, lookups atomic.Int64
		midOnce                               sync.Once
		wg                                    sync.WaitGroup
		mu                                    sync.Mutex
	)

	stats := &loadStats{classes: map[string]int{}}
	perG := total / goroutines
	before, after := runtime.MemStats{}, runtime.MemStats{}

	runtime.ReadMemStats(&before)

	started := time.Now()

	for gor := range goroutines {
		wg.Go(func() {
			own := make([]time.Duration, 0, perG)

			for i := range perG {
				if atMid != nil && done.Load() >= int64(total/3) {
					midOnce.Do(atMid)
				}

				size := loadSize(gor + i)
				calls := make([]segment.Lookup, size)
				ids := make([]int, size)

				for pos := range calls {
					ids[pos] = (gor*131 + i*17 + pos*7) % loadIDSpace

					name := segBeta
					if pos%2 == 1 {
						name = segFlags
					}

					calls[pos] = lk(name, idOf(ids[pos]))
				}

				begin := time.Now()
				err := prov.Contains(context.Background(), calls)
				took := time.Since(begin)

				own = append(own, took)

				done.Add(1)
				lookups.Add(int64(size))

				for pos := range calls {
					want := segment.NotMember
					if ids[pos]%2 == 0 {
						want = segment.Member
					}

					if calls[pos].State == segment.Unknown {
						unknown.Add(1)
					} else if calls[pos].State != want {
						wrong.Add(1)
					}
				}

				if err != nil {
					failed.Add(1)

					mu.Lock()
					stats.classes[classOf(err)]++
					mu.Unlock()
				}
			}

			mu.Lock()

			stats.latency = append(stats.latency, own...)

			mu.Unlock()
		})
	}

	wg.Wait()

	stats.elapsed = time.Since(started)

	runtime.ReadMemStats(&after)

	stats.allocs = after.Mallocs - before.Mallocs
	stats.calls = done.Load()
	stats.lookups = lookups.Load()
	stats.wrong, stats.unknown, stats.failed = wrong.Load(), unknown.Load(), failed.Load()
	slices.Sort(stats.latency)

	if n := len(stats.latency); n > 0 {
		stats.maxCall = stats.latency[n-1]
	}

	return stats
}

func (s *loadStats) percentile(p float64) time.Duration {
	if len(s.latency) == 0 {
		return 0
	}

	return s.latency[int(float64(len(s.latency)-1)*p)]
}

func (s *loadStats) report(tb testing.TB, label string) {
	tb.Helper()

	tb.Logf("%s: %d calls, %.0f calls/s, p50 %v, p99 %v, max %v, %.0f allocs/call, unknown %d, failed %d %v",
		label, s.calls, float64(s.calls)/s.elapsed.Seconds(), s.percentile(0.50), s.percentile(0.99), s.maxCall,
		float64(s.allocs)/float64(s.calls), s.unknown, s.failed, s.classes)
}

// requireRecovered checks that the same provider answers everything correctly again.
func requireRecovered(t *testing.T, prov segment.Provider) {
	t.Helper()

	// the first calls after a fault may still meet the dead connection
	require.Eventually(t, func() bool {
		calls := []segment.Lookup{lk(segBeta, idOf(2))}

		return prov.Contains(context.Background(), calls) == nil && calls[0].State == segment.Member
	}, 30*time.Second, 50*time.Millisecond, "the provider does not recover")

	stats := runLoad(t, prov, 1, loadRecovery, nil)

	assert.Zero(t, stats.wrong, "wrong answers after the recovery")
	assert.Zero(t, stats.failed, "failed calls after the recovery")
	assert.Zero(t, stats.unknown, "unknown answers after the recovery")
}

// goroutinesBack waits, without starting a goroutine of its own, until the goroutine count is back to the baseline.
func goroutinesBack(t *testing.T, baseline int) {
	t.Helper()

	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()

	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	for runtime.NumGoroutine() > baseline {
		select {
		case <-tick.C:
		case <-deadline.C:
			assert.LessOrEqual(t, runtime.NumGoroutine(), baseline, "goroutines left after the run")

			return
		}
	}
}

// The load tests are not parallel: they measure a whole process, and they count its goroutines.
//
//nolint:paralleltest // the throughput and the goroutine count are process wide
func TestE2ELoad(t *testing.T) {
	srv := startServerImage(t, valkeyImage)
	admin := seed(t, srv.option(plainDial))

	seedLoad(t, admin)

	allowed := []string{valkeysegment.ClassUnavailable.String(), valkeysegment.ClassTimeout.String()}
	faults := []struct {
		inject  func()
		name    string
		timeout time.Duration
	}{
		{
			name:    "client kill of every connection",
			timeout: loadSteadyTTL,
			inject: func() {
				res := admin.Do(
					context.Background(),
					admin.B().Arbitrary("CLIENT").Args("KILL", "TYPE", "normal").Build(),
				)
				assert.NoError(t, res.Error())
			},
		},
		{
			name:    "slow server so that deadlines fire",
			timeout: loadSlowTTL,
			inject: func() {
				res := admin.Do(
					context.Background(),
					admin.B().Arbitrary("CLIENT").Args("PAUSE", loadPauseMS, "ALL").Build(),
				)
				assert.NoError(t, res.Error())
			},
		},
	}

	for _, each := range loadProviders() {
		t.Run(each.name, func(t *testing.T) {
			for _, goroutines := range []int{64, 256} {
				baseline := runtime.NumGoroutine()
				prov, stop := each.build(t, srv, loadSteadyTTL)
				stats := runLoad(t, prov, goroutines, loadTotal, nil)

				stats.report(t, each.name+" steady "+strconv.Itoa(goroutines)+" goroutines")
				assert.Zero(t, stats.wrong, "wrong answers")
				assert.Zero(t, stats.unknown, "unknown answers without a fault")
				assert.Zero(t, stats.failed, "failed calls without a fault: %v", stats.classes)
				assert.Less(t, stats.maxCall, loadSteadyTTL+loadMargin, "a call went past its deadline")

				stop()
				goroutinesBack(t, baseline)
			}

			for _, fault := range faults {
				t.Run(fault.name, func(t *testing.T) {
					baseline := runtime.NumGoroutine()
					prov, stop := each.build(t, srv, fault.timeout)
					stats := runLoad(t, prov, 64, loadTotal, fault.inject)

					stats.report(t, each.name+" "+fault.name)
					assert.Zero(t, stats.wrong, "wrong answers under a fault")
					assert.Less(t, stats.maxCall, fault.timeout+loadMargin, "a call went past its deadline")

					for class := range stats.classes {
						assert.Contains(t, allowed, class, "a class that the fault should not produce")
					}

					requireRecovered(t, prov)
					stop()
					goroutinesBack(t, baseline)
				})
			}
		})
	}
}
