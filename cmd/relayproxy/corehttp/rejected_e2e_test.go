package corehttp_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/controlsource"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

// lockedBuffer is the writer of the injected log handler of the tests.
type lockedBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(data)
	if err != nil {
		return n, fmt.Errorf("write the log: %w", err)
	}

	return n, nil
}

// reloadLines returns the lines of the reload log, in order.
func (b *lockedBuffer) reloadLines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	var out []string

	for line := range strings.SplitSeq(b.buf.String(), "\n") {
		if strings.Contains(line, `msg="runtime reload"`) {
			out = append(out, line)
		}
	}

	return out
}

func (b *lockedBuffer) all() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func newTestLogger(buf *lockedBuffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// The help lines of the new metrics, as the collector declares them.
const (
	helpRejected = "# HELP semaphore_flag_rejected_total Flags rejected at load, by what became of them: kept " +
		"(the last valid definition keeps serving) or dropped (none to fall back on). Counted once per rejected " +
		"flag, in the applied generation that brought the rejection."
	helpObjectAge = "# HELP semaphore_object_oldest_unapplied_age_seconds Seconds since the oldest object that is " +
		"not ok left that state; 0 when every object is ok."
)

const (
	// flagsWithBad has a valid flag and one without variations.
	flagsWithBad = "good:\n  variations: {a: 1}\n  defaultRule: {variation: a}\nbad:\n  defaultRule: {variation: a}\n"
	// flagsAllBad has the two flags of flagsWithBad, both invalid.
	flagsAllBad = "good:\n  defaultRule: {variation: a}\nbad:\n  defaultRule: {variation: a}\n"
	flagsB      = "flags-b"
	flagsC      = "flags-c"
)

// TestNewConfigCollector_RejectedFlags: an invalid flag is counted once, when the version of its object arrives, as
// dropped (nothing to fall back on) or kept (the last valid definition serves); the same content in a later
// generation counts nothing; the objects by state are exported; the log has one applied line per generation and
// no key.
func TestNewConfigCollector_RejectedFlags(t *testing.T) {
	t.Parallel()

	logs := &lockedBuffer{}
	rig := newV2With(t, v2Options{cold: true, logger: newTestLogger(logs)})

	step := func(flags map[string]string) {
		t.Helper()

		for name, data := range flags {
			rig.flags[name] = []byte(data)
		}

		rig.sets = []controlplane.FlagSet{
			{Name: setName, Sources: []string{flagsObject, flagsB}, APIKeys: []string{tenantKey}},
		}
		rig.publish(t)
		rig.tick(t)
		rig.awaitGeneration(t, rig.gen)
	}

	wantMetrics := func(kept, dropped, ok, invalid int) {
		t.Helper()

		want := fmt.Sprintf(`
%s
# TYPE semaphore_flag_rejected_total counter
semaphore_flag_rejected_total{reason="dropped"} %d
semaphore_flag_rejected_total{reason="kept"} %d
%s
# TYPE semaphore_object_oldest_unapplied_age_seconds gauge
semaphore_object_oldest_unapplied_age_seconds 0
# HELP semaphore_object_status Source objects by state: ok, stale, missing, pending, invalid.
# TYPE semaphore_object_status gauge
semaphore_object_status{state="invalid"} %d
semaphore_object_status{state="missing"} 0
semaphore_object_status{state="ok"} %d
semaphore_object_status{state="pending"} 0
semaphore_object_status{state="stale"} 0
`, helpRejected, dropped, kept, helpObjectAge, invalid, ok)

		require.NoError(t, testutil.GatherAndCompare(
			rig.metrics.Registry,
			strings.NewReader(want),
			"semaphore_flag_rejected_total",
			"semaphore_object_status",
			"semaphore_object_oldest_unapplied_age_seconds",
		))
	}

	// generation 1: the invalid flag has nothing to fall back on
	step(map[string]string{flagsB: flagsWithBad})
	wantMetrics(0, 1, 3, 0)

	// generation 2: the same objects again, the rejection is still there and counts nothing more
	step(nil)
	wantMetrics(0, 1, 3, 0)

	// generation 3: a new version of the object, in which the flag that was valid is kept and the other dropped
	// again, and an object that does not decode
	step(map[string]string{flagsB: flagsAllBad, flagsC: "not: [valid"})
	wantMetrics(1, 2, 3, 1)

	lines := logs.reloadLines()
	line := func(generation int) string {
		for _, l := range lines {
			if strings.Contains(l, fmt.Sprintf("generation=%d ", generation)) {
				return l
			}
		}

		return ""
	}

	assert.Contains(t, line(1), "rejected=1")
	assert.Contains(t, line(1), "objects.ok=3")
	assert.Contains(t, line(2), "flags.created=0 flags.updated=0 flags.deleted=0")
	assert.Contains(t, line(2), "rejected=1")
	assert.Contains(t, line(3), "objects.invalid=1")
	assert.Contains(t, line(3), "rejected=2")

	for _, secret := range []string{tenantKey, adminKey} {
		assert.NotContains(t, logs.all(), secret)
	}
}

// TestNew_AdminKeyCollision: a generation whose flag-set key equals the admin key is rejected, the previous generation
// keeps serving, and neither the log nor /info shows the key.
func TestNew_AdminKeyCollision(t *testing.T) {
	t.Parallel()

	logs := &lockedBuffer{}
	rig := newV2With(t, v2Options{cold: true, logger: newTestLogger(logs)})

	rig.publish(t)
	rig.tick(t)
	rig.awaitGeneration(t, 1)

	rig.sets = []controlplane.FlagSet{{Name: setName, Sources: []string{flagsObject}, APIKeys: []string{adminKey}}}
	rig.publish(t)
	rig.tick(t)
	rig.awaitRejected(t, 2)

	code, _, body := do(t, http.MethodGet, rig.url+"/info", "", nil)
	require.Equal(t, http.StatusOK, code)

	var info model.InfoResponse
	require.NoError(t, json.Unmarshal(body, &info))
	require.NotNil(t, info.Config)
	require.NotNil(t, info.Config.LastRejected)

	assert.Equal(t, flagruntime.CodeAdminKeyCollision, info.Config.LastRejected.Code)
	assert.Equal(t, uint64(2), info.Config.LastRejected.Generation)
	assert.Equal(t, uint64(1), info.Config.Generation, "the previous generation stays")
	assert.Equal(t, string(flagruntime.OutcomeRejected), info.Config.LastReload.Outcome)

	// the previous generation still serves its key and the collision never made the admin key a flag-set key
	status, _, _ := do(t, http.MethodPost, rig.url+"/ofrep/v1/evaluate/flags/bool-flag", `{"context":{}}`,
		keyHeader(tenantKey))
	assert.Equal(t, http.StatusOK, status)

	rejected := ""

	for _, line := range logs.reloadLines() {
		if strings.Contains(line, "outcome=rejected") {
			rejected = line
		}
	}

	assert.Contains(t, rejected, "code=admin-key-collision")
	assert.Contains(t, rejected, "generation=2")
	assert.Contains(t, rejected, "flagSets[tenant].apiKeys")

	// the key is "admin-key": its only legitimate appearance is inside the code "admin-key-collision"
	for name, out := range map[string]string{"log": logs.all(), "info": string(body)} {
		assert.Equal(t, strings.Count(out, "admin-key-collision"), strings.Count(out, adminKey), name)
	}
}

type fixedSource struct{ status relaycore.Status }

func (f *fixedSource) Status() relaycore.Status { return f.status }

// TestConfigCollector_Collect: the age of the oldest object that is not ok, the objects by state and the rejected
// flags come from the status that was built with the runtime.
func TestConfigCollector_Collect(t *testing.T) {
	t.Parallel()

	now := clock.Now()
	object := func(name string, state flagruntime.ObjectState, age time.Duration) *flagruntime.ObjectStatus {
		status := &flagruntime.ObjectStatus{Name: name, State: state}
		if state != flagruntime.ObjectOK {
			status.Since = now.Add(-age)
		}

		return status
	}

	tests := []struct {
		rejected    map[string]uint64
		name        string
		objects     []*flagruntime.ObjectStatus
		wantAge     float64
		wantKept    uint64
		wantDropped uint64
		// wantStates are the objects of the states invalid, missing, ok, pending and stale.
		wantStates [5]int
	}{
		{
			name:       "all ok",
			objects:    []*flagruntime.ObjectStatus{object("a", flagruntime.ObjectOK, 0)},
			wantStates: [5]int{0, 0, 1, 0, 0},
		},
		{
			name: "oldest of two",
			objects: []*flagruntime.ObjectStatus{
				object(
					"a",
					flagruntime.ObjectStale,
					30*time.Second,
				),
				object("b", flagruntime.ObjectMissing, 90*time.Second),
				object("c", flagruntime.ObjectOK, 0),
			},
			wantAge: 90, wantStates: [5]int{0, 1, 1, 0, 1},
		},
		{
			name:     "counted",
			objects:  []*flagruntime.ObjectStatus{object("a", flagruntime.ObjectInvalid, 5*time.Second)},
			rejected: map[string]uint64{relaycore.RejectedKept: 3, relaycore.RejectedDropped: 2},
			wantAge:  5, wantKept: 3, wantDropped: 2, wantStates: [5]int{1, 0, 0, 0, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runtimeStatus := &flagruntime.Status{Objects: map[string]*flagruntime.ObjectStatus{}}
			for _, obj := range tt.objects {
				runtimeStatus.Objects[obj.Name] = obj
			}

			col := corehttp.NewConfigCollector(&fixedSource{status: relaycore.Status{
				Source:  controlsource.Status{Runtime: runtimeStatus},
				Reloads: relaycore.ReloadStats{FlagsRejected: tt.rejected},
			}})

			want := fmt.Sprintf(`
%s
# TYPE semaphore_flag_rejected_total counter
semaphore_flag_rejected_total{reason="dropped"} %d
semaphore_flag_rejected_total{reason="kept"} %d
%s
# TYPE semaphore_object_oldest_unapplied_age_seconds gauge
semaphore_object_oldest_unapplied_age_seconds %v
# HELP semaphore_object_status Source objects by state: ok, stale, missing, pending, invalid.
# TYPE semaphore_object_status gauge
semaphore_object_status{state="invalid"} %d
semaphore_object_status{state="missing"} %d
semaphore_object_status{state="ok"} %d
semaphore_object_status{state="pending"} %d
semaphore_object_status{state="stale"} %d
`, helpRejected, tt.wantDropped, tt.wantKept, helpObjectAge, tt.wantAge,
				tt.wantStates[0], tt.wantStates[1], tt.wantStates[2], tt.wantStates[3], tt.wantStates[4])

			reg := prom.NewPedanticRegistry()
			require.NoError(t, reg.Register(col))
			require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(want),
				"semaphore_flag_rejected_total", "semaphore_object_status",
				"semaphore_object_oldest_unapplied_age_seconds"))
		})
	}
}
