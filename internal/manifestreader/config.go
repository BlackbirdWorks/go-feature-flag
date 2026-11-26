package manifestreader

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"math/rand/v2"
	"time"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// Defaults and bounds of [Config].
const (
	// DefaultPollInterval is the poll interval of a manifest.
	DefaultPollInterval = 60 * time.Second
	// MinPollInterval is the shortest accepted poll interval, and the shortest wait between two rounds.
	MinPollInterval = time.Second
	// DefaultJitterFraction is the jitter of the poll interval: +/-10 %.
	DefaultJitterFraction = 0.1
	// DefaultRequestTimeout bounds every request of the reader (the manifest and each object).
	DefaultRequestTimeout = 60 * time.Second
	// DefaultMaxManifestBytes bounds the manifest download. 20000 entries are about 10 MiB of JSON or YAML.
	DefaultMaxManifestBytes = 16 << 20
	// DefaultMaxObjectBytes bounds one object download.
	DefaultMaxObjectBytes = 16 << 20
	// DefaultMaxTotalBytes bounds the sum of the sizes of the distinct objects of a manifest.
	DefaultMaxTotalBytes = 1 << 30
	// DefaultFetchConcurrency is the number of concurrent object downloads.
	DefaultFetchConcurrency = 4
	// MaxFetchConcurrency is the largest accepted number of concurrent object downloads.
	MaxFetchConcurrency = 16
	// maxStartPhase bounds the random delay before the first round.
	maxStartPhase = 5 * time.Second
)

// ErrNothingRestored is what [Hooks.Restore] returns when there is no saved state: not a failure.
var ErrNothingRestored = errors.New("manifestreader: nothing to restore")

// ErrInvalidConfig is matched by the error [New] returns for a configuration it cannot use.
var ErrInvalidConfig = errors.New("manifestreader: invalid config")

// Config configures a [Reader]. A zero field takes its default, except where noted. [New] copies it.
type Config struct {
	// ManifestKey is the absolute bucket key of the manifest, as the publisher wrote it (a key prefix is applied
	// by the publisher only; the reader never adds one). Required; it must not start with "/".
	ManifestKey string
	// Connection is the name of the connection the store of the reader serves. Required: an object whose entry
	// names another connection is failed with [ReasonUnknownConnection] unless [WithObjectStores] has a store
	// for that name.
	Connection string
	// Channel is the expected channel of the manifest; a manifest of another channel is rejected. Empty
	// accepts any channel.
	Channel string
	// PollInterval is the time between two rounds, before jitter; at least [MinPollInterval].
	PollInterval time.Duration
	// JitterFraction spreads the interval by +/- this fraction (0.1 is +/-10 %). Zero selects the default; it
	// must be in [0, 1). Set DisableJitter to poll at exactly the interval.
	JitterFraction float64
	// RequestTimeout bounds every request of the reader: the manifest GET and each object GET.
	RequestTimeout time.Duration
	// MaxObjects is the largest number of objects a manifest may list; a bigger manifest is rejected whole.
	MaxObjects int
	// MaxManifestBytes bounds the manifest download. Decoding a 20000 entry manifest costs about 190 ms and
	// 144 MB of allocations (measured with the controlplane decoder), and a hostile manifest near the cap can
	// allocate far more before it is rejected: the cap is what bounds that cost. A larger body is refused
	// without being buffered.
	MaxManifestBytes int64
	// MaxObjectBytes bounds one object: an entry that lists a larger size is not requested.
	MaxObjectBytes int64
	// MaxTotalBytes bounds the sum of the listed sizes of the distinct checksums of a manifest. A manifest
	// above it is rejected whole before any object request; a checksum listed with several sizes counts with the
	// largest. The worst-case memory of the reader is about this cap for the current content, plus the last good
	// content that stale names still carry, plus FetchConcurrency times MaxObjectBytes in flight. Each carried
	// object was within the limits when it was loaded, but the carried bytes of several older generations are not
	// bounded by this cap: they are bounded by MaxObjects times MaxObjectBytes.
	MaxTotalBytes int64
	// FetchConcurrency is the number of concurrent object downloads, at most [MaxFetchConcurrency].
	FetchConcurrency int
	// DisableJitter turns the jitter off (and with it the random start phase).
	DisableJitter bool
}

func (c *Config) withDefaults() error {
	err := c.validate()
	if err != nil {
		return err
	}

	c.PollInterval = cmp.Or(c.PollInterval, DefaultPollInterval)
	c.RequestTimeout = cmp.Or(c.RequestTimeout, DefaultRequestTimeout)
	c.MaxObjects = cmp.Or(c.MaxObjects, controlplane.DefaultMaxObjects)
	c.MaxManifestBytes = cmp.Or(c.MaxManifestBytes, DefaultMaxManifestBytes)
	c.MaxObjectBytes = cmp.Or(c.MaxObjectBytes, DefaultMaxObjectBytes)
	c.MaxTotalBytes = cmp.Or(c.MaxTotalBytes, DefaultMaxTotalBytes)
	c.FetchConcurrency = cmp.Or(c.FetchConcurrency, DefaultFetchConcurrency)
	c.JitterFraction = cmp.Or(c.JitterFraction, DefaultJitterFraction)

	if c.DisableJitter {
		c.JitterFraction = 0
	}

	return nil
}

