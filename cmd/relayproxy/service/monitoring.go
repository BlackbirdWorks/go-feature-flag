package service

import "github.com/blackbirdworks/semaphore/cmd/relayproxy/model"

// Monitoring is the interface of the monitoring service.
type Monitoring interface {
	Health() model.HealthResponse
	Info() (model.InfoResponse, error)
}

// Probes is implemented by a monitoring service that serves the probe endpoints /readyz and /startupz. A service that
// does not implement it does not serve the endpoints.
type Probes interface {
	// Ready answers /readyz: the status code and the body.
	Ready() (int, model.ReadyResponse)
	// Started answers /startupz: the status code and the body.
	Started() (int, model.ReadyResponse)
}
