package valkeysegment

import (
	"fmt"
	"log/slog"
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
	// loses the whole batch (all Unknown), and a cancel is seen only when the deadline of the call passes: a reply
	// that arrives after the cancel and before the deadline is returned as the answer (a correct one). Alone on a
	// connection it is not exposed to the unsolicited-reply panic; under concurrent load it is.
	PipeliningAuto Pipelining = "auto"
)

// The bounds of Options.ConnWriteTimeout.
const (
	// MinConnWriteTimeout is the least ConnWriteTimeout that is accepted.
	MinConnWriteTimeout = 10 * time.Millisecond
	// MaxConnWriteTimeout is the most ConnWriteTimeout that is accepted.
	MaxConnWriteTimeout = time.Minute
	// minDefaultConnWriteTimeout is the floor of the default: ten times the call timeout, but not under a second.
	minDefaultConnWriteTimeout = time.Second
	// connWriteTimeoutFactor is how many call timeouts the default ConnWriteTimeout is.
	connWriteTimeoutFactor = 10
)

// MaxClusterConcurrency is the most DoMulti calls that one lookup call has in flight on a cluster client with the
// atomic check: each set is a transaction in a DoMulti of its own.
const MaxClusterConcurrency = 16

// DefaultConnWriteTimeout is the ConnWriteTimeout that [Dial] sets when the option is zero: ten call timeouts (zero
// call timeout means DefaultTimeout), at least one second and at most MaxConnWriteTimeout.
func DefaultConnWriteTimeout(callTimeout time.Duration) time.Duration {
	if callTimeout <= 0 {
		callTimeout = DefaultTimeout
	}

	return min(max(callTimeout*connWriteTimeoutFactor, minDefaultConnWriteTimeout), MaxConnWriteTimeout)
}

// Options configures a Provider. Pipelining, BlockingPipeline, Retry and ConnWriteTimeout configure the client that
// [Dial] creates; [New] ignores them and does not check them.
type Options struct {
	// Segments maps a segment name to where its members are. A lookup of a segment that is not here stays Unknown:
	// a flag can never choose a key.
	Segments map[string]SegmentOptions
	// Observer (optional) receives one Observation per call, except for a call with no lookup and a call after
	// Shutdown.
	Observer Observer
	// Atomic sets the atomic existence check. Unset (nil) means on, except for a client that [Dial] creates with
	// replica reads (ClientOption.SendToReplicas or ReplicaOnly): then off, with one INFO line to Logger. On, with
	// MissingUnknown, a set segment sends MULTI, EXISTS, SMISMEMBER and EXEC in one batch, so a set that is removed
	// or expires during the call is missing for both commands. Off, it sends EXISTS and SMISMEMBER outside a
	// transaction: fewer commands, and a set that disappears between them can answer not-a-member for a former
	// member. It has no effect with MissingNotMember (no existence check is sent). A transaction goes to the primary,
	// never to a replica: an explicit true with replica reads moves the set lookups there. [New] cannot see the
	// replica reads of its client: it defaults to on.
	Atomic *bool
	// Logger (optional) receives the INFO line of the replica default of Atomic. Nil discards.
	Logger *slog.Logger
	// KeyPrefix is the default prefix of the keys. Empty means DefaultKeyPrefix.
	KeyPrefix string
	// MissingKey is what a missing set key means. Empty means MissingNotMember.
	MissingKey MissingKey
	// Pipelining is PipeliningAlways (default) or PipeliningAuto: see their documentation.
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
	// ConnWriteTimeout is the read and write timeout of each connection of the client (valkey-go
	// ClientOption.ConnWriteTimeout). It also ends the wait of a call behind a peer that stopped reading until the
	// ring of the connection is full, which the call timeout does not. Zero keeps the ConnWriteTimeout of the client
	// option, and when that is zero too uses DefaultConnWriteTimeout(Timeout). Otherwise it is from
	// MinConnWriteTimeout to MaxConnWriteTimeout.
	ConnWriteTimeout time.Duration
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

// validateClient checks the fields that only Dial uses.
func (o *Options) validateClient() error {
	if o.BlockingPipeline < 0 {
		return fmt.Errorf("%w: %w", ErrInvalidOptions, errNegativeBlocking)
	}

	if o.ConnWriteTimeout != 0 &&
		(o.ConnWriteTimeout < MinConnWriteTimeout || o.ConnWriteTimeout > MaxConnWriteTimeout) {
		return fmt.Errorf("%w: %w", ErrInvalidOptions, errConnWriteTimeout)
	}

	switch o.Pipelining {
	case "", PipeliningAlways, PipeliningAuto:
		return nil
	default:
		return fmt.Errorf("%w: %w, got %q", ErrInvalidOptions, errInvalidPipelining, o.Pipelining)
	}
}
