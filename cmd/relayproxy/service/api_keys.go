package service

import "github.com/blackbirdworks/semaphore/cmd/relayproxy/config"

// APIKeys answers which API keys the relay proxy accepts. The keys change at reload: the v1 configuration is updated
// in place (flag set rotation, configuration file change) and APIKeys reads it on every call, so the HTTP layer
// observes the change without owning a configuration.
// It is a value that wraps the pointer of the configuration, copying it shares the same live keys.
// Without a configuration (the zero value, or NewAPIKeys(nil)) it behaves like an empty configuration: no key is
// accepted and the authentication is disabled.
type APIKeys struct {
	cfg *config.Config
	src KeySource
}

// KeySource is another source of API keys than the v1 configuration: the config v2 core, whose routing changes at
// reload. Its methods are called for every request and must not allocate.
type KeySource interface {
	// AuthenticationEnabled reports whether the evaluation requests must carry an accepted key.
	AuthenticationEnabled() bool
	// KeyExists reports whether the key is accepted, whatever its kind.
	KeyExists(apiKey string) bool
	// AdminKeyExists reports whether the key is an admin key.
	AdminKeyExists(apiKey string) bool
}

// NewAPIKeysFrom returns the accessor of the API keys of a source, which takes precedence over a configuration.
func NewAPIKeysFrom(src KeySource) APIKeys { return APIKeys{src: src} }

// NewAPIKeys returns the accessor of the API keys of the configuration.
func NewAPIKeys(cfg *config.Config) APIKeys { return APIKeys{cfg: cfg} }

// AuthenticationEnabled reports whether the evaluation requests must carry an accepted key.
func (k APIKeys) AuthenticationEnabled() bool {
	if k.src != nil {
		return k.src.AuthenticationEnabled()
	}

	return k.cfg != nil && k.cfg.IsAuthenticationEnabled()
}

// KeyExists reports whether the key is accepted by the relay proxy, whatever its kind.
func (k APIKeys) KeyExists(apiKey string) bool {
	if k.src != nil {
		return k.src.KeyExists(apiKey)
	}

	return k.cfg != nil && k.cfg.APIKeyExists(apiKey)
}

// AdminKeyExists reports whether the key is an admin key.
func (k APIKeys) AdminKeyExists(apiKey string) bool {
	if k.src != nil {
		return k.src.AdminKeyExists(apiKey)
	}

	return k.cfg != nil && k.cfg.APIKeysAdminExists(apiKey)
}
