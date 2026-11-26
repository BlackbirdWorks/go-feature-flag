package bootstrap

// Version is the only schema version of the bootstrap file.
const Version = 2

// Config is the bootstrap file, version 2.
type Config struct {
	Connections   map[string]Connection `json:"connections,omitempty"  yaml:"connections,omitempty"`
	Exporters     map[string]Exporter   `json:"exporters,omitempty"    yaml:"exporters,omitempty"`
	Notifiers     map[string]Notifier   `json:"notifiers,omitempty"    yaml:"notifiers,omitempty"`
	ControlPlane  *ControlPlane         `json:"controlPlane,omitempty" yaml:"controlPlane,omitempty"`
	Server        Server                `json:"server,omitzero"        yaml:"server"`
	Observability Observability         `json:"observability,omitzero" yaml:"observability"`
	Auth          Auth                  `json:"auth,omitzero"          yaml:"auth"`
	Version       int                   `json:"version"                jsonschema:"enum=2"           yaml:"version"`
}

// Listener roles and protocols.
const (
	RoleAPI = "api"
	RoleOps = "ops"

	ProtocolHTTP1 = "http1"
	ProtocolH2C   = "h2c"
)

// Server is the network side of the relay.
type Server struct {
	// UnixSocketPath is the path of a unix socket the api listener also serves on.
	UnixSocketPath string     `json:"unixSocketPath,omitempty" yaml:"unixSocketPath,omitempty"`
	Listeners      []Listener `json:"listeners,omitempty"      yaml:"listeners,omitempty"`
}

// Listener is one address the relay listens on.
type Listener struct {
	Name    string `json:"name"    yaml:"name"`
	Address string `json:"address" yaml:"address"`
	// Role is "api" (default) or "ops".
	Role string `json:"role,omitempty" yaml:"role,omitempty"`
	// Protocols are "http1" (default) and "h2c". "h2" and "tls" are reserved.
	Protocols []string `json:"protocols,omitempty" yaml:"protocols,omitempty"`
}

// Auth holds the admin API keys.
type Auth struct {
	// AdminKeys are plain strings or secret references. They are never printed.
	AdminKeys []AdminKey `json:"adminKeys,omitempty" yaml:"adminKeys,omitempty"`
}
