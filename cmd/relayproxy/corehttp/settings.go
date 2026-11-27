package corehttp

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/opentelemetry"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/ofrep"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
)

// ErrListeners is wrapped by the error of [SettingsFrom] when the listeners of the file are more than the server
// serves. The server fails closed: it does not start with a listener it would ignore.
var ErrListeners = errors.New("corehttp: the listeners of the file are not supported")

// SettingsFrom returns the settings of the HTTP layer from a bootstrap file. The server serves one api listener
// and at most one ops listener, on the same host; a unix socket is not served yet. version is the version of the
// binary.
func SettingsFrom(cfg *bootstrap.Config, version string) (api.Settings, error) {
	settings := api.Settings{
		Version:                  version,
		Mode:                     api.ServerModeHTTP,
		ExposeLegacyMetrics:      cfg.Observability.Metrics.LegacyNames,
		DisableSemaphoreMetadata: cfg.Evaluation.DisableSemaphoreMetadata,
		Pprof:                    cfg.Server.Ops.Pprof.Enabled,
		ValidateFlags:            validateFlagsLimits(&cfg.Server.ValidateFlags),
		Telemetry: opentelemetry.Settings{
			Version:      version,
			OTLPEndpoint: cfg.Observability.Tracing.OTLPEndpoint,
			ServiceName:  cfg.Observability.Tracing.ServiceName,
			Sampler:      cfg.Observability.Tracing.Sampler,
		},
	}

	stream, streamErr := ofrep.NewEventStream(
		cfg.Server.OFREPEventStream.BaseURL,
		cfg.Server.OFREPEventStream.InactivityDelaySec,
	)
	if streamErr != nil {
		return api.Settings{}, fmt.Errorf("server.ofrepEventStream: %w", streamErr)
	}

	settings.OfrepEventStream = stream

	if cfg.Server.UnixSocketPath != "" {
		return api.Settings{}, fmt.Errorf("%w: server.unixSocketPath", ErrListeners)
	}

	var apiHost, opsHost string

	for i := range cfg.Server.Listeners {
		listener := &cfg.Server.Listeners[i]

		host, port, err := splitAddress(listener.Address)
		if err != nil {
			return api.Settings{}, fmt.Errorf("server.listeners[%d].address: %w", i, err)
		}

		switch listener.Role {
		case bootstrap.RoleOps:
			if settings.MonitoringPort != 0 {
				return api.Settings{}, fmt.Errorf("%w: more than one ops listener", ErrListeners)
			}

			settings.MonitoringPort, opsHost = port, host
		default:
			if settings.Port != 0 {
				return api.Settings{}, fmt.Errorf("%w: more than one api listener", ErrListeners)
			}

			settings.Port, apiHost = port, host
			settings.EnableH2C = slices.Contains(listener.Protocols, bootstrap.ProtocolH2C)
		}
	}

	if settings.MonitoringPort != 0 && opsHost != apiHost {
		return api.Settings{}, fmt.Errorf("%w: the api and ops listeners must share their host", ErrListeners)
	}

	settings.Host = apiHost

	return settings, nil
}

// splitAddress splits host:port into the host and a port in [1, 65535].
func splitAddress(address string) (string, int, error) {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("%w: %w", ErrListeners, err)
	}

	port, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil || port == 0 {
		return "", 0, fmt.Errorf("%w: port %q", ErrListeners, rawPort)
	}

	return host, int(port), nil
}

// validateFlagsLimits returns the limits of the validate flags endpoint; a limit the file does not set is zero, which
// the endpoint reads as its default.
func validateFlagsLimits(file *bootstrap.ValidateFlags) controller.ValidateFlagsLimits {
	var limits controller.ValidateFlagsLimits

	if file.MaxJSONBodySize != nil {
		limits.MaxJSONBodySize = int64(*file.MaxJSONBodySize)
	}

	if file.MaxYAMLBodySize != nil {
		limits.MaxYAMLBodySize = int64(*file.MaxYAMLBodySize)
	}

	if file.MaxTOMLBodySize != nil {
		limits.MaxTOMLBodySize = int64(*file.MaxTOMLBodySize)
	}

	return limits
}
