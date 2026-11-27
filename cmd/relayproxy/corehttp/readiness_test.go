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

const (
	bodyStarted  = `{"ready":true}`
	bodyEmpty    = `{"ready":true,"state":"empty"}`
	bodyDegraded = `{"ready":true,"state":"degraded"}`
)

// fakeStatus is a core status the test changes between the questions.
type fakeStatus struct {
	st atomic.Pointer[relaycore.Status]
}

func (f *fakeStatus) Status() relaycore.Status { return *f.st.Load() }

func (f *fakeStatus) set(status *relaycore.Status) { f.st.Store(status) }

func runtimeStatus(snapshot, generation uint64, sets map[string]*flagruntime.FlagSetStatus,
	objects map[string]*flagruntime.ObjectStatus,
) *relaycore.Status {
	return &relaycore.Status{Bootstrapped: snapshot > 0, Source: controlsource.Status{Runtime: &flagruntime.Status{
		Snapshot: snapshot, Generation: generation, FlagSets: sets, Objects: objects,
	}}}
}

func serving() map[string]*flagruntime.FlagSetStatus {
	return map[string]*flagruntime.FlagSetStatus{"a": {Name: "a", State: flagruntime.StateServing}}
}

// TestReadiness walks the state machine through every transition and every reason, with the exact answers.
func TestReadiness(t *testing.T) {
	t.Parallel()

	type step struct {
		status       *relaycore.Status
		name         string
		wantReadyBod string
		wantStartBod string
		wantReadyz   int
		wantStartz   int
		beginStop    bool
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
			{name: "no runtime", status: &relaycore.Status{}, wantReadyz: 503, wantReadyBod: notReadyStarting,
				wantStartz: 503, wantStartBod: notReadyStarting},
			{name: "empty runtime", status: runtimeStatus(0, 0, nil, nil), wantReadyz: 503,
				wantReadyBod: notReadyStarting, wantStartz: 503, wantStartBod: notReadyStarting},
			{name: "first round ended without a swap (no admin keys)", status: &relaycore.Status{
				Bootstrapped: true, Source: runtimeStatus(0, 0, nil, nil).Source,
			}, wantReadyz: 503, wantReadyBod: notReadyWaiting, wantStartz: 200, wantStartBod: bodyStarted},
			{name: "bootstrap applied", status: runtimeStatus(1, 0, nil, nil), wantReadyz: 503,
				wantReadyBod: notReadyWaiting, wantStartz: 200, wantStartBod: bodyStarted},
			{name: "manifest with zero objects", status: runtimeStatus(2, 1, nil, nil), wantReadyz: 200,
				wantReadyBod: bodyEmpty, wantStartz: 200, wantStartBod: bodyStarted},
			{name: "one flag set", status: runtimeStatus(3, 2, serving(), okObject), wantReadyz: 200,
				wantReadyBod: bodyServing, wantStartz: 200, wantStartBod: bodyStarted},
		}},
		{"state directory restore is ready at once", []step{
			{name: "restored generation", status: runtimeStatus(1, 7, serving(), okObject), wantReadyz: 200,
				wantReadyBod: bodyServing, wantStartz: 200, wantStartBod: bodyStarted},
		}},
		{"degraded and stale content never goes unready", []step{
			{name: "serving", status: runtimeStatus(2, 1, serving(), okObject), wantReadyz: 200,
				wantReadyBod: bodyServing, wantStartz: 200, wantStartBod: bodyStarted},
			{name: "stale object", status: runtimeStatus(2, 1, serving(), staleObject), wantReadyz: 200,
				wantReadyBod: bodyDegraded, wantStartz: 200, wantStartBod: bodyStarted},
			{name: "degraded set", status: runtimeStatus(2, 1, degradedSet, okObject), wantReadyz: 200,
				wantReadyBod: bodyDegraded, wantStartz: 200, wantStartBod: bodyStarted},
			{name: "pending set", status: runtimeStatus(2, 1, pendingSet, okObject), wantReadyz: 200,
				wantReadyBod: bodyDegraded, wantStartz: 200, wantStartBod: bodyStarted},
			{name: "everything gone", status: runtimeStatus(2, 1, nil, nil), wantReadyz: 200,
				wantReadyBod: bodyEmpty, wantStartz: 200, wantStartBod: bodyStarted},
		}},
		{"once ready it stays ready", []step{
			{name: "ready", status: runtimeStatus(2, 1, serving(), okObject), wantReadyz: 200,
				wantReadyBod: bodyServing, wantStartz: 200, wantStartBod: bodyStarted},
			{name: "generation reads zero again", status: runtimeStatus(2, 0, serving(), okObject),
				wantReadyz: 200, wantReadyBod: bodyServing, wantStartz: 200,
				wantStartBod: bodyStarted},
			{name: "no runtime reads again", status: &relaycore.Status{}, wantReadyz: 200,
				wantReadyBod: bodyEmpty, wantStartz: 200, wantStartBod: bodyStarted},
		}},
		{"shutdown is first", []step{
			{name: "ready", status: runtimeStatus(2, 1, serving(), okObject), wantReadyz: 200,
				wantReadyBod: bodyServing, wantStartz: 200, wantStartBod: bodyStarted},
			{name: "begin shutdown", status: runtimeStatus(2, 1, serving(), okObject), beginStop: true,
				wantReadyz: 503, wantReadyBod: notReadyStop, wantStartz: 200, wantStartBod: bodyStarted},
		}},
		{"shutdown before anything", []step{
			{name: "begin shutdown", status: &relaycore.Status{}, beginStop: true, wantReadyz: 503,
				wantReadyBod: notReadyStop, wantStartz: 503, wantStartBod: notReadyStarting},
		}},
		{"the core shutting down", []step{
			{name: "core shutdown", status: &relaycore.Status{ShuttingDown: true,
				Source: runtimeStatus(2, 1, serving(), okObject).Source}, wantReadyz: 503,
				wantReadyBod: notReadyStop, wantStartz: 200, wantStartBod: bodyStarted},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := &fakeStatus{}
			src.set(&relaycore.Status{})

			ready := corehttp.NewReadiness(src)

			for _, stp := range tt.steps {
				src.set(stp.status)

				if stp.beginStop {
					ready.BeginShutdown()
				}

				code, body := ready.Ready()
				got, err := json.Marshal(body)
				require.NoError(t, err, stp.name)
				assert.Equal(t, stp.wantReadyz, code, stp.name)
				assert.JSONEq(t, stp.wantReadyBod, string(got), stp.name)

				code, body = ready.Started()
				got, err = json.Marshal(body)
				require.NoError(t, err, stp.name)
				assert.Equal(t, stp.wantStartz, code, stp.name)
				assert.JSONEq(t, stp.wantStartBod, string(got), stp.name)
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
