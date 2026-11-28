//go:build bench && docker

package segments_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/loadtest"
)

const (
	// The load test is local (task test-segment-load): it seeds 8 sets of 1,000,000 members into a real Valkey, which
	// takes more memory and time than the e2e shard of the pull requests should spend. Each variable below changes one
	// size: a smaller run (SEMAPHORE_SEGMENT_LOAD_MEMBERS=50000) is a smoke run.
	membersEnv  = "SEMAPHORE_SEGMENT_LOAD_MEMBERS"
	rateEnv     = "SEMAPHORE_SEGMENT_LOAD_RATE"
	secondsEnv  = "SEMAPHORE_SEGMENT_LOAD_SECONDS"
	segmentsMax = 8
	workers     = 16
	warmPool    = 100
	loadResolve = "segments:\n  resolver:\n    callTimeout: 5s\n" +
		"    default: {ttl: 5m, negativeTtl: 5m, maxEntries: 200000}\n"
	oneSnapshot = "  - {name: s0, connection: audiences, mode: snapshot, key: set0, " +
		"version: {key: set0-version, interval: 1s}}\n"
	loadConn    = "forceSingleClient: true, lookupTimeout: 1s, timeouts: {connect: 1s, io: 30s}"
	loadedBound = 5 * time.Minute
)

func envInt(tb testing.TB, name string, def int) int {
	tb.Helper()

	raw, ok := os.LookupEnv(name)
	if !ok {
		return def
	}

	value, err := strconv.Atoi(raw)
	require.NoError(tb, err, name)

	return value
}

// segmentCounts are the flags of the test: flag N has a rule that names N segments.
func segmentCounts() []int { return []int{1, 3, 8} }

func loadFlagName(segs int) string { return fmt.Sprintf("load-%d", segs) }

func loadFlags() string {
	var builder strings.Builder

	for _, count := range segmentCounts() {
		parts := make([]string, count)
		for j := range count {
			parts[j] = fmt.Sprintf(`targetingKey in segment("s%d")`, j)
		}

		fmt.Fprintf(
			&builder,
			"%s:\n  variations: {on: true, off: false}\n  targeting:\n    - {name: any, query: '%s', variation: on}\n"+
				"  defaultRule: {name: default, variation: off}\n",
			loadFlagName(count),
			strings.Join(parts, " or "),
		)
	}

	return builder.String()
}

func loadSegments(mode string) string {
	var builder strings.Builder

	for j := range segmentsMax {
		fmt.Fprintf(&builder, "  - {name: s%d, connection: audiences, mode: %s, key: set%d", j, mode, j)

		if mode == "snapshot" {
			fmt.Fprintf(&builder, ", version: {key: set%d-version, interval: 1s}", j)
		}

		builder.WriteString("}\n")
	}

	return builder.String()
}

// loadTarget sends the requests of a phase: one request in four names a member of one of the flag's segments (the
// answer is true), the others a value that is in no set (false).
type loadTarget struct {
	rig      *rig
	flag     string
	segs     int
	members  int
	offset   int
	pool     int // 0: every request has a key of its own (a cold cache), else the keys repeat after pool requests
	requests atomic.Int64
}

var errWrongAnswer = errors.New("wrong answer")

func (l *loadTarget) Eval(_, i int) error {
	l.requests.Add(1)

	key, want := l.key(i)

	got, err := l.rig.eval(l.flag, key)
	if err != nil {
		return err
	}

	if got.Value != want || got.status() != "" {
		return fmt.Errorf("%w: %s: %+v", errWrongAnswer, key, got)
	}

	return nil
}

func (*loadTarget) Extra() map[string]int64 { return nil }
func (*loadTarget) Close()                  {}

func (l *loadTarget) key(i int) (string, bool) {
	if l.pool > 0 {
		i %= l.pool
	}

	if i%4 == 0 {
		return fmt.Sprintf("s%d-%d", (i/4)%l.segs, (l.offset+i)%l.members), true
	}

	return fmt.Sprintf("none-%d-%d", l.offset, i), false
}

type phaseResult struct {
	report      loadtest.PacedReport
	cmdsPerEval float64
	// callsPerEval is the resolver calls of a request (semaphore_segment_store_calls_total): one batch for the
	// request, which the cache may answer without a command.
	callsPerEval float64
	hitRatio     float64
}

// requestCommands are the commands that a lookup sends from the request path (the refresh of a snapshot reads with
// SSCAN and GET, off the request path).
func requestCommands(calls map[string]int64) int64 { return calls["smismember"] + calls["exists"] }

