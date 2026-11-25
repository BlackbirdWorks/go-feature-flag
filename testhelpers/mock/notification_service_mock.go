package mock

import (
	"log/slog"
	"sync"

	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

// NotificationService is a mock notification service that counts its calls.
type NotificationService struct {
	NotifyCalls int
	CloseCalled bool
	mu          sync.Mutex
}

// Notify records a call to the notification service.
func (n *NotificationService) Notify(_, _ map[string]flag.Flag, _ *slog.Logger) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.NotifyCalls++
}

// Close records that the notification service was closed.
func (n *NotificationService) Close() {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.CloseCalled = true
}

// GetNotifyCalls returns the number of Notify calls.
func (n *NotificationService) GetNotifyCalls() int {
	n.mu.Lock()
	defer n.mu.Unlock()

	return n.NotifyCalls
}

// WasCloseCalled reports whether Close was called.
func (n *NotificationService) WasCloseCalled() bool {
	return n.CloseCalled
}
