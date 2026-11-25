package semaphore

import (
	"context"
	"fmt"
	"net/http"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
)

// Info is the controller of the info endpoint.
type Info struct {
	monitoringService service.Monitoring
}

// InfoOutput is the response of the info endpoint.
type InfoOutput struct {
	Body model.InfoResponse
}

// NewInfo is the constructor of the controller of the info endpoint.
func NewInfo(monitoring service.Monitoring) *Info {
	return &Info{monitoringService: monitoring}
}

// Handle returns information about the state of the relay proxy.
func (h *Info) Handle(_ context.Context, _ *struct{}) (*InfoOutput, error) {
	info, err := h.monitoringService.Info()
	if err != nil {
		return nil, fmt.Errorf(
			"getting the information of the relay proxy: %w",
			helper.NewHTTPError(http.StatusInternalServerError, err.Error()),
		)
	}

	return &InfoOutput{Body: info}, nil
}
