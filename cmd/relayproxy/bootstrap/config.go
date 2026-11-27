package bootstrap

// Version is the only schema version of the bootstrap file.
const Version = 2

// Config is the bootstrap file, version 2.
//
//nolint:govet // the field order is the order of the effective print
type Config struct {
	Connections   map[string]Connection `json:"connections,omitempty"  yaml:"connections,omitempty"`
	Exporters     map[string]Exporter   `json:"exporters,omitempty"    yaml:"exporters,omitempty"`
	Notifiers     map[string]Notifier   `json:"notifiers,omitempty"    yaml:"notifiers,omitempty"`
	ControlPlane  *ControlPlane         `json:"controlPlane,omitempty" yaml:"controlPlane,omitempty"`
	Server        Server                `json:"server,omitzero"        yaml:"server"`
	Observability Observability         `json:"observability,omitzero" yaml:"observability"`
	Evaluation    Evaluation            `json:"evaluation,omitzero"    yaml:"evaluation"`
	Auth          Auth                  `json:"auth,omitzero"          yaml:"auth"`
	Version       int                   `json:"version"                jsonschema:"enum=2"           yaml:"version"`
	// badEnv holds the shutdown variables that did not parse, by setting path, for the validation.
	badEnv map[string]string
}

// Listener roles and protocols.
const (
	RoleAPI = "api"
	RoleOps = "ops"

	ProtocolHTTP1 = "http1"
	ProtocolH2C   = "h2c"
)

// Server is the network side of the relay.
//
//nolint:govet // the field order is the order of the effective print
type Server struct {
	// UnixSocketPath is the path of a unix socket the api listener also serves on.
	UnixSocketPath string     `json:"unixSocketPath,omitempty" yaml:"unixSocketPath,omitempty"`
	Listeners      []Listener `json:"listeners,omitempty"      yaml:"listeners,omitempty"`
	// Shutdown tunes the stop of the relay after SIGTERM or SIGINT.
	Shutdown Shutdown `json:"shutdown,omitzero" yaml:"shutdown,omitempty"`
}

// Listener is one address the relay listens on.
type Listener struct {
	Name    string `json:"name"              yaml:"name"`
	Address string `json:"address,omitempty" yaml:"address"`
	// Role is "api" (default) or "ops".
	Role string `json:"role,omitempty" jsonschema:"enum=api,enum=ops,default=api" yaml:"role,omitempty"`
	// Protocols are "http1" (default) and "h2c". "h2" and "tls" are reserved.
	Protocols []string `json:"protocols,omitempty" jsonschema:"enum=http1,enum=h2c,uniqueItems=true" yaml:"protocols,omitempty"` //nolint:lll // struct tags can not be wrapped
}

// Auth holds the admin API keys.
type Auth struct {
	// AdminKeys are plain strings or secret references. They are never printed.
	AdminKeys []AdminKey `json:"adminKeys,omitempty" yaml:"adminKeys,omitempty"`
}

// Evaluation is how the relay evaluates flags.
type Evaluation struct {
	// ContextEnrichment is merged into the context of every evaluation of every flag set that has no enrichment of
	// its own. It wins over a request attribute of the same name, as in version 1.
	ContextEnrichment map[string]any `json:"contextEnrichment,omitempty" jsonschema_description:"Merged into the context of every evaluation. Values: strings, integers, finite numbers, booleans, lists and mappings with string keys (no timestamps, binary or infinity)." yaml:"contextEnrichment,omitempty"` //nolint:lll // struct tags can not be wrapped
	// Environment is added to the enrichment under the key env.
	Environment string `json:"environment,omitempty" yaml:"environment,omitempty"`
	// DisableSemaphoreMetadata keeps the semaphore_cacheable key out of the metadata of the OFREP answers; the
	// legacy gofeatureflag_cacheable key is always sent.
	DisableSemaphoreMetadata bool `json:"disableSemaphoreMetadata,omitempty" yaml:"disableSemaphoreMetadata,omitempty"`
}