// validKey reports whether key can be an absolute bucket key: not empty and not starting with a slash.
func validKey(key string) bool {
	return key != "" && key[0] != '/'
}

func (c *Config) validateLimits() error {
	if c.PollInterval < 0 || c.RequestTimeout < 0 || c.MaxObjects < 0 || c.MaxManifestBytes < 0 ||
		c.MaxObjectBytes < 0 || c.MaxTotalBytes < 0 || c.FetchConcurrency < 0 {
		return fmt.Errorf("%w: a limit is negative", ErrInvalidConfig)
	}

	return nil
}

func (c *Config) validate() error {
	if c.ManifestKey == "" {
		return fmt.Errorf("%w: ManifestKey is required", ErrInvalidConfig)
	}

	if !validKey(c.ManifestKey) {
		return fmt.Errorf("%w: ManifestKey is not an absolute bucket key", ErrInvalidConfig)
	}

	if c.Connection == "" {
		return fmt.Errorf("%w: Connection is required", ErrInvalidConfig)
	}

	err := c.validateLimits()
	if err != nil {
		return err
	}

	if c.PollInterval != 0 && c.PollInterval < MinPollInterval {
		return fmt.Errorf("%w: PollInterval is below %s", ErrInvalidConfig, MinPollInterval)
	}

	if c.JitterFraction < 0 || c.JitterFraction >= 1 || math.IsNaN(c.JitterFraction) {
		return fmt.Errorf("%w: JitterFraction is outside [0, 1)", ErrInvalidConfig)
	}

	if c.FetchConcurrency > MaxFetchConcurrency {
		return fmt.Errorf("%w: FetchConcurrency is above %d", ErrInvalidConfig, MaxFetchConcurrency)
	}

	return nil
}

// Restored is persisted state handed back to the reader at start (see [Hooks.Restore]).
type Restored struct {
	// ETag and Manifest are the last accepted manifest as it was saved.
	ETag     string
	Manifest []byte
	// Objects are the saved verified objects. The reader hashes them: a corrupted one is simply not found by
	// its checksum and fetched again.
	Objects [][]byte
}

// Persist is what a [Hooks.Save] call gets after a snapshot was published. Every field is read-only and
// shared with the reader: a hook must copy what it keeps.
type Persist struct {
	Snapshot *Snapshot
	// ETag and Manifest are the ACCEPTED manifest (the one the snapshot comes from), never a rejected one that
	// was read later.
	ETag     string
	Manifest []byte
}

// Hooks are the seams the state directory of a later change uses. Every field is optional. A hook runs on the
// polling goroutine and must not block. A panic in a hook is recovered, logged once per hook, counted in
// [Status.HookPanics], and polling goes on. The arguments of Save and OnRound are read-only: do not change them
// and do not keep them without copying.
type Hooks struct {
	// Restore is called once when [Reader.Run] starts. It returns [ErrNothingRestored] when nothing was saved.
	Restore func() (*Restored, error)
	// Save is called after every published snapshot.
	Save func(Persist)
	// OnRound is called at the end of every round with the new status.
	OnRound func(*Status)
}

// Option configures a [Reader].
type Option func(*Reader)

// WithLogger sets the logger; the default discards. The reader logs names and closed codes only, never keys,
// bytes or the text of an error of the store.
func WithLogger(logger *slog.Logger) Option {
	return func(r *Reader) {
		if logger != nil {
			r.log = logger
		}
	}
}

// WithScheduler replaces the timer of the polling loop (tests use it to drive the rounds).
func WithScheduler(s Scheduler) Option {
	return func(r *Reader) {
		if s != nil {
			r.sched = s
		}
	}
}

// WithJitter replaces the random source of the jitter: it returns a value in [0, 1). The default is
// math/rand/v2.
func WithJitter(source func() float64) Option {
	return func(r *Reader) {
		if source != nil {
			r.jitter = source
		}
	}
}

// WithHooks sets the hooks.
func WithHooks(h Hooks) Option {
	return func(r *Reader) { r.hooks = h }
}

// WithObjectStores gives the store of each connection name that objects of the manifest may name. With it an
// object of an unknown connection is failed with [ReasonUnknownConnection]. The store of the manifest always
// serves the connection named by [Config.Connection], and only that one.
func WithObjectStores(stores map[string]ObjectStore) Option {
	return func(r *Reader) { r.stores = maps.Clone(stores) }
}

// WithRequestContext replaces the creation of the context of each request (manifest and objects), which is
// [context.WithTimeout] by default. Tests use it to end a request without waiting for a real timer.
func WithRequestContext(
	create func(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc),
) Option {
	return func(r *Reader) {
		if create != nil {
			r.reqCtx = create
		}
	}
}

func defaultJitter() float64 {
	return rand.Float64() //nolint:gosec // spreads poll times, not security related
}
