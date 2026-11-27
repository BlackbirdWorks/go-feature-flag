package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
)

func TestAPIKeys(t *testing.T) {
	t.Parallel()

	conf := &config.Config{AuthorizedKeys: config.APIKeys{Evaluation: []string{"user"}, Admin: []string{"admin"}}}
	conf.ForceReloadAPIKeys()

	keys := service.NewAPIKeys(conf)

	assert.True(t, keys.AuthenticationEnabled())
	assert.True(t, keys.KeyExists("user"))
	assert.True(t, keys.KeyExists("admin"))
	assert.False(t, keys.KeyExists("other"))
	assert.False(t, keys.AdminKeyExists("user"))
	assert.True(t, keys.AdminKeyExists("admin"))

	// the accessor follows the configuration: the keys that a reload replaces in place are the ones it answers with,
	// and so does a copy of it.
	copied := keys

	conf.ReplaceAPIKeys(config.APIKeys{Admin: []string{"rotated"}})

	for _, k := range []service.APIKeys{keys, copied} {
		assert.False(t, k.AuthenticationEnabled(), "only admin keys: the evaluation endpoints are not authenticated")
		assert.False(t, k.KeyExists("user"))
		assert.True(t, k.AdminKeyExists("rotated"))
		assert.False(t, k.AdminKeyExists("admin"))
	}
}

// TestAPIKeys_WithoutConfiguration checks that an accessor without configuration behaves like the empty
// configuration of v1: nothing is accepted and the authentication is off.
func TestAPIKeys_WithoutConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		keys service.APIKeys
		name string
	}{
		{name: "zero value", keys: service.APIKeys{}},
		{name: "nil configuration", keys: service.NewAPIKeys(nil)},
		{name: "empty configuration", keys: service.NewAPIKeys(&config.Config{})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.False(t, tt.keys.AuthenticationEnabled())
			assert.False(t, tt.keys.KeyExists("key"))
			assert.False(t, tt.keys.AdminKeyExists("key"))
		})
	}
}

// fixedKeys is a source of keys: one user key, one admin key.
type fixedKeys struct{}

func (fixedKeys) AuthenticationEnabled() bool { return true }

func (fixedKeys) KeyExists(apiKey string) bool { return apiKey == "user" || apiKey == "admin" }

func (fixedKeys) AdminKeyExists(apiKey string) bool { return apiKey == "admin" }

// TestAPIKeys_FromSource checks that a source answers instead of a configuration.
func TestAPIKeys_FromSource(t *testing.T) {
	t.Parallel()

	keys := service.NewAPIKeysFrom(fixedKeys{})

	assert.True(t, keys.AuthenticationEnabled())
	assert.True(t, keys.KeyExists("user"))
	assert.False(t, keys.KeyExists("other"))
	assert.False(t, keys.AdminKeyExists("user"))
	assert.True(t, keys.AdminKeyExists("admin"))
}