// runPhase sends rate requests per second for the given seconds and reports what the store and the resolver saw.
func runPhase(t *testing.T, backend *store, r *rig, tgt *loadTarget, rate, seconds int) phaseResult {
	t.Helper()

	if tgt.pool > 0 { // the warm phase: every key of the pool is asked once before the run
		for i := range tgt.pool {
			require.NoError(t, tgt.Eval(0, i))
		}
	}

	cmdsBefore := requestCommands(backend.calls(t))
	callsBefore, _ := r.metric(t, "semaphore_segment_store_calls_total", nil)
	hitBefore, _ := r.metric(t, "semaphore_segment_lookups_total", map[string]string{labelResult: "hit"})
	allBefore, _ := r.metric(t, "semaphore_segment_lookups_total", nil)

	tgt.requests.Store(0)

	report, err := loadtest.RunPaced(context.Background(), tgt, loadtest.PacedOptions{
		Rate: rate, Duration: time.Duration(seconds) * time.Second, Workers: workers,
	}, func(ctx context.Context, _ *loadtest.Windows) error {
		<-ctx.Done()

		return nil
	})
	require.NoError(t, err)

	requests := float64(tgt.requests.Load())
	cmdsAfter := requestCommands(backend.calls(t))
	callsAfter, _ := r.metric(t, "semaphore_segment_store_calls_total", nil)
	hitAfter, _ := r.metric(t, "semaphore_segment_lookups_total", map[string]string{labelResult: "hit"})
	allAfter, _ := r.metric(t, "semaphore_segment_lookups_total", nil)

	out := phaseResult{
		report:       report,
		cmdsPerEval:  float64(cmdsAfter-cmdsBefore) / requests,
		callsPerEval: (callsAfter - callsBefore) / requests,
	}
	if allAfter > allBefore {
		out.hitRatio = (hitAfter - hitBefore) / (allAfter - allBefore)
	}

	return out
}

func heapAfterGC() uint64 {
	runtime.GC()
	runtime.GC()

	var m runtime.MemStats

	runtime.ReadMemStats(&m)

	return m.HeapAlloc
}

func (backend *store) usedMemory(tb testing.TB) int64 {
	tb.Helper()

	text, err := backend.client.Do(tb.Context(), backend.client.B().Info().Section("memory").Build()).ToString()
	require.NoError(tb, err)

	for line := range strings.SplitSeq(text, "\n") {
		if raw, ok := strings.CutPrefix(strings.TrimSpace(line), "used_memory:"); ok {
			used, convErr := strconv.ParseInt(raw, 10, 64)
			require.NoError(tb, convErr)

			return used
		}
	}

	require.Fail(tb, "no used_memory in INFO")

	return 0
}

func logPhase(t *testing.T, mode, phase string, segs int, res *phaseResult) {
	t.Helper()

	rep := res.report.Outside
	t.Logf(
		"REPORT mode=%s phase=%s segments=%d requests=%d failures=%d commands/eval=%.2f resolverCalls/eval=%.2f hit=%.3f "+
			"p50=%s p99=%s max=%s",
		mode,
		phase,
		segs,
		rep.Requests,
		rep.Failures,
		res.cmdsPerEval,
		res.callsPerEval,
		res.hitRatio,
		rep.P50.Round(10*time.Microsecond),
		rep.P99.Round(10*time.Microsecond),
		rep.Max.Round(10*time.Microsecond),
	)
}

// TestSegmentLoad seeds 8 sets of 1,000,000 members into a real Valkey and sends a constant request rate (open loop)
// to a real relay in both modes, to flags that name 1, 3 and 8 segments. It asserts no failed request and the
// commands of the request path; the latencies, the memory and the load times are recorded (REPORT lines), never
// asserted: they depend on the machine.
func TestSegmentLoad(t *testing.T) {
	t.Setenv(serialMarker, "1")

	members := envInt(t, membersEnv, 1_000_000)
	rate := envInt(t, rateEnv, 300)
	seconds := envInt(t, secondsEnv, 5)

	backend := startStore(t)

	seedStart := time.Now()

	for j := range segmentsMax {
		backend.seed(t, fmt.Sprintf("set%d", j), fmt.Sprintf("s%d-", j), members)
	}

	t.Logf("REPORT seeded %d sets of %d members in %s, store memory %d bytes (%.1f bytes per member)", segmentsMax,
		members, time.Since(seedStart).Round(time.Millisecond), backend.usedMemory(t),
		float64(backend.usedMemory(t))/float64(segmentsMax*members))

	runLookup(t, backend, members, rate, seconds)
	runSnapshot(t, backend, members, rate, seconds)
}

