package retrieverconf

import (
	"crypto/tls"
	"time"

	"github.com/redis/go-redis/v9"
)

// SerializableRedisOptions is a JSON-serializable alternative to redis.Options.
// It contains only the fields that can be serialized and are most commonly used.
// This allows the configuration to be passed via command line or JSON files.
type SerializableRedisOptions struct {
	// Network type, either tcp or unix. Default is tcp.
	Network string `json:"network,omitempty" mapstructure:"network"`
	// Addr is the address formatted as host:port
	Addr string `json:"addr" mapstructure:"addr"`
	// ClientName will execute the `CLIENT SETNAME ClientName` command for each conn.
	ClientName string `json:"clientName,omitempty" mapstructure:"clientName"`
	// IdentitySuffix is an optional suffix to append to the client name.
	IdentitySuffix string `json:"identitySuffix,omitempty" mapstructure:"identitySuffix"`
	// Username is used to authenticate the current connection
	Username string `json:"username,omitempty" mapstructure:"username"`
	// Password is an optional password.
	Password string `json:"password,omitempty" mapstructure:"password"`
	// WriteTimeout for socket writes in milliseconds. If reached, commands will fail
	// with a timeout instead of blocking.
	// Default is ReadTimeout.
	WriteTimeoutMs int64 `json:"writeTimeout,omitempty" mapstructure:"writeTimeout"`
	// PoolSize is the maximum number of socket connections.
	// Default is 10 connections per every available CPU.
	PoolSize int `json:"poolSize,omitempty" mapstructure:"poolSize"`
	// MinRetryBackoff is the minimum backoff between each retry in milliseconds.
	// -1 disables backoff. Default is 8 milliseconds.
	MinRetryBackoffMs int64 `json:"minRetryBackoff,omitempty" mapstructure:"minRetryBackoff"`
	// MaxRetryBackoff is the maximum backoff between each retry in milliseconds.
	// -1 disables backoff. Default is 512 milliseconds.
	MaxRetryBackoffMs int64 `json:"maxRetryBackoff,omitempty" mapstructure:"maxRetryBackoff"`
	// DialTimeout for establishing new connections in milliseconds.
	// Default is 5 seconds (5000ms).
	DialTimeoutMs int64 `json:"dialTimeout,omitempty" mapstructure:"dialTimeout"`
	// ReadTimeout for socket reads in milliseconds. If reached, commands will fail
	// with a timeout instead of blocking.
	// -1 = no timeout (block indefinitely)
	// -2 = disables SetReadDeadline calls completely
	// Default is 3 seconds (3000ms).
	ReadTimeoutMs int64 `json:"readTimeout,omitempty" mapstructure:"readTimeout"`
	// DB is the database to be selected after connecting to the server.
	DB int `json:"db,omitempty" mapstructure:"db"`
	// Protocol 2 or 3. Use the version to negotiate RESP version with redis-server.
	// Default is 3.
	Protocol int `json:"protocol,omitempty" mapstructure:"protocol"`
	// ConnMaxLifetime is the maximum amount of time a connection may be reused in milliseconds.
	// Default is to not close aged connections.
	ConnMaxLifetimeMs int64 `json:"connMaxLifetime,omitempty" mapstructure:"connMaxLifetime"`
	// MaxRetries is the maximum number of retries before giving up.
	// -1 (not 0) disables retries. Default is 3 retries.
	MaxRetries int `json:"maxRetries,omitempty" mapstructure:"maxRetries"`
	// PoolTimeout is the amount of time client waits for connection if all
	// connections are busy before returning an error in milliseconds.
	// Default is ReadTimeout + 1 second.
	PoolTimeoutMs int64 `json:"poolTimeout,omitempty" mapstructure:"poolTimeout"`
	// MinIdleConns is the minimum number of idle connections which is useful when
	// establishing new connection is slow.
	// Default is 0 (no idle connections are retained).
	MinIdleConns int `json:"minIdleConns,omitempty" mapstructure:"minIdleConns"`
	// MaxIdleConns is the maximum number of idle connections.
	// Default is 0 (unlimited).
	MaxIdleConns int `json:"maxIdleConns,omitempty" mapstructure:"maxIdleConns"`
	// ConnMaxIdleTime is the maximum amount of time a connection may be idle in milliseconds.
	// Should be less than server's timeout.
	// Default is 30 minutes (1800000ms). -1 disables idle timeout check.
	ConnMaxIdleTimeMs int64 `json:"connMaxIdleTime,omitempty" mapstructure:"connMaxIdleTime"`
	// PoolFIFO uses FIFO mode for each node connection pool GET/PUT (default LIFO).
	PoolFIFO bool `json:"poolFifo,omitempty" mapstructure:"poolFIFO"`
	// TLS Config (simplified - only enable/disable for now)
	// For more complex TLS configurations, use the non-serializable RedisOptions
	TLSEnabled bool `json:"tlsEnabled,omitempty" mapstructure:"tlsEnabled"`
	// DisableIndentity disables set-lib on connect.
	DisableIndentity bool `json:"disableIndentity,omitempty" mapstructure:"disableIndentity"`
	// DisableIdentity disables set-lib on connect.
	DisableIdentity bool `json:"disableIdentity,omitempty" mapstructure:"disableIdentity"`
	// ContextTimeoutEnabled controls whether the client respects context timeouts and deadlines.
	// Default is false.
	ContextTimeoutEnabled bool `json:"contextTimeoutEnabled,omitempty" mapstructure:"contextTimeoutEnabled"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped
}

// ToRedisOptions converts SerializableRedisOptions to redis.Options.
func (s *SerializableRedisOptions) ToRedisOptions() *redis.Options {
	// DisableIdentity overrides DisableIndentity.
	// This is to maintain backward compatibility with the deprecated flag and to
	// be consistent with the non-serializable RedisOptions.
	disableIdentity := s.DisableIndentity
	if s.DisableIdentity {
		disableIdentity = true
	}

	opts := &redis.Options{
		Network:               s.Network,
		Addr:                  s.Addr,
		ClientName:            s.ClientName,
		Protocol:              s.Protocol,
		Username:              s.Username,
		Password:              s.Password,
		DB:                    s.DB,
		PoolFIFO:              s.PoolFIFO,
		PoolSize:              s.PoolSize,
		MinIdleConns:          s.MinIdleConns,
		MaxIdleConns:          s.MaxIdleConns,
		DisableIdentity:       disableIdentity,
		IdentitySuffix:        s.IdentitySuffix,
		ContextTimeoutEnabled: s.ContextTimeoutEnabled,
	}

	// Convert milliseconds to time.Duration
	if s.MinRetryBackoffMs != 0 {
		opts.MinRetryBackoff = time.Duration(s.MinRetryBackoffMs) * time.Millisecond
	}

	if s.MaxRetryBackoffMs != 0 {
		opts.MaxRetryBackoff = time.Duration(s.MaxRetryBackoffMs) * time.Millisecond
	}

	if s.DialTimeoutMs != 0 {
		opts.DialTimeout = time.Duration(s.DialTimeoutMs) * time.Millisecond
	}

	if s.ReadTimeoutMs != 0 {
		opts.ReadTimeout = time.Duration(s.ReadTimeoutMs) * time.Millisecond
	}

	if s.WriteTimeoutMs != 0 {
		opts.WriteTimeout = time.Duration(s.WriteTimeoutMs) * time.Millisecond
	}

	if s.PoolTimeoutMs != 0 {
		opts.PoolTimeout = time.Duration(s.PoolTimeoutMs) * time.Millisecond
	}

	if s.ConnMaxIdleTimeMs != 0 {
		opts.ConnMaxIdleTime = time.Duration(s.ConnMaxIdleTimeMs) * time.Millisecond
	}

	if s.ConnMaxLifetimeMs != 0 {
		opts.ConnMaxLifetime = time.Duration(s.ConnMaxLifetimeMs) * time.Millisecond
	}

	if s.MaxRetries != 0 {
		opts.MaxRetries = s.MaxRetries
	}

	// TLS configuration (basic)
	if s.TLSEnabled {
		opts.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	return opts
}
