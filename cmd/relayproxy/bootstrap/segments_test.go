package bootstrap_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

const (
	addressPath  = "connections.v.addresses[0]"
	segmentsHead = "version: 2\nconnections:\n  v: {type: valkey, addresses: [\"h:1\"]}\n" +
		"  r: {type: redis, addresses: [\"h:1\"]}\n"
	dbPath = "connections.v.db"
)

func TestSegmentConfigErrors(t *testing.T) {
	t.Parallel()

	runParseErrors(t, []parseErrorCase{
		{
			name:    "definitions are not part of the bootstrap file",
			doc:     segmentsHead + "segments:\n  definitions: {}\n",
			wantErr: bootstrap.ErrUnknownKey, wantPath: "segments.definitions", wantLine: 6,
		},
		{
			name:    "invalid normalization",
			doc:     segmentsHead + "segments:\n  resolver:\n    default: {normalize: upper}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "segments.resolver.default.normalize", wantLine: 7,
		},
		{
			name:    "zero shards",
			doc:     segmentsHead + "segments:\n  resolver:\n    default: {shards: 0}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "segments.resolver.default.shards", wantLine: 7,
		},
		{
			name:    "zero call timeout",
			doc:     segmentsHead + "segments:\n  resolver: {callTimeout: 0s}\n",
			wantErr: bootstrap.ErrInvalidDuration, wantPath: "segments.resolver.callTimeout", wantLine: 6,
		},
		{
			name:    "negative ttl",
			doc:     segmentsHead + "segments:\n  resolver:\n    default: {ttl: -1s}\n",
			wantErr: bootstrap.ErrInvalidDuration, wantPath: "segments.resolver.default.ttl", wantLine: 7,
		},
		{
			name:    "zero max overdue",
			doc:     segmentsHead + "segments:\n  resolver: {maxOverdue: 0}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "segments.resolver.maxOverdue", wantLine: 6,
		},
		{
			name:    "zero max id bytes",
			doc:     segmentsHead + "segments:\n  resolver: {maxIdBytes: 0}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "segments.resolver.maxIdBytes", wantLine: 6,
		},
	})
}

