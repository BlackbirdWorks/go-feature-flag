package mock

import (
	"sync"

	"github.com/blackbirdworks/semaphore/notifier"
)

// Notifier is a mock notifier that counts its notifications.
type Notifier struct {
	NotifyCalls int
	mu          sync.Mutex
}

// Notify records a call to the notifier.
func (n *Notifier) Notify(_ notifier.DiffCache) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.NotifyCalls++

	return nil
}

// GetNotifyCalls returns the number of Notify calls.
func (n *Notifier) GetNotifyCalls() int {
	n.mu.Lock()
	defer n.mu.Unlock()

	return n.NotifyCalls
}
