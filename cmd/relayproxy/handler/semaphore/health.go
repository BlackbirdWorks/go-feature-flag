package controller

import (
	"context"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
)

// Health is the controller of the health endpoint.
type Health struct {
	monitoringService service.Monitoring
}

// HealthOutput is the response of the health endpoint.
type HealthOutput struct {
	Body model.HealthResponse
}

// NewHealth is a constructor to create a new controller for the health method.
func NewHealth(monitoring service.Monitoring) *Health {
	return &Health{monitoringService: monitoring}
}

// Handle tells if the relay proxy is ready to serve traffic.
func (h *Health) Handle(_ context.Context, _ *struct{}) (*HealthOutput, error) {
	return &HealthOutput{Body: h.monitoringService.Health()}, nil
}
