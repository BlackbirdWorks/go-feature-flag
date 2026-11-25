package service

import (
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
)

// Services holds the services used by the relay proxy.
type Services struct {
	// MonitoringService is the service in charge of managing the monitoring
	MonitoringService Monitoring
	// WebsocketBroadcasterService is the service in charge to manage the websockets in the relay proxy
	WebsocketService stream.WebsocketService
	// SSEService is the service in charge of managing SSE connections in the relay proxy
	SSEService stream.SSEService
	// FlagsetManager is the service in charge of managing the flagsets
	FlagsetManager FlagsetManager
	// Metrics is the service containing all the prometheus metrics
	Metrics *metric.Metrics
}
