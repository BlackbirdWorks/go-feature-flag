package corehttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

const (
	bodyServing = `{"ready":true,"state":"serving"}`
	bodyWaiting = `{"ready":false,"reason":"waiting-for-manifest"}`
)

// probe asks an endpoint of the server and returns the status and the body.
func (rig *v2) probe(tb testing.TB, path string) (int, string) {
	tb.Helper()

	code, _, body := do(tb, http.MethodGet, rig.url+path, "", nil)

	return code, strings.TrimSpace(string(body))
}

// tick lets the reader do one round, and waits for it to end.
func (rig *v2) tick(tb testing.TB) {
	tb.Helper()

	select {
	case rig.sched.release <- struct{}{}:
	case <-time.After(waitBound):
		require.FailNow(tb, "the reader is not waiting for a tick")
	}

	rig.settle(tb)
}

func (rig *v2) wantProbe(tb testing.TB, path string, wantCode int, wantBody string) {
	tb.Helper()

	code, body := rig.probe(tb, path)
	assert.Equal(tb, wantCode, code, path)
	assert.JSONEq(tb, wantBody, body, path)
}

func TestProbes_ColdStartThenManifest(t *testing.T) {
	t.Parallel()

	rig := newV2With(t, v2Options{cold: true})

	rig.wantProbe(t, "/livez", http.StatusOK, `{"live":true}`)
	rig.wantProbe(t, "/startupz", http.StatusOK, `{"ready":true}`)
	rig.wantProbe(t, "/readyz", http.StatusServiceUnavailable, bodyWaiting)
	rig.wantProbe(t, "/health", http.StatusOK, `{"initialized":true}`)

	// the store is still empty: several rounds with backoff do not make it ready
	rig.tick(t)
	rig.wantProbe(t, "/readyz", http.StatusServiceUnavailable, bodyWaiting)

	rig.publish(t)
	rig.tick(t)
	rig.awaitGeneration(t, 1)

	rig.wantProbe(t, "/readyz", http.StatusOK, bodyServing)
}

// TestProbes_ColdStartWithoutAdminKeys: the bootstrap file equals the empty runtime, so its round swaps nothing; the
// startup probe still passes and the readiness waits for the manifest.
func TestProbes_ColdStartWithoutAdminKeys(t *testing.T) {
	t.Parallel()

	rig := newV2With(t, v2Options{cold: true, noAdminKeys: true})

	rig.wantProbe(t, "/startupz", http.StatusOK, `{"ready":true}`)
	rig.wantProbe(t, "/readyz", http.StatusServiceUnavailable, bodyWaiting)
	assert.Equal(t, uint64(0), rig.core.Status().Source.Runtime.Snapshot)
}

func TestProbes_OutageAfterGoodLoadNeverUnready(t *testing.T) {
	t.Parallel()

	rig := newV2(t)
	rig.wantProbe(t, "/readyz", http.StatusOK, bodyServing)

	require.NoError(t, os.Rename(rig.dir, rig.dir+".gone"))

	for range 25 {
		rig.tick(t)
		rig.wantProbe(t, "/readyz", http.StatusOK, bodyServing)
		rig.wantProbe(t, "/livez", http.StatusOK, `{"live":true}`)
	}

	code, _, body := do(t, http.MethodGet, rig.url+"/info", "", nil)
	require.Equal(t, http.StatusOK, code)

	var info model.InfoResponse
	require.NoError(t, json.Unmarshal(body, &info))
	require.NotNil(t, info.Config)
	assert.Equal(t, uint64(1), info.Config.Generation)
	assert.NotNil(t, info.Config.Staleness.LastConfirmed)
}

func TestProbes_ShutdownIsFirst(t *testing.T) {
	t.Parallel()

	rig := newV2(t)
	rig.wantProbe(t, "/readyz", http.StatusOK, bodyServing)

	rig.ready.BeginShutdown()

	// the listener still answers, and the probe says why not to send traffic
	rig.wantProbe(t, "/readyz", http.StatusServiceUnavailable, `{"ready":false,"reason":"shutting-down"}`)
	rig.wantProbe(t, "/livez", http.StatusOK, `{"live":true}`)
	rig.wantProbe(t, "/health", http.StatusOK, `{"initialized":true}`)
}

