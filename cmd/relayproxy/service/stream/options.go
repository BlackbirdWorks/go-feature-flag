package stream

import (
	"time"

	"github.com/blackbirdworks/semaphore/notifier"
)

// defaultSSEHeartbeatInterval is the default interval between two SSE heartbeat comments.
const defaultSSEHeartbeatInterval = 30 * time.Second

// defaultWriteTimeout bounds the write of one message to one websocket client: a client that does not take it in
// that time is closed and removed.
const defaultWriteTimeout = 10 * time.Second

type options struct {
	now                  func() time.Time
	sseHeartbeatInterval time.Duration
	writeTimeout         time.Duration
	includeFlagDetails   bool
}

// Option configures flag change streams.
type Option func(*options)

// WithFlagDetails controls whether stream payloads include flag definitions.
func WithFlagDetails(include bool) Option {
	return func(o *options) {
		o.includeFlagDetails = include
	}
}

// WithSSEHeartbeatInterval sets the interval between two SSE heartbeat comments,
// sent to keep idle connections open through proxies. A value <= 0 disables the heartbeat.
func WithSSEHeartbeatInterval(interval time.Duration) Option {
	return func(o *options) {
		o.sseHeartbeatInterval = interval
	}
}

// WithClock sets the clock that the write deadlines of an SSE stream are computed from. The default is the real
// time: a network deadline is enforced by the runtime, so a stream that runs against the network keeps the default.
func WithClock(now func() time.Time) Option {
	return func(o *options) {
		if now != nil {
			o.now = now
		}
	}
}

// WithWriteTimeout sets the time a websocket client has to take a message before it is closed and removed, and the
// write deadline of every write of an SSE stream. A value <= 0 keeps the default.
func WithWriteTimeout(timeout time.Duration) Option {
	return func(o *options) {
		if timeout > 0 {
			o.writeTimeout = timeout
		}
	}
}

func newOptions(opts ...Option) options {
	cfg := options{
		includeFlagDetails:   true,
		sseHeartbeatInterval: defaultSSEHeartbeatInterval,
		writeTimeout:         defaultWriteTimeout,
		now:                  time.Now,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return cfg
}

type flagChangeNames struct {
	Deleted map[string]struct{} `json:"deleted"`
	Added   map[string]struct{} `json:"added"`
	Updated map[string]struct{} `json:"updated"`
}

func flagChangePayload(diff notifier.DiffCache, includeFlagDetails bool) any {
	if includeFlagDetails {
		return diff
	}

	return flagChangeNames{
		Deleted: mapKeys(diff.Deleted),
		Added:   mapKeys(diff.Added),
		Updated: mapKeys(diff.Updated),
	}
}

func mapKeys[T any](values map[string]T) map[string]struct{} {
	keys := make(map[string]struct{}, len(values))
	for key := range values {
		keys[key] = struct{}{}
	}

	return keys
}
