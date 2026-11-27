package corehttp

import (
	"net/http"
	"sync/atomic"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
)

// The reasons of a 503 of /readyz and /startupz. The list is closed ([Reasons]).
const (
	// ReasonStarting: the bootstrap snapshot is not applied yet.
	ReasonStarting = "starting"
	// ReasonWaitingForManifest: the bootstrap snapshot is applied and no manifest was applied yet (nor a verified
	// copy in the state directory).
	ReasonWaitingForManifest = "waiting-for-manifest"
	// ReasonShuttingDown: the relay stops.
	ReasonShuttingDown = "shutting-down"
)

// The states of the body of a 200 of /readyz. The list is closed ([ReadyStates]).
const (
	// ReadyEmpty: ready with zero flag sets.
	ReadyEmpty = "empty"
	// ReadyServing: every flag set and every object is fine.
	ReadyServing = "serving"
	// ReadyDegraded: a flag set or an object is not fine; the relay still serves.
	ReadyDegraded = "degraded"
)

// Reasons are the closed reasons of a 503.
func Reasons() []string {
	return []string{ReasonStarting, ReasonWaitingForManifest, ReasonShuttingDown}
}

// ReadyStates are the closed states of a 200 of /readyz.
func ReadyStates() []string { return []string{ReadyEmpty, ReadyServing, ReadyDegraded} }

// StatusSource is what the readiness needs of a core: *relaycore.Core implements it.
type StatusSource interface {
	Status() relaycore.Status
}

// Readiness is the readiness state machine of a core. It follows the uptime rules: the relay is not ready until
// the bootstrap snapshot and a first manifest (or the verified copy of the state directory) are applied; from then
// on it stays ready whatever the control plane does, until [Readiness.BeginShutdown] or [relaycore.Core.Shutdown].
// Degraded and stale content never makes it unready. It owns no goroutine and reads the status on demand.
type Readiness struct {
	src       StatusSource
	sums      *summaries
	shutting  atomic.Bool
	started   atomic.Bool
	everReady atomic.Bool
}

var _ service.Probes = (*Readiness)(nil)

// NewReadiness returns the readiness of a core.
func NewReadiness(src StatusSource) *Readiness {
	return &Readiness{src: src, sums: &summaries{}}
}

// BeginShutdown makes /readyz answer 503 shutting-down. Call it first on SIGTERM, before the listeners close.
func (r *Readiness) BeginShutdown() { r.shutting.Store(true) }

// Ready answers /readyz.
func (r *Readiness) Ready() (int, model.ReadyResponse) {
	st := r.src.Status()
	if r.shutting.Load() || st.ShuttingDown {
		return http.StatusServiceUnavailable, model.ReadyResponse{Reason: ReasonShuttingDown}
	}

	rt := st.Source.Runtime
	if rt != nil && rt.Snapshot > 0 {
		r.started.Store(true)
	}

	if rt != nil && rt.Generation > 0 {
		r.everReady.Store(true)
	}

	switch {
	case r.everReady.Load():
		return http.StatusOK, model.ReadyResponse{Ready: true, State: r.state(st)}
	case r.started.Load():
		return http.StatusServiceUnavailable, model.ReadyResponse{Reason: ReasonWaitingForManifest}
	}

	return http.StatusServiceUnavailable, model.ReadyResponse{Reason: ReasonStarting}
}

// Started answers /startupz: 200 once the bootstrap snapshot is applied, and from then on.
func (r *Readiness) Started() (int, model.ReadyResponse) {
	rt := r.src.Status().Source.Runtime
	if rt != nil && rt.Snapshot > 0 {
		r.started.Store(true)
	}

	if r.started.Load() {
		return http.StatusOK, model.ReadyResponse{Ready: true}
	}

	return http.StatusServiceUnavailable, model.ReadyResponse{Reason: ReasonStarting}
}

func (r *Readiness) state(st relaycore.Status) string {
	sum := r.sums.of(st.Source.Runtime)

	switch {
	case st.Source.Runtime == nil || len(st.Source.Runtime.FlagSets) == 0:
		return ReadyEmpty
	case len(sum.badSets) > 0 || len(sum.badObject) > 0:
		return ReadyDegraded
	}

	return ReadyServing
}