func TestStoreConnectionErrors(t *testing.T) {
	t.Parallel()

	runParseErrors(t, []parseErrorCase{
		{
			name:    "inline password",
			doc:     "version: 2\nconnections:\n  v: {type: valkey, addresses: [\"h:1\"], password: hunter2}\n",
			wantErr: bootstrap.ErrInlineSecret, wantPath: "connections.v.password", wantLine: 3,
		},
		{
			name:    "no address",
			doc:     "version: 2\nconnections:\n  v: {type: redis}\n",
			wantErr: bootstrap.ErrMissingValue, wantPath: "connections.v.addresses", wantLine: 3,
		},
		{
			name:    "address without port",
			doc:     "version: 2\nconnections:\n  v: {type: redis, addresses: [host]}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: addressPath, wantLine: 3,
		},
		{
			name:    "credentials in the address",
			doc:     "version: 2\nconnections:\n  v: {type: redis, addresses: [\"u:pw@host:1\"]}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: addressPath, wantLine: 3,
		},
		{
			name:    "duplicate address",
			doc:     "version: 2\nconnections:\n  v: {type: redis, addresses: [\"h:1\", \"h:1\"]}\n",
			wantErr: bootstrap.ErrDuplicateName, wantPath: "connections.v.addresses[1]", wantLine: 3,
		},
		{
			name:    "bad pipelining",
			doc:     "version: 2\nconnections:\n  v: {type: valkey, addresses: [\"h:1\"], pipelining: never}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "connections.v.pipelining", wantLine: 3,
		},
		{
			name:    "negative blocking pipeline",
			doc:     "version: 2\nconnections:\n  v: {type: valkey, addresses: [\"h:1\"], blockingPipeline: -1}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "connections.v.blockingPipeline", wantLine: 3,
		},
		{
			name:    "redis key on a valkey connection",
			doc:     "version: 2\nconnections:\n  v: {type: valkey, addresses: [\"h:1\"], db: 1}\n",
			wantErr: bootstrap.ErrUnknownKey, wantPath: dbPath, wantLine: 3,
		},
		{
			name:    "database of a cluster",
			doc:     "version: 2\nconnections:\n  v: {type: redis, addresses: [\"h:1\", \"h:2\"], db: 1}\n",
			wantErr: bootstrap.ErrNotAllowed, wantPath: dbPath, wantLine: 3,
		},
		{
			name:    "pool size",
			doc:     "version: 2\nconnections:\n  v: {type: redis, addresses: [\"h:1\"], poolSize: 0}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "connections.v.poolSize", wantLine: 3,
		},
		{
			name:    "max retries",
			doc:     "version: 2\nconnections:\n  v: {type: redis, addresses: [\"h:1\"], maxRetries: -2}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "connections.v.maxRetries", wantLine: 3,
		},
		{
			name:    "read timeout",
			doc:     "version: 2\nconnections:\n  v: {type: redis, addresses: [\"h:1\"], timeouts: {read: 0s}}\n",
			wantErr: bootstrap.ErrInvalidDuration, wantPath: "connections.v.timeouts.read", wantLine: 3,
		},
		{
			name:     "ca bundle without tls",
			doc:      "version: 2\nconnections:\n  v: {type: valkey, addresses: [\"h:1\"], tls: {caBundleFile: /ca.pem}}\n",
			wantErr:  bootstrap.ErrNotAllowed,
			wantPath: "connections.v.tls.caBundleFile",
			wantLine: 3,
		},
		{
			name:     "two addresses and a single client",
			doc:      "version: 2\nconnections:\n  v: {type: valkey, addresses: [\"h:1\", \"h:2\"], forceSingleClient: true}\n",
			wantErr:  bootstrap.ErrNotAllowed,
			wantPath: "connections.v.forceSingleClient",
			wantLine: 3,
		},
		{
			name:    "key prefix with white space",
			doc:     "version: 2\nconnections:\n  v: {type: redis, addresses: [\"h:1\"], keyPrefix: \"a b\"}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "connections.v.keyPrefix", wantLine: 3,
		},
		{
			name:    "server name without tls",
			doc:     "version: 2\nconnections:\n  v: {type: redis, addresses: [\"h:1\"], tls: {serverName: x}}\n",
			wantErr: bootstrap.ErrNotAllowed, wantPath: "connections.v.tls.serverName", wantLine: 3,
		},
		{
			name:    "negative database",
			doc:     "version: 2\nconnections:\n  v: {type: redis, addresses: [\"h:1\"], db: -1}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: dbPath, wantLine: 3,
		},
		{
			name:    "lookup timeout",
			doc:     "version: 2\nconnections:\n  v: {type: valkey, addresses: [\"h:1\"], lookupTimeout: 0s}\n",
			wantErr: bootstrap.ErrInvalidDuration, wantPath: "connections.v.lookupTimeout", wantLine: 3,
		},
	})
}

// A rejected inline password is never part of the error text.
func TestStorePasswordIsNotInTheError(t *testing.T) {
	t.Parallel()

	const canary = "CANARY-store-password-71c2"

	_, err := bootstrap.Parse(
		[]byte("version: 2\nconnections:\n  v: {type: redis, addresses: [\"h:1\"], password: "+canary+"}\n"),
		nil, options(nil, nil, false))
	require.ErrorIs(t, err, bootstrap.ErrInlineSecret)
	assert.NotContains(t, err.Error(), canary)
}

func TestSegmentDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := bootstrap.Parse([]byte(segmentsHead+"segments: {}\n"), nil, options(nil, nil, false))
	require.NoError(t, err)

	got := cfg.Segments
	require.NotNil(t, got)

	res := got.Resolver
	assert.Equal(t, bootstrap.Duration(bootstrap.DefaultSegmentCallTimeout), *res.CallTimeout)
	assert.Equal(t, bootstrap.Duration(bootstrap.DefaultSegmentShutdownGrace), *res.ShutdownGrace)
	assert.Equal(t, bootstrap.DefaultSegmentMaxOverdue, *res.MaxOverdue)
	assert.Equal(t, bootstrap.DefaultSegmentMaxIDBytes, *res.MaxIDBytes)
	assert.Equal(t, bootstrap.NormalizeNone, res.Default.Normalize)
	assert.Equal(t, bootstrap.Duration(bootstrap.DefaultSegmentTTL), *res.Default.TTL)
	assert.Equal(t, bootstrap.Duration(bootstrap.DefaultSegmentNegativeTTL), *res.Default.NegativeTTL)
	assert.Equal(t, bootstrap.Duration(bootstrap.DefaultSegmentBreakerCooldown), *res.Default.BreakerCooldown)
	assert.Equal(t, bootstrap.DefaultSegmentMaxEntries, *res.Default.MaxEntries)
	assert.Equal(t, bootstrap.DefaultSegmentShards, *res.Default.Shards)
	assert.Equal(t, bootstrap.DefaultSegmentBreakerFailures, *res.Default.BreakerFailures)
	assert.False(t, *res.Default.NoCache)
	assert.False(t, *res.Default.NoBreaker)
	assert.Equal(t, bootstrap.PipeliningAlways, cfg.Connections["v"].Valkey.Pipelining)
}

