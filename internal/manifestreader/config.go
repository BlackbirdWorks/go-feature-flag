package manifestreader

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math/rand/v2"
	"time"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// Defaults of [Config].
const (
	// DefaultPollInterval is the poll interval of a manifest.
	DefaultPollInterval = 60 * time.Second
	// DefaultJitterFraction is the jitter of the poll interval: +/-10 %.
	DefaultJitterFraction = 0.1
	// DefaultMaxManifestBytes bounds the manifest download: 20000 entries are about 10 MiB of YAML.
	DefaultMaxManifestBytes = 32 << 20
	// DefaultMaxObjectBytes bounds one object download.
	DefaultMaxObjectBytes = 16 << 20
	// DefaultFetchConcurrency is the number of concurrent object downloads.
	DefaultFetchConcurrency = 4
	// MaxFetchConcurrency is the largest accepted number of concurrent object downloads.
	MaxFetchConcurrency = 16
)

// ErrNothingRestored is what [Hooks.Restore] returns when there is no saved state: not a failure.
var ErrNothingRestored = errors.New("manifestreader: nothing to restore")

// ErrInvalidConfig is matched by the error [New] returns for a configuration it cannot use.
var ErrInvalidConfig = errors.New("manifestreader: invalid config")

// Config configures a [Reader]. A zero field takes its default, except where noted.
type Config struct {
	// ManifestKey is the key of the manifest in the store. Required.
	ManifestKey string
	// Channel is the expected channel of the manifest; a manifest of another channel is rejected. Empty
	// accepts any channel.
	Channel string
	// PollInterval is the time between two rounds, before jitter.
	PollInterval time.Duration
	// JitterFraction spreads the interval by +/- this fraction (0.1 is +/-10 %). Zero selects the default; a
	// negative value switches the jitter off. At most 1.
	JitterFraction float64
	// MaxObjects is the largest number of objects a manifest may list; a bigger manifest is rejected whole.
	MaxObjects int
	// MaxManifestBytes and MaxObjectBytes bound what one download may read; a larger body is rejected
	// without being buffered.
	MaxManifestBytes int64
	MaxObjectBytes   int64
	// FetchConcurrency is the number of concurrent object downloads, at most [MaxFetchConcurrency].
	FetchConcurrency int
}

func (c *Config) withDefaults() error {
	if c.ManifestKey == "" {
		return fmt.Errorf("%w: ManifestKey is required", ErrInvalidConfig)
	}

	if c.PollInterval < 0 || c.MaxObjects < 0 || c.MaxManifestBytes < 0 || c.MaxObjectBytes < 0 ||
		c.FetchConcurrency < 0 {
		return fmt.Errorf("%w: a limit is negative", ErrInvalidConfig)
	}

	if c.JitterFraction > 1 {
		return fmt.Errorf("%w: JitterFraction is above 1", ErrInvalidConfig)
	}

	if c.FetchConcurrency > MaxFetchConcurrency {
		return fmt.Errorf("%w: FetchConcurrency is above %d", ErrInvalidConfig, MaxFetchConcurrency)
	}

	c.PollInterval = cmp.Or(c.PollInterval, DefaultPollInterval)
	c.MaxObjects = cmp.Or(c.MaxObjects, controlplane.DefaultMaxObjects)
	c.MaxManifestBytes = cmp.Or(c.MaxManifestBytes, DefaultMaxManifestBytes)
	c.MaxObjectBytes = cmp.Or(c.MaxObjectBytes, DefaultMaxObjectBytes)
	c.FetchConcurrency = cmp.Or(c.FetchConcurrency, DefaultFetchConcurrency)

	switch {
	case c.JitterFraction == 0:
		c.JitterFraction = DefaultJitterFraction
	case c.JitterFraction < 0:
		c.JitterFraction = 0
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

// Persist is what a [Hooks.Save] call gets after a snapshot was published.
type Persist struct {
	Snapshot *Snapshot
	// ETag and Manifest are the held manifest.
	ETag     string
	Manifest []byte
}

// Hooks are the seams the state directory of a later change uses. Every field is optional. A hook runs on the
// polling goroutine and must not block.
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
// object of an unknown connection is failed with [ReasonUnknownConnection]; without it every object is read
// from the store of the manifest.
func WithObjectStores(stores map[string]ObjectStore) Option {
	return func(r *Reader) { r.stores = maps.Clone(stores) }
}

func defaultJitter() float64 {
	return rand.Float64() //nolint:gosec // spreads poll times, not security related
}
