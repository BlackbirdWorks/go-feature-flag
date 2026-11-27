package valkeysegment

import (
	"fmt"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
)

// The defaults and limits are the ones of package redissegment.
const (
	// DefaultTimeout is the per-call timeout when Options.Timeout is zero.
	DefaultTimeout = redissegment.DefaultTimeout
	// DefaultKeyPrefix is the prefix of the keys of the segments when Options.KeyPrefix is empty.
	DefaultKeyPrefix = redissegment.DefaultKeyPrefix
	// DefaultMaxIDBytes is the longest id that is looked up when Options.MaxIDBytes is zero.
	DefaultMaxIDBytes = redissegment.DefaultMaxIDBytes
	// MaxIDsPerCommand is the most ids that one SMISMEMBER carries: a segment with more ids in a call is several
	// commands in the same DoMulti.
	MaxIDsPerCommand = redissegment.MaxIDsPerCommand
	// MaxInitProbes is the most namespaces that Init probes.
	MaxInitProbes = redissegment.MaxInitProbes
)

// Pipelining is how the valkey-go client sends the commands of a call. It only applies to a client that [Dial]
// creates. The names are the ones of the relay proxy configuration.
type Pipelining string

const (
	// PipeliningAlways (default) pipelines in the background even for a call that is alone on a connection. A
	// connection that fails inside a batch keeps the replies already read (the others are Unknown), and a cancel of
	// the caller ends the call at once. Exposed to the unsolicited-reply panic of valkey-go (see the package doc).
	PipeliningAlways Pipelining = "always"
	// PipeliningAuto is the default of valkey-go: a call that is alone on a connection is written and read
	// synchronously, and the background reader starts under concurrent load. A transport failure inside a batch
	// loses the whole batch (all Unknown), and a cancel is seen only when the deadline of the call passes. Alone
	// on a connection it is not exposed to the unsolicited-reply panic; under concurrent load it is.
	PipeliningAuto Pipelining = "auto"
	// PipeliningOff (valkey-go DisableAutoPipelining) takes a dedicated connection from the pool for each call:
	// no batching of concurrent calls, the same loss of a whole batch and the same deadline-only cancel as auto.
	// It is not exposed to the unsolicited-reply panic, at the cost of one connection for each call in flight.
	PipeliningOff Pipelining = "off"
)

// Options configures a Provider. There is no Atomic mode: the provider sends no script. The last four fields
// configure the client that [Dial] creates and are ignored by [New].
type Options struct {
	// Segments maps a segment name to where its members are. A lookup of a segment that is not here stays Unknown:
	// a flag can never choose a key.
	Segments map[string]SegmentOptions
	// Observer (optional) receives one Observation per call, except for a call with no lookup and a call after
	// Shutdown.
	Observer Observer
	// KeyPrefix is the default prefix of the keys. Empty means DefaultKeyPrefix.
	KeyPrefix string
	// MissingKey is what a missing set key means. Empty means MissingUnknown.
	MissingKey MissingKey
	// Pipelining is PipeliningAlways (default), PipeliningAuto or PipeliningOff: see their documentation.
	Pipelining Pipelining
	// MaxIDBytes is the longest id, in bytes, that the provider looks up. A longer id stays Unknown and is not sent.
	// Zero means DefaultMaxIDBytes.
	MaxIDBytes int
	// Timeout bounds one call, under the deadline of the caller. Zero means DefaultTimeout.
	Timeout time.Duration
	// BlockingPipeline is the number of commands of one call from which valkey-go moves the call to a dedicated
	// connection, where a cancel waits for the connection to close. Zero (default) means never: a call of any size
	// stays on the shared connection. A positive value is valkey-go's own threshold.
	BlockingPipeline int
	// ClientSideCache turns on the client-side caching of valkey-go (server-assisted tracking). Default false. The
	// provider reads with DoMulti, which valkey-go never caches, so it has no effect on the lookups; the resolver
	// is the cache of the segments. Needs RESP3 and a server that supports CLIENT TRACKING.
	ClientSideCache bool
	// Retry lets valkey-go send a read again after a network error. Default false: a retry runs behind the call
	// timeout of the provider and sends the lookups twice.
	Retry bool
}

func (o *Options) validate() error {
	if o.Timeout < 0 {
		return fmt.Errorf("%w: %w", ErrInvalidOptions, errNegativeTimeout)
	}

	if o.MaxIDBytes < 0 {
		return fmt.Errorf("%w: %w", ErrInvalidOptions, errNegativeMaxID)
	}

	if o.BlockingPipeline < 0 {
		return fmt.Errorf("%w: %w", ErrInvalidOptions, errNegativeBlocking)
	}

	switch o.Pipelining {
	case "", PipeliningAlways, PipeliningAuto, PipeliningOff:
	default:
		return fmt.Errorf("%w: %w, got %q", ErrInvalidOptions, errInvalidPipelining, o.Pipelining)
	}

	switch o.MissingKey {
	case "", MissingUnknown, MissingNotMember:
	default:
		return fmt.Errorf("%w: %w, got %q", ErrInvalidOptions, errInvalidMissing, o.MissingKey)
	}

	for name, seg := range o.Segments {
		if !segment.ValidName(name) {
			return fmt.Errorf("%w: %w: %q", ErrInvalidOptions, errInvalidName, name)
		}

		switch seg.Layout {
		case "", LayoutSet, LayoutKeys:
		default:
			return fmt.Errorf("%w: %q: %w, got %q", ErrInvalidOptions, name, errInvalidLayout, seg.Layout)
		}
	}

	return nil
}