func TestProbes_CoreShutdownMakesReadyzUnready(t *testing.T) {
	t.Parallel()

	rig := newV2(t)

	ctx, cancel := context.WithTimeout(t.Context(), waitBound)
	defer cancel()

	require.NoError(t, rig.core.Shutdown(ctx))
	rig.wantProbe(t, "/readyz", http.StatusServiceUnavailable, `{"ready":false,"reason":"shutting-down"}`)
}

func TestProbes_StateDirRestoreWithStoreDownIsReady(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	first := newV2With(t, v2Options{stateDir: stateDir})
	first.wantProbe(t, "/readyz", http.StatusOK, bodyServing)

	// Shutdown writes the last snapshot before it returns
	ctx, cancel := context.WithTimeout(t.Context(), waitBound)
	defer cancel()

	require.NoError(t, first.core.Shutdown(ctx))

	// the store is empty (down) for the second start
	second := newV2With(t, v2Options{stateDir: stateDir, cold: true, dir: filepath.Join(t.TempDir(), "empty")})
	second.awaitGeneration(t, 1)
	second.wantProbe(t, "/readyz", http.StatusOK, bodyServing)
	assert.Equal(t, manifestreader.RestoreRestored, second.core.Status().Source.Reader.State.Restore)
	assert.Equal(t, uint64(1), second.core.Status().Source.Runtime.Generation)
}

func TestInfo_CompatAndZeroFlagSets(t *testing.T) {
	t.Parallel()

	rig := newV2(t)
	rig.sets, rig.flags = nil, map[string][]byte{}
	rig.publish(t)
	rig.tick(t)
	rig.awaitGeneration(t, 2)

	rig.wantProbe(t, "/readyz", http.StatusOK, `{"ready":true,"state":"empty"}`)

	code, _, body := do(t, http.MethodGet, rig.url+"/info", "", nil)
	require.Equal(t, http.StatusOK, code)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &raw))
	assert.Contains(t, raw, "cacheRefresh")
	assert.Contains(t, raw, "config")
}

// TestInfo_DegradedGolden pins /info after a bad object and a withdrawn flag set, and that no key is in it.
func TestInfo_DegradedGolden(t *testing.T) {
	t.Parallel()

	// a cold start: the bootstrap round ends alone before the first manifest. On a warm start the engine could take the
	// file and the first manifest in one round (it is latest-wins), and the golden count of applied rounds would be
	// 3 or 4 depending on the scheduling.
	rig := newV2With(t, v2Options{cold: true})
	rig.publish(t)
	rig.tick(t)
	rig.awaitGeneration(t, 1)

	rig.flags["flags-bad"] = []byte(flagsYAML)
	rig.sets = []controlplane.FlagSet{
		{Name: "tenant", Sources: []string{flagsObject}, APIKeys: []string{tenantKey}},
		{Name: "shaky", Sources: []string{"flags-bad"}, APIKeys: []string{"shaky-key"}},
		{Name: "gone", Sources: []string{flagsObject}, APIKeys: []string{"gone-key"}},
	}
	rig.publish(t)
	rig.tick(t)
	rig.awaitGeneration(t, 2)

	// generation 3: an object that no longer parses, and a flag set the publisher removed
	rig.flags["flags-bad"] = []byte("not: [valid")
	rig.sets = rig.sets[:2]
	rig.publish(t)
	rig.tick(t)
	rig.awaitGeneration(t, 3)

	rig.wantProbe(t, "/readyz", http.StatusOK, `{"ready":true,"state":"degraded"}`)

	_, _, body := do(t, http.MethodGet, rig.url+"/info", "", nil)

	for _, secret := range []string{tenantKey, "shaky-key", "gone-key", adminKey} {
		assert.NotContains(t, string(body), secret)
	}

	var info model.InfoResponse
	require.NoError(t, json.Unmarshal(body, &info))
	require.NotNil(t, info.Config)

	// the dates come from the clocks of the stores: the golden pins the rest
	info.LatestCacheRefresh, info.Flagsets = nil, nil
	info.Config.Staleness = model.StalenessInfo{}

	got, err := json.MarshalIndent(info, "", "  ")
	require.NoError(t, err)

	golden := filepath.Join("testdata", "info_degraded.golden.json")
	if os.Getenv("GOLDEN_UPDATE") != "" {
		require.NoError(t, os.WriteFile(golden, append(got, '\n'), 0o600))
	}

	want, err := os.ReadFile(golden)
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(got))
}

var errVeto = errors.New("vetoed by the test")

