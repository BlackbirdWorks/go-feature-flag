package resolver

import (
	"errors"
	"fmt"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// Normalization is applied once to the id of a lookup, before the cache and the store.
type Normalization string

const (
	// NormalizeNone leaves the id as it is. It is the default.
	NormalizeNone Normalization = "none"
	// NormalizeTrimLower trims the white space around the id and lowers it.
	NormalizeTrimLower Normalization = "trimLower"
)

// The defaults of a segment whose option is zero.
const (
	DefaultTTL             = 30 * time.Second
	DefaultNegativeTTL     = 5 * time.Second
	DefaultMaxEntries      = 10000
	DefaultShards          = 16
	DefaultBreakerFailures = 5
	DefaultBreakerCooldown = 10 * time.Second
	DefaultCallTimeout     = 2 * time.Second
	// DefaultMaxOverdue is the number of overdue shared calls from which no new call is started (see Options.MaxOverdue).
	DefaultMaxOverdue = 8
	// DefaultMaxIDBytes is the longest id that is looked up: a longer one is left Unknown and never sent to the store.
	DefaultMaxIDBytes = 512
)

var (
	// ErrInvalidOptions is wrapped by the errors of options that are not valid.
	ErrInvalidOptions = errors.New("resolver: invalid options")
	// ErrNilProvider is returned when the wrapped provider is nil.
	ErrNilProvider = errors.New("resolver: nil provider")

	errNegative      = errors.New("a duration or count must not be negative")
	errBadName       = errors.New("invalid segment name")
	errBadNormalizer = errors.New("invalid normalization: want none or trimLower")
)

// SegmentOptions are the settings of one segment. A zero field takes its default.
type SegmentOptions struct {
	// Normalize is applied to the ids. Empty means NormalizeNone.
	Normalize Normalization
	// TTL is how long a Member is cached. Zero means DefaultTTL.
	TTL time.Duration
	// NegativeTTL is how long a NotMember is cached. Zero means DefaultNegativeTTL.
	NegativeTTL time.Duration
	// MaxEntries bounds the entries of the cache of the segment. Zero means DefaultMaxEntries.
	MaxEntries int
	// Shards is the number of locks of the cache. Zero means DefaultShards, 1 is one lock.
	Shards int
	// BreakerFailures is the number of consecutive failed calls that open the breaker. Zero means
	// DefaultBreakerFailures.
	BreakerFailures int
	// BreakerCooldown is how long the breaker stays open before it lets one probe through. Zero means
	// DefaultBreakerCooldown.
	BreakerCooldown time.Duration
	// NoCache turns the cache of the segment off.
	NoCache bool
	// NoBreaker turns the breaker of the segment off.
	NoBreaker bool
}

func (o SegmentOptions) validate(name string) error {
	if !segment.ValidName(name) {
		return fmt.Errorf("%w: %w: %q", ErrInvalidOptions, errBadName, name)
	}

	switch o.Normalize {
	case "", NormalizeNone, NormalizeTrimLower:
	default:
		return fmt.Errorf("%w: %q: %w, got %q", ErrInvalidOptions, name, errBadNormalizer, o.Normalize)
	}

	if o.TTL < 0 || o.NegativeTTL < 0 || o.MaxEntries < 0 || o.Shards < 0 || o.BreakerFailures < 0 ||
		o.BreakerCooldown < 0 {
		return fmt.Errorf("%w: %q: %w", ErrInvalidOptions, name, errNegative)
	}

	return nil
}

// withDefaults returns the options with every zero field set to its default.
func (o SegmentOptions) withDefaults() SegmentOptions {
	if o.Normalize == "" {
		o.Normalize = NormalizeNone
	}

	o.TTL = orDuration(o.TTL, DefaultTTL)
	o.NegativeTTL = orDuration(o.NegativeTTL, DefaultNegativeTTL)
	o.BreakerCooldown = orDuration(o.BreakerCooldown, DefaultBreakerCooldown)
	o.MaxEntries = orInt(o.MaxEntries, DefaultMaxEntries)
	o.Shards = orInt(o.Shards, DefaultShards)
	o.BreakerFailures = orInt(o.BreakerFailures, DefaultBreakerFailures)

	return o
}

func orDuration(v, fallback time.Duration) time.Duration {
	if v == 0 {
		return fallback
	}

	return v
}

func orInt(v, fallback int) int {
	if v == 0 {
		return fallback
	}

	return v
}

// Options configures a Resolver.
type Options struct {
	// Segments holds the settings of the named segments. A segment that is not here uses Default.
	Segments map[string]SegmentOptions
	// Observer (optional) receives the calls and the breaker transitions.
	Observer Observer
	// Default is the settings of the segments that Segments does not name.
	Default SegmentOptions
	// CallTimeout bounds the shared call to the store, which does not depend on the context of any one caller.
	// Zero means DefaultCallTimeout.
	CallTimeout time.Duration
	// MaxOverdue is the number of shared calls still running after their call timeout (a store that ignores its
	// context) from which no new call is started: the lookups that would need one are Unknown with ErrOverdue. It is
	// a threshold checked when a lookup would start a call, not a hard cap: calls that are started together, or just
	// before the count reaches it, can leave more overdue calls than MaxOverdue. Zero means DefaultMaxOverdue.
	MaxOverdue int
	// MaxIDBytes is the longest id, in bytes, that the resolver looks up. A longer id stays Unknown, is not cached
	// and is not sent to the store. Zero means DefaultMaxIDBytes.
	MaxIDBytes int
}

func (o *Options) validate() error {
	if o.CallTimeout < 0 || o.MaxIDBytes < 0 || o.MaxOverdue < 0 {
		return fmt.Errorf("%w: %w", ErrInvalidOptions, errNegative)
	}

	err := o.Default.validate("default")
	if err != nil {
		return err
	}

	for name, seg := range o.Segments {
		err = seg.validate(name)
		if err != nil {
			return err
		}
	}

	return nil
}