func TestNoSegmentsSection(t *testing.T) {
	t.Parallel()

	cfg, err := bootstrap.Parse([]byte(control), nil, options(nil, nil, false))
	require.NoError(t, err)
	assert.Nil(t, cfg.Segments)

	var out bytes.Buffer
	require.NoError(t, cfg.Print(&out))
	assert.NotContains(t, out.String(), "segments")
}

// The defaults of the file are the ones of the resolver and of the providers.
func TestDefaultsMatchTheModules(t *testing.T) {
	t.Parallel()

	assert.Equal(t, bootstrap.DefaultSegmentTTL, resolver.DefaultTTL)
	assert.Equal(t, bootstrap.DefaultSegmentNegativeTTL, resolver.DefaultNegativeTTL)
	assert.Equal(t, bootstrap.DefaultSegmentMaxEntries, resolver.DefaultMaxEntries)
	assert.Equal(t, bootstrap.DefaultSegmentShards, resolver.DefaultShards)
	assert.Equal(t, bootstrap.DefaultSegmentBreakerFailures, resolver.DefaultBreakerFailures)
	assert.Equal(t, bootstrap.DefaultSegmentBreakerCooldown, resolver.DefaultBreakerCooldown)
	assert.Equal(t, bootstrap.DefaultSegmentCallTimeout, resolver.DefaultCallTimeout)
	assert.Equal(t, bootstrap.DefaultSegmentMaxOverdue, resolver.DefaultMaxOverdue)
	assert.Equal(t, bootstrap.DefaultSegmentMaxIDBytes, resolver.DefaultMaxIDBytes)
	assert.Equal(t, bootstrap.DefaultSegmentShutdownGrace, resolver.DefaultShutdownGrace)
	assert.Equal(t, bootstrap.DefaultLookupTimeout, redissegment.DefaultTimeout)
	assert.Equal(t, bootstrap.DefaultLookupTimeout, valkeysegment.DefaultTimeout)
	assert.Equal(t, bootstrap.DefaultSegmentKeyPrefix, redissegment.DefaultKeyPrefix)
	assert.Equal(t, bootstrap.DefaultSegmentKeyPrefix, valkeysegment.DefaultKeyPrefix)
	assert.Equal(t, bootstrap.PipeliningAlways, string(valkeysegment.PipeliningAlways))
	assert.Equal(t, bootstrap.PipeliningAuto, string(valkeysegment.PipeliningAuto))
	assert.Equal(t, bootstrap.NormalizeTrimLower, string(resolver.NormalizeTrimLower))
	assert.Equal(t, 100*time.Millisecond, bootstrap.DefaultLookupTimeout)
}

func TestStoreAddressErrors(t *testing.T) {
	t.Parallel()

	runParseErrors(t, []parseErrorCase{
		{
			name:    "white space in the host",
			doc:     "version: 2\nconnections:\n  v: {type: redis, addresses: [\"redis :6379\"]}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: addressPath, wantLine: 3,
		},
		{
			name:    "trailing white space",
			doc:     "version: 2\nconnections:\n  v: {type: valkey, addresses: [\"redis:6379 \"]}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: addressPath, wantLine: 3,
		},
		{
			name:    "empty host",
			doc:     "version: 2\nconnections:\n  v: {type: redis, addresses: [\":6379\"]}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: addressPath, wantLine: 3,
		},
	})
}

func TestStoreAddressesAccepted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		address       string
		wantAddresses []string
	}{
		{name: "host", address: "redis:6379", wantAddresses: []string{"redis:6379"}},
		{name: "bracketed IPv6", address: "[::1]:6379", wantAddresses: []string{"[::1]:6379"}},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			doc := "version: 2\nconnections:\n  v: {type: redis, addresses: [\"" + tcase.address + "\"]}\n"

			cfg, err := bootstrap.Parse([]byte(doc), nil, options(nil, nil, false))
			require.NoError(t, err)
			assert.Equal(t, tcase.wantAddresses, cfg.Connections["v"].Redis.Addresses)
		})
	}
}