// TestMetricsAndInfo_AfterScriptedReloads scripts three applied rounds and one rejected, and pins every series.
func TestMetricsAndInfo_AfterScriptedReloads(t *testing.T) {
	t.Parallel()

	// a cold start: the bootstrap round ends alone before the first manifest. On a warm start the engine could take the
	// file and the first manifest in one round (it is latest-wins), and the count of applied rounds below would be
	// 2 or 3 depending on the scheduling.
	rig := newV2With(t, v2Options{cold: true, beforeSwap: func(candidate *flagruntime.Runtime) error {
		if candidate.Generation == 3 {
			return errVeto
		}

		return nil
	}})
	rig.publish(t)
	rig.tick(t)
	rig.awaitGeneration(t, 1)

	rig.publish(t)
	rig.tick(t)
	rig.awaitGeneration(t, 2)

	rig.publish(t)
	rig.tick(t)
	rig.awaitRejected(t, 3)

	now := float64(clock.Now().UnixNano()) / 1e9
	want := fmt.Sprintf(`
# HELP semaphore_config_age_seconds Seconds since the control plane last confirmed the manifest.
# TYPE semaphore_config_age_seconds gauge
semaphore_config_age_seconds 0
# HELP semaphore_config_generation Config generation by layer: applied (served) and received (handed to the engine).
# TYPE semaphore_config_generation gauge
semaphore_config_generation{layer="applied"} 2
semaphore_config_generation{layer="received"} 3
# HELP semaphore_config_last_success_timestamp_seconds Unix time of the last applied round; 0 when none.
# TYPE semaphore_config_last_success_timestamp_seconds gauge
semaphore_config_last_success_timestamp_seconds %v
# HELP semaphore_config_pending_restart Settings that changed in the bootstrap file and need a restart.
# TYPE semaphore_config_pending_restart gauge
semaphore_config_pending_restart 0
# HELP semaphore_config_reload_total Reconcile rounds by result: applied, rejected, ignored.
# TYPE semaphore_config_reload_total counter
semaphore_config_reload_total{result="applied"} 3
semaphore_config_reload_total{result="ignored"} 0
semaphore_config_reload_total{result="rejected"} 1
# HELP semaphore_config_snapshot Local counter of the swaps of the runtime.
# TYPE semaphore_config_snapshot gauge
semaphore_config_snapshot 3
# HELP semaphore_resource_count Resources by kind (flagset, object) and state.
# TYPE semaphore_resource_count gauge
semaphore_resource_count{kind="flagset",state="degraded"} 0
semaphore_resource_count{kind="flagset",state="pending"} 0
semaphore_resource_count{kind="flagset",state="ready"} 1
semaphore_resource_count{kind="object",state="invalid"} 0
semaphore_resource_count{kind="object",state="missing"} 0
semaphore_resource_count{kind="object",state="ok"} 2
semaphore_resource_count{kind="object",state="pending"} 0
semaphore_resource_count{kind="object",state="stale"} 0
`, now)

	require.NoError(t, testutil.GatherAndCompare(rig.metrics.Registry, strings.NewReader(want),
		"semaphore_config_age_seconds", "semaphore_config_generation",
		"semaphore_config_last_success_timestamp_seconds", "semaphore_config_pending_restart",
		"semaphore_config_reload_total", "semaphore_config_snapshot", "semaphore_resource_count",
		"semaphore_resource_status"))

	_, _, body := do(t, http.MethodGet, rig.url+"/info", "", nil)

	var info model.InfoResponse
	require.NoError(t, json.Unmarshal(body, &info))
	require.NotNil(t, info.Config)
	require.NotNil(t, info.Config.LastRejected)

	rejectedAt := clock.Now().UTC()
	assert.Equal(t, model.ReloadInfo{
		At: &rejectedAt, Outcome: "rejected", Code: flagruntime.CodeVetoed, Generation: 3,
	}, *info.Config.LastRejected)
	assert.Equal(t, map[string]uint64{"applied": 3, "rejected": 1, "ignored": 0}, info.Config.Reloads)
	assert.Equal(t, uint64(2), info.Config.Generation)
	assert.Equal(t, "rejected", info.Config.LastReload.Outcome)
	assert.Equal(t, uint64(2), info.Config.LastApplied.Generation)
	rig.wantProbe(t, "/readyz", http.StatusOK, bodyServing)
}
