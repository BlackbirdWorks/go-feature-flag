package bootstrap

// StoreTLS is the TLS setting of a redis or valkey connection. TLS is off unless Enable is true.
type StoreTLS struct {
	// ServerName overrides the name the server certificate is checked against.
	ServerName string `json:"serverName,omitempty" yaml:"serverName,omitempty"`
	// CABundleFile is a PEM file of certificate authorities trusted in addition to the system roots.
	CABundleFile string `json:"caBundleFile,omitempty" yaml:"caBundleFile,omitempty"`
	Enable       bool   `json:"enable,omitempty"       yaml:"enable,omitempty"`
}

// RedisTimeouts are the go-redis timeouts of a redis connection. An unset timeout keeps the go-redis default.
type RedisTimeouts struct {
	// Connect is the dial timeout.
	Connect *Duration `json:"connect,omitempty" yaml:"connect,omitempty"`
	// Read is the timeout of a socket read.
	Read *Duration `json:"read,omitempty" yaml:"read,omitempty"`
	// Write is the timeout of a socket write.
	Write *Duration `json:"write,omitempty" yaml:"write,omitempty"`
}

// ValkeyTimeouts are the valkey-go timeouts of a valkey connection. An unset timeout keeps the valkey-go default.
type ValkeyTimeouts struct {
	// Connect is the dial timeout.
	Connect *Duration `json:"connect,omitempty" yaml:"connect,omitempty"`
	// IO is the read and write timeout of each connection (valkey-go `ConnWriteTimeout`), from 10ms to 1m, and not
	// below lookupTimeout. Unset means ten lookup timeouts, at least 1s.
	IO *Duration `json:"io,omitempty" yaml:"io,omitempty"`
}

// RedisConnection is a connection of type redis: a store of dynamic segments, reached through go-redis.
type RedisConnection struct {
	TLS      *StoreTLS      `json:"tls,omitempty"      yaml:"tls,omitempty"`
	Timeouts *RedisTimeouts `json:"timeouts,omitempty" yaml:"timeouts,omitempty"`
	// MaxRetries is the go-redis `MaxRetries`: -1 turns the retries off. Unset keeps the go-redis default.
	MaxRetries *int `json:"maxRetries,omitempty" jsonschema:"minimum=-1" yaml:"maxRetries,omitempty"`
	// PoolSize is the go-redis `PoolSize`. Unset keeps the go-redis default.
	PoolSize *int `json:"poolSize,omitempty" jsonschema:"minimum=1" yaml:"poolSize,omitempty"`
	// LookupTimeout bounds one lookup call to the store. Unset means 100ms.
	LookupTimeout *Duration `json:"lookupTimeout,omitempty" yaml:"lookupTimeout,omitempty"`
	Type          string    `json:"type"                    jsonschema:"enum=redis"        yaml:"type"`
	Username      string    `json:"username,omitempty"      yaml:"username,omitempty"`
	// KeyPrefix is the default prefix of the keys of the segments. Unset means semaphore:segment:.
	KeyPrefix string `json:"keyPrefix,omitempty" yaml:"keyPrefix,omitempty"`
	Password  Secret `json:"password,omitzero"   yaml:"password,omitempty"`
	// Addresses are host:port. One address is a single node; several are a cluster.
	Addresses []string `json:"addresses,omitempty" yaml:"addresses,omitempty"`
	// DB is the database number. It does not apply to a cluster.
	DB int `json:"db,omitempty" jsonschema:"minimum=0" yaml:"db,omitempty"`
	// Cluster makes a single address a cluster (a configuration endpoint). Several addresses are a cluster already.
	Cluster bool `json:"cluster,omitempty" yaml:"cluster,omitempty"`
	// Atomic checks the key and the ids of a set segment with one Lua script.
	Atomic bool `json:"atomic,omitempty" yaml:"atomic,omitempty"`
}

// Pipelining modes of a valkey connection.
const (
	PipeliningAlways = "always"
	PipeliningAuto   = "auto"
)

// ValkeyConnection is a connection of type valkey: a store of dynamic segments, reached through valkey-go.
type ValkeyConnection struct {
	TLS      *StoreTLS       `json:"tls,omitempty"      yaml:"tls,omitempty"`
	Timeouts *ValkeyTimeouts `json:"timeouts,omitempty" yaml:"timeouts,omitempty"`
	// Atomic sends the existence check and SMISMEMBER of a set segment in one MULTI/EXEC. Unset means on; it only
	// matters with segments.missingKey unknown. The transaction goes to the primary.
	Atomic *bool `json:"atomic,omitempty" yaml:"atomic,omitempty"`
	// LookupTimeout bounds one lookup call to the store. Unset means 100ms.
	LookupTimeout *Duration `json:"lookupTimeout,omitempty" yaml:"lookupTimeout,omitempty"`
	Type          string    `json:"type"                    jsonschema:"enum=valkey"       yaml:"type"`
	Username      string    `json:"username,omitempty"      yaml:"username,omitempty"`
	// Pipelining is always (default) or auto, as in the valkey segment provider.
	Pipelining string `json:"pipelining,omitempty" jsonschema:"enum=always,enum=auto,default=always" yaml:"pipelining,omitempty"` //nolint:lll // struct tags can not be wrapped
	// KeyPrefix is the default prefix of the keys of the segments. Unset means semaphore:segment:.
	KeyPrefix string `json:"keyPrefix,omitempty" yaml:"keyPrefix,omitempty"`
	Password  Secret `json:"password,omitzero"   yaml:"password,omitempty"`
	// Addresses are host:port. valkey-go finds out whether they are a cluster.
	Addresses []string `json:"addresses,omitempty" yaml:"addresses,omitempty"`
	// BlockingPipeline is the number of commands of one call from which valkey-go uses a dedicated connection.
	// Zero (default) means never.
	BlockingPipeline int `json:"blockingPipeline,omitempty" jsonschema:"minimum=0" yaml:"blockingPipeline,omitempty"`
	// ForceSingleClient keeps valkey-go from probing for a cluster when one address is given.
	ForceSingleClient bool `json:"forceSingleClient,omitempty" yaml:"forceSingleClient,omitempty"`
	// Retry lets valkey-go send a read again after a network error. Default false.
	Retry bool `json:"retry,omitempty" yaml:"retry,omitempty"`
}
