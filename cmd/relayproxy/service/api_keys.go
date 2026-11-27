package service

// APIKeys answers which API keys the relay proxy accepts. The keys change at reload: APIKeys asks its source on every
// call, so the HTTP layer observes the change without owning the keys.
// It is a value that wraps the source, copying it shares the same live keys.
// Without a source (the zero value) it behaves like an empty configuration: no key is accepted and the authentication
// is disabled.
type APIKeys struct {
	src KeySource
}

// KeySource is the source of the API keys: the config v2 core, whose routing changes at reload. Its methods are
// called for every request and must not allocate.
type KeySource interface {
	// AuthenticationEnabled reports whether the evaluation requests must carry an accepted key.
	AuthenticationEnabled() bool
	// KeyExists reports whether the key is accepted, whatever its kind.
	KeyExists(apiKey string) bool
	// AdminKeyExists reports whether the key is an admin key.
	AdminKeyExists(apiKey string) bool
}

// NewAPIKeysFrom returns the accessor of the API keys of a source.
func NewAPIKeysFrom(src KeySource) APIKeys { return APIKeys{src: src} }

// AuthenticationEnabled reports whether the evaluation requests must carry an accepted key.
func (k APIKeys) AuthenticationEnabled() bool {
	return k.src != nil && k.src.AuthenticationEnabled()
}

// KeyExists reports whether the key is accepted by the relay proxy, whatever its kind.
func (k APIKeys) KeyExists(apiKey string) bool {
	return k.src != nil && k.src.KeyExists(apiKey)
}

// AdminKeyExists reports whether the key is an admin key.
func (k APIKeys) AdminKeyExists(apiKey string) bool {
	return k.src != nil && k.src.AdminKeyExists(apiKey)
}
