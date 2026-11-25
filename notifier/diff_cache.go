package notifier

import (
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// DiffCache contains the changes made in the cache, to be able
// to notify the user that something has changed (logs, webhook ...)
type DiffCache struct {
	Deleted map[string]flagdef.Flag `json:"deleted"`
	Added   map[string]flagdef.Flag `json:"added"`
	Updated map[string]DiffUpdated  `json:"updated"`
}

// HasDiff check if we have differences.
func (d *DiffCache) HasDiff() bool {
	return len(d.Deleted) > 0 || len(d.Added) > 0 || len(d.Updated) > 0
}

// DiffUpdated holds the state of one flag before and after it was updated.
type DiffUpdated struct {
	Before flagdef.Flag `json:"old_value"` //nolint:tagliatelle // webhook notifier and flag-change websocket payload
	After  flagdef.Flag `json:"new_value"` //nolint:tagliatelle // webhook notifier and flag-change websocket payload
}
