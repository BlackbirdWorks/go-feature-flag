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
	helpPresent = "# HELP semaphore_flags_rejected Rejected flags present in the running generation, by what became " +
		"of them: kept or dropped. Unlike the counter, it falls when the rejection is fixed."
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

	wantMetrics := func(kept, dropped, presentKept, presentDropped, ok, invalid int) {
		t.Helper()

		want := fmt.Sprintf(`
%s
# TYPE semaphore_flag_rejected_total counter
semaphore_flag_rejected_total{reason="dropped"} %d
semaphore_flag_rejected_total{reason="kept"} %d
%s
# TYPE semaphore_flags_rejected gauge
semaphore_flags_rejected{reason="dropped"} %d
semaphore_flags_rejected{reason="kept"} %d
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
`, helpRejected, dropped, kept, helpPresent, presentDropped, presentKept, helpObjectAge, invalid, ok)

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
	wantMetrics(0, 1, 0, 1, 3, 0)

	// generation 2: the same objects again, the rejection is still there and counts nothing more
	step(nil)
	wantMetrics(0, 1, 0, 1, 3, 0)

	// generation 3: a new version of the object, in which the flag that was valid is kept and the other dropped
	// again, and an object that does not decode
	step(map[string]string{flagsB: flagsAllBad, flagsC: "not: [valid"})
	wantMetrics(1, 2, 1, 1, 3, 1)

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

// TestNew_AdminKeyCollision: a key that is both an admin key and a flag-set key grants nothing while the collision
// exists (401 on the admin endpoints and on evaluation, as for an unknown key); the other keys work, the relay stays
// ready, and a reload of the admin keys changes it without republishing, in both directions.
func TestNew_AdminKeyCollision(t *testing.T) {
	t.Parallel()

	const second = "second-admin"

	logs := &lockedBuffer{}
	rig := newV2With(t, v2Options{cold: true, logger: newTestLogger(logs)})

	rig.sets = []controlplane.FlagSet{
		{Name: setName, Sources: []string{flagsObject}, APIKeys: []string{adminKey, tenantKey}},
	}
	rig.publish(t)
	rig.tick(t)
	rig.awaitGeneration(t, 1)
	rig.setAdmin(t, adminKey+", "+second)

	evaluate := func(key string) int {
		status, _, _ := do(t, http.MethodPost, rig.url+"/ofrep/v1/evaluate/flags/bool-flag", `{"context":{}}`,
			keyHeader(key))

		return status
	}

	// the admin endpoint answers 401 to a key that is not an admin key and something else (here a refusal of the
	// empty body) to an admin key
	admin := func(key string) int {
		status, _, _ := do(t, http.MethodPost, rig.url+"/admin/v1/flags/validate", "", keyHeader(key))

		return status
	}

	info := func() model.ConfigInfo {
		_, _, body := do(t, http.MethodGet, rig.url+"/info", "", nil)

		var out model.InfoResponse
		require.NoError(t, json.Unmarshal(body, &out))
		require.NotNil(t, out.Config)

		// the key is "admin-key": its only legitimate appearance is inside the code "admin-key-collision"
		assert.Equal(t, strings.Count(string(body), "admin-key-collision"), strings.Count(string(body), adminKey))

		return *out.Config
	}

	problems := func(cfg *model.ConfigInfo) []string {
		var out []string

		for _, set := range cfg.FlagSets {
			for _, p := range set.Problems {
				out = append(out, set.Name+":"+p.Code)
			}
		}

		return out
	}

	collision := []string{setName + ":" + string(flagruntime.CodeAdminKeyCollision)}

	tests := []struct {
		name           string
		admin          string
		wantReadyState string
		wantProblems   []string
		wantEvaluate   int
		wantAdmin      int
		wantDisabled   int
		wantSecondOK   bool
	}{
		{
			name: "collision", admin: adminKey + ", " + second, wantEvaluate: http.StatusUnauthorized,
			wantAdmin: http.StatusUnauthorized, wantSecondOK: true, wantProblems: collision, wantDisabled: 1,
			wantReadyState: corehttp.ReadyDegraded,
		},
		{
			name: "rotated", admin: second, wantEvaluate: http.StatusOK, wantAdmin: http.StatusUnauthorized,
			wantSecondOK: true, wantDisabled: 0, wantReadyState: corehttp.ReadyServing,
		},
		{
			name: "introduced by reload", admin: adminKey + ", " + second, wantEvaluate: http.StatusUnauthorized,
			wantAdmin: http.StatusUnauthorized, wantSecondOK: true, wantProblems: collision, wantDisabled: 1,
			wantReadyState: corehttp.ReadyDegraded,
		},
		{
			name: "every admin key disabled", admin: adminKey, wantEvaluate: http.StatusUnauthorized,
			wantAdmin: http.StatusUnauthorized, wantProblems: collision, wantDisabled: 1,
			wantReadyState: corehttp.ReadyDegraded,
		},
	}

	// the steps depend on one another: they run in order, in this goroutine
	for _, tt := range tests {
		if tt.name != "collision" {
			rig.setAdmin(t, tt.admin)
		}

		cfg := info()

		assert.Equal(t, tt.wantEvaluate, evaluate(adminKey), tt.name)
		assert.Equal(t, http.StatusOK, evaluate(tenantKey), tt.name)
		assert.Equal(t, tt.wantAdmin, admin(adminKey), tt.name)
		assert.Equal(t, tt.wantSecondOK, admin(second) != http.StatusUnauthorized, tt.name)
		assert.Equal(t, tt.wantProblems, problems(&cfg), tt.name)
		assert.Equal(t, tt.wantDisabled, cfg.DisabledAdminKeys, tt.name)
		rig.wantProbe(t, "/readyz", http.StatusOK, `{"ready":true,"state":"`+tt.wantReadyState+`"}`)
	}

	warnings := 0

	for line := range strings.SplitSeq(logs.all(), "\n") {
		if strings.Contains(line, "an admin key is disabled") {
			warnings++

			assert.Contains(t, line, "level=ERROR")
			assert.Contains(t, line, "flagsets=tenant")
		}
	}

	assert.Equal(t, 2, warnings, "once for each time the collision is new")
	assert.Equal(t, strings.Count(logs.all(), "admin-key-collision"), strings.Count(logs.all(), adminKey))
	assert.Equal(t, uint64(1), rig.core.Status().Source.Runtime.Generation, "no generation was rejected")
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

	withRejected := func(obj *flagruntime.ObjectStatus) *flagruntime.ObjectStatus {
		obj.Rejected = []flagruntime.FlagRejection{{Flag: "a", Kept: true}, {Flag: "b"}, {Flag: "c"}}

		return obj
	}

	tests := []struct {
		rejected    map[string]uint64
		name        string
		objects     []*flagruntime.ObjectStatus
		wantAge     float64
		wantKept    uint64
		wantDropped uint64
		// wantPresentKept and wantPresentDropped are the rejected flags the objects hold.
		wantPresentKept, wantPresentDropped int
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
			name: "counted",
			objects: []*flagruntime.ObjectStatus{
				withRejected(object("a", flagruntime.ObjectInvalid, 5*time.Second)),
			},
			rejected:           map[string]uint64{relaycore.RejectedKept: 3, relaycore.RejectedDropped: 2},
			wantAge:            5,
			wantKept:           3,
			wantDropped:        2,
			wantPresentKept:    1,
			wantPresentDropped: 2,
			wantStates:         [5]int{1, 0, 0, 0, 0},
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
# TYPE semaphore_flags_rejected gauge
semaphore_flags_rejected{reason="dropped"} %d
semaphore_flags_rejected{reason="kept"} %d
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
`, helpRejected, tt.wantDropped, tt.wantKept, helpPresent, tt.wantPresentDropped, tt.wantPresentKept,
				helpObjectAge, tt.wantAge,
				tt.wantStates[0], tt.wantStates[1], tt.wantStates[2], tt.wantStates[3], tt.wantStates[4])

			reg := prom.NewPedanticRegistry()
			require.NoError(t, reg.Register(col))
			require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(want),
				"semaphore_flag_rejected_total", "semaphore_flags_rejected", "semaphore_object_status",
				"semaphore_object_oldest_unapplied_age_seconds"))
		})
	}
}
