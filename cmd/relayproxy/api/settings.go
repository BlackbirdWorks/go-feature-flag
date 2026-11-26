package api

import (
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/opentelemetry"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/ofrep"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
)

// ServerMode is the mode the server runs in.
type ServerMode = string

const (
	// ServerModeHTTP is the HTTP server mode, the default.
	ServerModeHTTP ServerMode = "http"
	// ServerModeLambda is the AWS Lambda server mode.
	ServerModeLambda ServerMode = "lambda"
	// ServerModeUnixSocket is the Unix Socket server mode.
	ServerModeUnixSocket ServerMode = "unixsocket"
)

const (
	// LambdaAdapterAPIGatewayV1 is the adapter for API Gateway version 1 events.
	LambdaAdapterAPIGatewayV1 = "APIGatewayV1"
	// LambdaAdapterAPIGatewayV2 is the adapter for API Gateway version 2 events, the default.
	LambdaAdapterAPIGatewayV2 = "APIGatewayV2"
	// LambdaAdapterALB is the adapter for Application Load Balancer events.
	LambdaAdapterALB = "ALB"
)

// Settings is what the HTTP layer reads of the configuration of the relay proxy: the listeners, the optional
// endpoints, the toggles of the middleware and the telemetry. It is built once at startup, its values are final (the
// defaults and the deprecated aliases are resolved), and the server never changes it.
// What changes at reload, the API keys, is not here: see [service.APIKeys].
type Settings struct {
	// Version is the version of the relay proxy, reported by the version header, the logs and the OpenAPI document.
	Version string
	// Mode is the server mode: http, lambda or unixsocket.
	Mode ServerMode
	// Host is the host the API and the monitoring listeners bind.
	Host string
	// UnixSocketPath is the path of the socket in the unix socket mode.
	UnixSocketPath string
	// LambdaAdapter is the event adapter of the lambda mode.
	LambdaAdapter string
	// AwsAPIGatewayBasePath is the base path that the lambda mode strips from the requests.
	AwsAPIGatewayBasePath string
	// OfrepEventStream is what the OFREP bulk evaluation advertises of the stream of flag changes.
	OfrepEventStream ofrep.EventStream
	// Telemetry is the OpenTelemetry configuration.
	Telemetry opentelemetry.Settings
	// ValidateFlags are the body size limits of the validate flags endpoint.
	ValidateFlags controller.ValidateFlagsLimits
	// Port is the port of the API listener.
	Port int
	// MonitoringPort is the port of the dedicated monitoring listener, 0 when the API listener serves the
	// monitoring endpoints.
	MonitoringPort int
	// EnableH2C makes the API listener accept HTTP/2 over cleartext.
	EnableH2C bool
	// Debug is the debug log level: the errors of the requests carry their details, pprof is served.
	Debug bool
	// Swagger serves the Swagger UI and the OpenAPI document.
	Swagger bool
	// Pprof serves the pprof endpoints without the debug level.
	Pprof bool
	// ExposeLegacyMetrics exposes the metrics under their legacy names too.
	ExposeLegacyMetrics bool
	// DisableVersionHeader removes the version headers of the responses.
	DisableVersionHeader bool
	// DisableSemaphoreMetadata keeps the Semaphore keys out of the metadata of the OFREP evaluations.
	DisableSemaphoreMetadata bool
}