func runLookup(t *testing.T, backend *store, members, rate, seconds int) {
	t.Helper()

	{
		r := startRig(t, &rigOptions{
			address: backend.address, settings: loadConn, resolver: loadResolve, flags: loadFlags(),
			segments: loadSegments("lookup"),
		})

		for _, segs := range segmentCounts() {
			// cold: no key repeats, the cache never answers; the batch is one call to the store for the request,
			// with one SMISMEMBER for each segment of the flag
			cold := &loadTarget{rig: r, flag: loadFlagName(segs), segs: segs, members: members, offset: segs * 1000}
			res := runPhase(t, backend, r, cold, rate, seconds)
			logPhase(t, "lookup", "cold", segs, &res)

			assert.Zero(t, res.report.Outside.Failures, "no request failed (cold, %d segments)", segs)
			assert.InDelta(t, float64(segs), res.cmdsPerEval, 0.01,
				"cold: one SMISMEMBER for each segment, sent in one call (%d segments)", segs)
			assert.InDelta(
				t,
				1.0,
				res.callsPerEval,
				0.01,
				"cold: one batched resolver call for each request (%d segments)",
				segs,
			)

			warm := &loadTarget{
				rig: r, flag: loadFlagName(segs), segs: segs, members: members, offset: segs * 1000, pool: warmPool,
			}
			res = runPhase(t, backend, r, warm, rate, seconds)
			logPhase(t, "lookup", "warm", segs, &res)

			assert.Zero(t, res.report.Outside.Failures, "no request failed (warm, %d segments)", segs)
			assert.Zero(t, res.cmdsPerEval, "warm: the cache answers, the store is not called (%d segments)", segs)
			assert.InDelta(t, 1.0, res.hitRatio, 0.001, "warm: every lookup is a cache hit (%d segments)", segs)
		}

		t.Logf("REPORT mode=lookup heap=%d bytes", heapAfterGC())
	}
}

func runSnapshot(t *testing.T, backend *store, members, rate, seconds int) {
	t.Helper()

	{
		// one set first: the time to the first load of 1 set, and the heap that it holds
		base := heapAfterGC()
		start := time.Now()

		one := startRig(t, &rigOptions{
			address: backend.address, settings: loadConn, resolver: loadResolve, flags: loadFlags(),
			segments: oneSnapshot,
		})

		awaitLoaded(t, one, 1)
		t.Logf("REPORT mode=snapshot first load of 1 set of %d members: %s (including the relay start)", members,
			time.Since(start).Round(time.Millisecond))

		held := heapAfterGC() - base
		t.Logf(
			"REPORT mode=snapshot heap of 1 set: %d bytes, %.1f bytes per member",
			held,
			float64(held)/float64(members),
		)

		one.Close()

		base = heapAfterGC()
		start = time.Now()

		r := startRig(t, &rigOptions{
			address: backend.address, settings: loadConn, resolver: loadResolve, flags: loadFlags(),
			segments: loadSegments("snapshot"),
		})

		awaitLoaded(t, r, segmentsMax)
		t.Logf("REPORT mode=snapshot first load of %d sets of %d members: %s", segmentsMax, members,
			time.Since(start).Round(time.Millisecond))

		held = heapAfterGC() - base
		t.Logf("REPORT mode=snapshot heap of %d sets: %d bytes, %.1f bytes per member", segmentsMax, held,
			float64(held)/float64(segmentsMax*members))

		for _, segs := range segmentCounts() {
			tgt := &loadTarget{
				rig: r, flag: loadFlagName(segs), segs: segs, members: members, offset: segs * 1000, pool: 0,
			}
			res := runPhase(t, backend, r, tgt, rate, seconds)
			logPhase(t, "snapshot", "steady", segs, &res)

			assert.Zero(t, res.report.Outside.Failures, "no request failed (%d segments)", segs)
			assert.Zero(t, res.cmdsPerEval, "the request path sends no command (%d segments)", segs)
			assert.Zero(t, res.callsPerEval, "the request path makes no resolver call (%d segments)", segs)
		}
	}
}

// awaitLoaded waits until the relay holds n snapshot sets.
func awaitLoaded(t *testing.T, r *rig, n int) {
	t.Helper()

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		loaded, _ := r.metric(t, "semaphore_segment_snapshot_sets", map[string]string{"state": "loaded"})
		assert.InDelta(collect, float64(n), loaded, 0.5, "loaded snapshot sets")
	}, loadedBound, 20*time.Millisecond, "the snapshot sets were not loaded")
}
