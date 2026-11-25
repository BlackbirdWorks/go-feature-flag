package notification

import (
	"log/slog"
	"sync"

	"github.com/google/go-cmp/cmp"

	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/notifier"
)

var _ Service = (*ServiceImpl)(nil)

// Service dispatches flag change notifications to the notifiers.
type Service interface {
	Close()
	Notify(oldCache, newCache map[string]flag.Flag, log *slog.Logger)
}

// ServiceImpl is the default Service implementation.
type ServiceImpl struct {
	waitGroup *sync.WaitGroup
	Notifiers []notifier.Notifier
}

// NewService returns a notification service that sends flag changes to the notifiers.
func NewService(notifiers []notifier.Notifier) *ServiceImpl {
	return &ServiceImpl{
		Notifiers: notifiers,
		waitGroup: &sync.WaitGroup{},
	}
}

// Notify is sending the notification to the notifiers.
// oldCache is the old cache of the flags.
// newCache is the new cache of the flags.
// log is the logger to use.
func (c *ServiceImpl) Notify(oldCache, newCache map[string]flag.Flag, log *slog.Logger) {
	diff := c.getDifferences(oldCache, newCache)
	if diff.HasDiff() {
		for _, n := range c.Notifiers {
			c.waitGroup.Add(1)

			notif := n

			go func() {
				defer c.waitGroup.Done()

				err := notif.Notify(diff)
				if err != nil {
					log.Error("error while calling the notifier", slog.Any("error", err.Error()))
				}
			}()
		}
	}
}

// Close stops the notification service.
func (c *ServiceImpl) Close() {
	c.waitGroup.Wait()
}

// getDifferences is checking what are the difference in the updated cache.
func (*ServiceImpl) getDifferences(
	oldCache, newCache map[string]flag.Flag) notifier.DiffCache {
	diff := notifier.DiffCache{
		Deleted: map[string]flag.Flag{},
		Added:   map[string]flag.Flag{},
		Updated: map[string]notifier.DiffUpdated{},
	}

	for key := range oldCache {
		newFlag, inNewCache := newCache[key]

		oldFlag := oldCache[key]
		if !inNewCache {
			diff.Deleted[key] = oldFlag

			continue
		}

		if !cmp.Equal(oldCache[key], newCache[key]) {
			diff.Updated[key] = notifier.DiffUpdated{
				Before: oldFlag,
				After:  newFlag,
			}
		}
	}

	for key := range newCache {
		if _, inOldCache := oldCache[key]; !inOldCache {
			f := newCache[key]
			diff.Added[key] = f
		}
	}

	return diff
}
