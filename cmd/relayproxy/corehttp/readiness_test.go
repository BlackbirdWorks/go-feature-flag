package corehttp_test

import (
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/controlsource"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
)

// fakeStatus is a core status the test changes between the questions.
type fakeStatus struct {
	st atomic.Pointer[relaycore.Status]
}

func (f *fakeStatus) Status() relaycore.Status { return *f.st.Load() }

func (f *fakeStatus) set(st relaycore.Status) { f.st.Store(&st) }

func runtimeStatus(snapshot, generation uint64, sets map[string]*flagruntime.FlagSetStatus,
	objects map[string]*flagruntime.ObjectStatus,
) relaycore.Status {
	return relaycore.Status{Source: controlsource.Status{Runtime: &flagruntime.Status{
		Snapshot: snapshot, Generation: generation, FlagSets: sets, Objects: objects,
	}}}
}

func serving(name string) map[string]*flagruntime.FlagSetStatus {
	return map[string]*flagruntime.FlagSetStatus{name: {Name: name, State: flagruntime.StateServing}}
}

// TestReadiness walks the state machine through every transition and every reason, with the exact answers.
func TestReadiness(t *testing.T) {
	t.Parallel()

	type step struct {
		name         string
		status       relaycore.Status
		beginStop    bool
		wantReadyz   int
		wantReadyBod string
		wantStartz   int
		wantStartBod string
	}

	const (
		notReadyStarting = `{"ready":false,"reason":"starting"}`
		notReadyWaiting  = `{"ready":false,"reason":"waiting-for-manifest"}`
		notReadyStop     = `{"ready":false,"reason":"shutting-down"}`
	)

	degradedSet := map[string]*flagruntime.FlagSetStatus{"a": {Name: "a", State: flagruntime.StateDegraded}}
	pendingSet := map[string]*flagruntime.FlagSetStatus{"a": {Name: "a", State: flagruntime.StatePending}}
	staleObject := map[string]*flagruntime.ObjectStatus{"o": {Name: "o", State: flagruntime.ObjectStale}}
	okObject := map[string]*flagruntime.ObjectStatus{"o": {Name: "o", State: flagruntime.ObjectOK}}

	tests := []struct {
		name  string
		steps []step
	}{
		{"cold start without manifest then manifest", []step{
			{name: "no runtime", status: relaycore.Status{}, wantReadyz: 503, wantReadyBod: notReadyStarting,
				wantStartz: 503, wantStartBod: notReadyStarting},
			{name: "empty runtime", status: runtimeStatus(0, 0, nil, nil), wantReadyz: 503,
				wantReadyBod: notReadyStarting, wantStartz: 503, wantStartBod: notReadyStarting},
			{name: "bootstrap applied", status: runtimeStatus(1, 0, nil, nil), wantReadyz: 503,
				wantReadyBod: notReadyWaiting, wantStartz: 200, wantStartBod: `{"ready":true}`},
			{name: "manifest with zero objects", status: runtimeStatus(2, 1, nil, nil), wantReadyz: 200,
				wantReadyBod: `{"ready":true,"state":"empty"}`, wantStartz: 200, wantStartBod: `{"ready":true}`},
			{name: "one flag set", status: runtimeStatus(3, 2, serving("a"), okObject), wantReadyz: 200,
				wantReadyBod: `{"ready":true,"state":"serving"}`, wantStartz: 200, wantStartBod: `{"ready":true}`},
		}},
		{"state directory restore is ready at once", []step{
			{name: "restored generation", status: runtimeStatus(1, 7, serving("a"), okObject), wantReadyz: 200,
				wantReadyBod: `{"ready":true,"state":"serving"}`, wantStartz: 200, wantStartBod: `{"ready":true}`},
		}},
		{"degraded and stale content never goes unready", []step{
			{name: "serving", status: runtimeStatus(2, 1, serving("a"), okObject), wantReadyz: 200,
				wantReadyBod: `{"ready":true,"state":"serving"}`, wantStartz: 200, wantStartBod: `{"ready":true}`},
			{name: "stale object", status: runtimeStatus(2, 1, serving("a"), staleObject), wantReadyz: 200,
				wantReadyBod: `{"ready":true,"state":"degraded"}`, wantStartz: 200, wantStartBod: `{"ready":true}`},
			{name: "degraded set", status: runtimeStatus(2, 1, degradedSet, okObject), wantReadyz: 200,
				wantReadyBod: `{"ready":true,"state":"degraded"}`, wantStartz: 200, wantStartBod: `{"ready":true}`},
			{name: "pending set", status: runtimeStatus(2, 1, pendingSet, okObject), wantReadyz: 200,
				wantReadyBod: `{"ready":true,"state":"degraded"}`, wantStartz: 200, wantStartBod: `{"ready":true}`},
			{name: "everything gone", status: runtimeStatus(2, 1, nil, nil), wantReadyz: 200,
				wantReadyBod: `{"ready":true,"state":"empty"}`, wantStartz: 200, wantStartBod: `{"ready":true}`},
		}},
		{"once ready it stays ready", []step{
			{name: "ready", status: runtimeStatus(2, 1, serving("a"), okObject), wantReadyz: 200,
				wantReadyBod: `{"ready":true,"state":"serving"}`, wantStartz: 200, wantStartBod: `{"ready":true}`},
			{name: "generation reads zero again", status: runtimeStatus(2, 0, serving("a"), okObject),
				wantReadyz: 200, wantReadyBod: `{"ready":true,"state":"serving"}`, wantStartz: 200,
				wantStartBod: `{"ready":true}`},
			{name: "no runtime reads again", status: relaycore.Status{}, wantReadyz: 200,
				wantReadyBod: `{"ready":true,"state":"empty"}`, wantStartz: 200, wantStartBod: `{"ready":true}`},
		}},
		{"shutdown is first", []step{
			{name: "ready", status: runtimeStatus(2, 1, serving("a"), okObject), wantReadyz: 200,
				wantReadyBod: `{"ready":true,"state":"serving"}`, wantStartz: 200, wantStartBod: `{"ready":true}`},
			{name: "begin shutdown", status: runtimeStatus(2, 1, serving("a"), okObject), beginStop: true,
				wantReadyz: 503, wantReadyBod: notReadyStop, wantStartz: 200, wantStartBod: `{"ready":true}`},
		}},
		{"shutdown before anything", []step{
			{name: "begin shutdown", status: relaycore.Status{}, beginStop: true, wantReadyz: 503,
				wantReadyBod: notReadyStop, wantStartz: 503, wantStartBod: notReadyStarting},
		}},
		{"the core shutting down", []step{
			{name: "core shutdown", status: relaycore.Status{ShuttingDown: true,
				Source: runtimeStatus(2, 1, serving("a"), okObject).Source}, wantReadyz: 503,
				wantReadyBod: notReadyStop, wantStartz: 200, wantStartBod: `{"ready":true}`},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := &fakeStatus{}
			src.set(relaycore.Status{})

			ready := corehttp.NewReadiness(src)

			for _, st := range tt.steps {
				src.set(st.status)

				if st.beginStop {
					ready.BeginShutdown()
				}

				code, body := ready.Ready()
				got, err := json.Marshal(body)
				require.NoError(t, err, st.name)
				assert.Equal(t, st.wantReadyz, code, st.name)
				assert.JSONEq(t, st.wantReadyBod, string(got), st.name)

				code, body = ready.Started()
				got, err = json.Marshal(body)
				require.NoError(t, err, st.name)
				assert.Equal(t, st.wantStartz, code, st.name)
				assert.JSONEq(t, st.wantStartBod, string(got), st.name)
			}
		})
	}
}

// TestReadiness_ClosedLists pins the closed lists the docs repeat.
func TestReadiness_ClosedLists(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"starting", "waiting-for-manifest", "shutting-down"}, corehttp.Reasons())
	assert.Equal(t, []string{"empty", "serving", "degraded"}, corehttp.ReadyStates())
}
