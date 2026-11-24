package config

// APIKeys is a struct to store the API keys for the different endpoints.
type APIKeys struct {
	Admin      []string `mapstructure:"admin"      koanf:"admin"`
	Evaluation []string `mapstructure:"evaluation" koanf:"evaluation"`
}

type ApiKeyType = string

// Enum for the type of the API keys.
const (
	EvaluationKeyType ApiKeyType = "EVALUATION"
	AdminKeyType      ApiKeyType = "ADMIN"
	FlagSetKeyType    ApiKeyType = "FLAGSET"
	ErrorKeyType      ApiKeyType = "ERROR"
)

// APIKeysAdminExists is checking if an admin API Key exist in the relay proxy configuration.
func (c *Config) APIKeysAdminExists(apiKey string) bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return c.apiKeysSet[apiKey] == AdminKeyType
}

// APIKeyExists is checking if an API Key exist in the relay proxy configuration.
func (c *Config) APIKeyExists(apiKey string) bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	_, ok := c.apiKeysSet[apiKey]

	return ok
}

// IsAuthenticationEnabled returns true if we need to be authenticated.
func (c *Config) IsAuthenticationEnabled() bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return c.forceAuthenticatedRequests
}

// preloadAPIKeysLocked performs the actual API key preloading logic.
// It assumes the caller holds the write lock (c.mutex.Lock()).
// This function should never be called directly; use preloadAPIKeys() or ForceReloadAPIKeys() instead.
func (c *Config) preloadAPIKeysLocked() {
	apiKeySet := make(map[string]ApiKeyType)
	addAPIKeys := func(keys []string, keyType ApiKeyType) {
		for _, k := range keys {
			apiKeySet[k] = keyType
		}
	}

	// In flag set mode an evaluation request is routed by its key to a flag set, so only the keys of
	// the flag sets can evaluate. authorizedKeys.evaluation and the top-level apiKeys are not linked
	// to any flag set: accepting them would authenticate a request that then fails the flag set lookup.
	if len(c.FlagSets) > 0 {
		c.hadFlagSets = true
	}
	if !c.hadFlagSets {
		addAPIKeys(c.AuthorizedKeys.Evaluation, EvaluationKeyType)
		addAPIKeys(c.APIKeys, EvaluationKeyType)
	}

	for _, flagSet := range c.FlagSets {
		addAPIKeys(flagSet.APIKeys, FlagSetKeyType)
	}

	// it has to be before adding the admin keys, because when we add only the admin keys,
	// we don't want to force the authentication (except for the admin endpoints).
	if len(apiKeySet) > 0 {
		c.forceAuthenticatedRequests = true
	} else {
		c.forceAuthenticatedRequests = false
	}

	addAPIKeys(c.AuthorizedKeys.Admin, AdminKeyType)
	c.apiKeysSet = apiKeySet
}

// preloadAPIKeys is storing in the struct all the API Keys available for the relay-proxy.
func (c *Config) preloadAPIKeys() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.preloadAPIKeysLocked()
}

// ForceReloadAPIKeys is forcing the reload of the API Keys.
// This is used to reload the API Keys when the configuration changes.
// The entire reload operation is atomic to prevent race conditions.
func (c *Config) ForceReloadAPIKeys() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.forceAuthenticatedRequests = false
	c.apiKeysSet = nil
	c.preloadAPIKeysLocked()
}

// ReplaceAPIKeys replaces the authorized keys and the (deprecated) top-level API keys and rebuilds the
// accepted key set in a single critical section: a concurrent request is authenticated against either the
// previous or the new keys, never against a partially updated set.
func (c *Config) ReplaceAPIKeys(authorizedKeys APIKeys, apiKeys []string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.AuthorizedKeys = authorizedKeys
	c.APIKeys = apiKeys
	c.preloadAPIKeysLocked()
}

// HasUnroutableEvaluationKeys returns true when the configuration uses flag sets and also sets
// authorizedKeys.evaluation or the top-level apiKeys. Those keys are not linked to a flag set, so they are
// not accepted on the evaluation endpoints in flag set mode.
func (c *Config) HasUnroutableEvaluationKeys() bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return (c.hadFlagSets || len(c.FlagSets) > 0) && (len(c.AuthorizedKeys.Evaluation) > 0 || len(c.APIKeys) > 0)
}
