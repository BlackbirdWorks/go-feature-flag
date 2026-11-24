package stream

import (
	"time"

	"github.com/blackbirdworks/semaphore/notifier"
)

// defaultSSEHeartbeatInterval is the default interval between two SSE heartbeat comments.
const defaultSSEHeartbeatInterval = 30 * time.Second

type options struct {
	includeFlagDetails   bool
	sseHeartbeatInterval time.Duration
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

func newOptions(opts ...Option) options {
	o := options{includeFlagDetails: true, sseHeartbeatInterval: defaultSSEHeartbeatInterval}
	for _, opt := range opts {
		opt(&o)
	}

	return o
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
