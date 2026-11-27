package retrieverconf_test

import (
	"crypto/tls"
	"encoding/json"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/evaluate/retrieverconf"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	localhost6379       = "localhost:6379"
	redisExampleCom6380 = "redis.example.com:6380"
)

func TestSerializableRedisOptions_ToRedisOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input *retrieverconf.SerializableRedisOptions
		// want is the whole expected configuration of the redis client
		want *redis.Options
		name string
	}{
		{
			name: "basic configuration with addr and password",
			input: &retrieverconf.SerializableRedisOptions{
				Addr:     localhost6379,
				Password: fixtures.SecretValue,
				DB:       1,
			},
			want: &redis.Options{Addr: localhost6379, Password: fixtures.SecretValue, DB: 1},
		},
		{
			name: "full configuration",
			input: &retrieverconf.SerializableRedisOptions{
				Network:               protocol.NetworkTCP,
				Addr:                  redisExampleCom6380,
				ClientName:            "semaphore",
				Protocol:              3,
				Username:              fixtures.UserAdmin,
				Password:              "password123",
				DB:                    2,
				MaxRetries:            5,
				MinRetryBackoffMs:     100,
				MaxRetryBackoffMs:     1000,
				DialTimeoutMs:         10000,
				ReadTimeoutMs:         5000,
				WriteTimeoutMs:        5000,
				PoolSize:              20,
				PoolTimeoutMs:         6000,
				MinIdleConns:          5,
				MaxIdleConns:          15,
				ConnMaxIdleTimeMs:     300000,
				ConnMaxLifetimeMs:     600000,
				PoolFIFO:              true,
				ContextTimeoutEnabled: true,
				DisableIndentity:      false,
				IdentitySuffix:        fixtures.PlaceholderTest,
			},
			want: &redis.Options{
				Network: protocol.NetworkTCP, Addr: redisExampleCom6380, ClientName: "semaphore", Protocol: 3,
				Username: fixtures.UserAdmin, Password: "password123", DB: 2, MaxRetries: 5,
				MinRetryBackoff: 100 * time.Millisecond, MaxRetryBackoff: 1000 * time.Millisecond,
				DialTimeout: 10000 * time.Millisecond, ReadTimeout: 5000 * time.Millisecond,
				WriteTimeout: 5000 * time.Millisecond, PoolSize: 20, PoolTimeout: 6000 * time.Millisecond,
				MinIdleConns: 5, MaxIdleConns: 15, ConnMaxIdleTime: 300000 * time.Millisecond,
				ConnMaxLifetime: 600000 * time.Millisecond, PoolFIFO: true, ContextTimeoutEnabled: true,
				IdentitySuffix: fixtures.PlaceholderTest,
			},
		},
		{
			name: "TLS enabled",
			input: &retrieverconf.SerializableRedisOptions{
				Addr:       "secure-redis.example.com:6379",
				TLSEnabled: true,
			},
			want: &redis.Options{
				Addr:      "secure-redis.example.com:6379",
				TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
		},
		{
			name: "TLS disabled",
			input: &retrieverconf.SerializableRedisOptions{
				Addr:       "redis.example.com:6379",
				TLSEnabled: false,
			},
			want: &redis.Options{Addr: "redis.example.com:6379"},
		},
		{
			name: "zero values are not set (use redis defaults)",
			input: &retrieverconf.SerializableRedisOptions{
				Addr: localhost6379,
			},
			want: &redis.Options{Addr: localhost6379},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := tt.input.ToRedisOptions()
			require.NotNil(t, result)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestSerializableRedisOptions_JSONSerialization(t *testing.T) {
	t.Parallel()

	basic := &retrieverconf.SerializableRedisOptions{
		Addr:     localhost6379,
		Password: fixtures.SecretValue,
		DB:       1,
	}
	full := &retrieverconf.SerializableRedisOptions{
		Network:               protocol.NetworkTCP,
		Addr:                  redisExampleCom6380,
		ClientName:            "test-client",
		Protocol:              3,
		Username:              "user",
		Password:              "pass",
		DB:                    2,
		MaxRetries:            5,
		MinRetryBackoffMs:     100,
		MaxRetryBackoffMs:     1000,
		DialTimeoutMs:         10000,
		ReadTimeoutMs:         5000,
		WriteTimeoutMs:        5000,
		PoolSize:              20,
		PoolTimeoutMs:         6000,
		MinIdleConns:          5,
		MaxIdleConns:          15,
		ConnMaxIdleTimeMs:     300000,
		ConnMaxLifetimeMs:     600000,
		TLSEnabled:            true,
		PoolFIFO:              true,
		ContextTimeoutEnabled: true,
		DisableIndentity:      false,
		IdentitySuffix:        fixtures.PlaceholderTest,
	}

	tests := []struct {
		opts *retrieverconf.SerializableRedisOptions
		// wantDecoded is what decoding the JSON of opts gives back: every option survives the round trip
		wantDecoded *retrieverconf.SerializableRedisOptions
		name        string
	}{
		{name: "basic options", opts: basic, wantDecoded: basic},
		{name: "full options", opts: full, wantDecoded: full},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			jsonData, err := json.Marshal(tt.opts)
			require.NoError(t, err)

			var decoded retrieverconf.SerializableRedisOptions
			require.NoError(t, json.Unmarshal(jsonData, &decoded))

			assert.Equal(t, tt.wantDecoded, &decoded)
		})
	}
}

func TestSerializableRedisOptions_JSONStructure(t *testing.T) {
	t.Parallel()

	// This test ensures the JSON structure is as expected
	jsonStr := `{
		"addr": "localhost:6379",
		"password": "secret",
		"db": 1,
		"username": "admin",
		"maxRetries": 3,
		"dialTimeout": 5000,
		"readTimeout": 3000,
		"tlsEnabled": true
	}`

	var opts retrieverconf.SerializableRedisOptions

	err := json.Unmarshal([]byte(jsonStr), &opts)
	require.NoError(t, err)

	assert.Equal(t, localhost6379, opts.Addr)
	assert.Equal(t, fixtures.SecretValue, opts.Password)
	assert.Equal(t, 1, opts.DB)
	assert.Equal(t, fixtures.UserAdmin, opts.Username)
	assert.Equal(t, 3, opts.MaxRetries)
	assert.Equal(t, int64(5000), opts.DialTimeoutMs)
	assert.Equal(t, int64(3000), opts.ReadTimeoutMs)
	assert.True(t, opts.TLSEnabled)
}
