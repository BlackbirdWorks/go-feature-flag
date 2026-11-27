package bootstrap

import "time"

// The defaults of the segment resolver and of the segment stores. They are the defaults of
// modules/core/segment/resolver and of the segment providers (a test pins them).
const (
	DefaultSegmentTTL             = 30 * time.Second
	DefaultSegmentNegativeTTL     = 5 * time.Second
	DefaultSegmentMaxEntries      = 10000
	DefaultSegmentShards          = 16
	DefaultSegmentBreakerFailures = 5
	DefaultSegmentBreakerCooldown = 10 * time.Second
	DefaultSegmentCallTimeout     = 2 * time.Second
	DefaultSegmentMaxOverdue      = 8
	DefaultSegmentMaxIDBytes      = 512
	DefaultSegmentShutdownGrace   = 5 * time.Second
	DefaultLookupTimeout          = 100 * time.Millisecond
	DefaultSegmentKeyPrefix       = "semaphore:segment:"
)

// Normalization of the ids of a lookup.
const (
	NormalizeNone      = "none"
	NormalizeTrimLower = "trimLower"
)

// Segments configures the process-level settings of the segment stores. The segment definitions are published
// content: they come from the control plane, not from this file.
type Segments struct {
	Resolver SegmentResolver `json:"resolver,omitzero" yaml:"resolver"`
}

// SegmentResolver is the settings of the resolver that sits in front of the stores.
type SegmentResolver struct {
	// CallTimeout bounds the shared call to a store. Default 2s.
	CallTimeout *Duration `json:"callTimeout,omitempty" yaml:"callTimeout,omitempty"`
	// ShutdownGrace bounds how long the old resolver may take to stop after a reload. Default 5s.
	ShutdownGrace *Duration `json:"shutdownGrace,omitempty" yaml:"shutdownGrace,omitempty"`
	// MaxOverdue is the number of overdue store calls from which no new call starts. Default 8.
	MaxOverdue *int `json:"maxOverdue,omitempty" jsonschema:"minimum=1" yaml:"maxOverdue,omitempty"`
	// MaxIDBytes is the longest id that is looked up. Default 512.
	MaxIDBytes *int `json:"maxIdBytes,omitempty" jsonschema:"minimum=1" yaml:"maxIdBytes,omitempty"`
	// Default is the cache and breaker settings of every segment.
	Default SegmentTuning `json:"default,omitzero" yaml:"default"`
}

// SegmentTuning is the cache and breaker settings of the segments. An unset value takes the default.
type SegmentTuning struct {
	// TTL is how long a member is cached. Default 30s.
	TTL *Duration `json:"ttl,omitempty" yaml:"ttl,omitempty"`
	// NegativeTTL is how long a non-member is cached. Default 5s.
	NegativeTTL *Duration `json:"negativeTtl,omitempty" yaml:"negativeTtl,omitempty"`
	// BreakerCooldown is how long the breaker stays open. Default 10s.
	BreakerCooldown *Duration `json:"breakerCooldown,omitempty" yaml:"breakerCooldown,omitempty"`
	// MaxEntries bounds the cache of a segment. Default 10000.
	MaxEntries *int `json:"maxEntries,omitempty" jsonschema:"minimum=1" yaml:"maxEntries,omitempty"`
	// Shards is the number of locks of the cache. Default 16.
	Shards *int `json:"shards,omitempty" jsonschema:"minimum=1" yaml:"shards,omitempty"`
	// BreakerFailures is the number of consecutive failed calls that open the breaker. Default 5.
	BreakerFailures *int `json:"breakerFailures,omitempty" jsonschema:"minimum=1" yaml:"breakerFailures,omitempty"`
	// NoCache turns the cache off.
	NoCache *bool `json:"noCache,omitempty" yaml:"noCache,omitempty"`
	// NoBreaker turns the breaker off.
	NoBreaker *bool `json:"noBreaker,omitempty" yaml:"noBreaker,omitempty"`
	// Normalize is none (default) or trimLower, applied to the ids before the cache and the store.
	Normalize string `json:"normalize,omitempty" jsonschema:"enum=none,enum=trimLower" yaml:"normalize,omitempty"`
}

func (s *Segments) applyDefaults() {
	r := &s.Resolver
	r.CallTimeout = orDefault(r.CallTimeout, Duration(DefaultSegmentCallTimeout))
	r.ShutdownGrace = orDefault(r.ShutdownGrace, Duration(DefaultSegmentShutdownGrace))
	r.MaxOverdue = orDefault(r.MaxOverdue, DefaultSegmentMaxOverdue)
	r.MaxIDBytes = orDefault(r.MaxIDBytes, DefaultSegmentMaxIDBytes)

	def := &r.Default
	if def.Normalize == "" {
		def.Normalize = NormalizeNone
	}

	def.TTL = orDefault(def.TTL, Duration(DefaultSegmentTTL))
	def.NegativeTTL = orDefault(def.NegativeTTL, Duration(DefaultSegmentNegativeTTL))
	def.BreakerCooldown = orDefault(def.BreakerCooldown, Duration(DefaultSegmentBreakerCooldown))
	def.MaxEntries = orDefault(def.MaxEntries, DefaultSegmentMaxEntries)
	def.Shards = orDefault(def.Shards, DefaultSegmentShards)
	def.BreakerFailures = orDefault(def.BreakerFailures, DefaultSegmentBreakerFailures)
	def.NoCache = orDefault(def.NoCache, false)
	def.NoBreaker = orDefault(def.NoBreaker, false)
}
