package bootstrap

import "strings"

// The paths of the settings of the server that are checked together.
const (
	pathOPSPprof    = "server.ops.pprof.enabled"
	pathStreamURL   = "server.ofrepEventStream.baseUrl"
	pathStreamDelay = "server.ofrepEventStream.inactivityDelaySec"
	pathAnonymous   = "auth.anonymousFlagSet"
)

// Ops holds the settings of the ops listener (the listener with the role ops).
type Ops struct {
	// Pprof serves the Go profiler on the ops listener, never on the api listener.
	Pprof Pprof `json:"pprof,omitzero" yaml:"pprof,omitempty"`
}

// Pprof is the profiler endpoint, under /debug/pprof/ of the ops listener.
type Pprof struct {
	// Enabled needs a listener with the role ops. Default false.
	Enabled bool `json:"enabled,omitempty" jsonschema:"default=false" yaml:"enabled,omitempty"`
}

// OFREPEventStream makes the OFREP bulk evaluation advertise the endpoint of the flag changes (OpenFeature ADR-0008),
// so that a provider subscribes instead of polling. It is on when BaseURL is set.
type OFREPEventStream struct {
	// BaseURL is the public base URL clients use to reach the relay, for example https://flags.example.com: an
	// absolute http or https URL without credentials, query or fragment. Empty means off.
	BaseURL string `json:"baseUrl,omitempty" yaml:"baseUrl,omitempty"`
	// InactivityDelaySec is the delay in seconds after which a client takes the stream as inactive; 0 omits it.
	InactivityDelaySec int `json:"inactivityDelaySec,omitempty" jsonschema:"minimum=0" yaml:"inactivityDelaySec,omitempty"` //nolint:lll // struct tags can not be wrapped
}

// ValidateFlags are the limits of the body of POST /admin/v1/flags/validate, by format.
type ValidateFlags struct {
	// MaxJSONBodySize is the largest JSON body. Default 1MiB, applied by the endpoint when unset or zero.
	MaxJSONBodySize *ByteSize `json:"maxJsonBodySize,omitempty" jsonschema_description:"Largest JSON body. Default 1MiB, applied by the endpoint when unset or zero." yaml:"maxJsonBodySize,omitempty"` //nolint:lll // struct tags can not be wrapped
	// MaxYAMLBodySize is the largest YAML body. Default 64KiB, applied by the endpoint when unset or zero.
	MaxYAMLBodySize *ByteSize `json:"maxYamlBodySize,omitempty" jsonschema_description:"Largest YAML body. Default 64KiB, applied by the endpoint when unset or zero." yaml:"maxYamlBodySize,omitempty"` //nolint:lll // struct tags can not be wrapped
	// MaxTOMLBodySize is the largest TOML body. Default 64KiB, applied by the endpoint when unset or zero.
	MaxTOMLBodySize *ByteSize `json:"maxTomlBodySize,omitempty" jsonschema_description:"Largest TOML body. Default 64KiB, applied by the endpoint when unset or zero." yaml:"maxTomlBodySize,omitempty"` //nolint:lll // struct tags can not be wrapped
}

// serverSettings checks the settings of the server that are not listeners.
func (v *validator) serverSettings() {
	server := &v.cfg.Server

	if server.Ops.Pprof.Enabled && !v.hasOps() {
		v.failf(pathOPSPprof, ErrInvalidValue,
			"needs a listener with role %s, pprof is never served on the api listener", RoleOps)
	}

	v.eventStream(&server.OFREPEventStream)
}

// eventStream checks the event stream. The URL may carry credentials: it is never part of an error.
func (v *validator) eventStream(stream *OFREPEventStream) {
	if stream.InactivityDelaySec < 0 {
		v.failf(pathStreamDelay, ErrInvalidValue, "expected zero or more seconds")
	}

	if stream.BaseURL == "" {
		return
	}

	before := len(v.errs)

	v.httpURL(pathStreamURL, stream.BaseURL)

	if len(v.errs) == before && strings.ContainsAny(stream.BaseURL, "?#") {
		v.failf(pathStreamURL, ErrInvalidValue, "a query or a fragment is not allowed")
	}
}

func (v *validator) hasOps() bool {
	for i := range v.cfg.Server.Listeners {
		if v.cfg.Server.Listeners[i].Role == RoleOps {
			return true
		}
	}

	return false
}

// anonymousFlagSet checks the name of the flag set that serves the requests without a key. Whether the flag set
// exists is known to the relay only, per generation.
func (v *validator) anonymousFlagSet() {
	name := v.cfg.Auth.AnonymousFlagSet
	if name == "" {
		return
	}

	if !nameRE.MatchString(name) {
		v.failf(pathAnonymous, ErrInvalidName, "names match %s", nameRE.String())
	}
}
