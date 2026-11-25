package metric

import (
	"github.com/blackbirdworks/semaphore/notifier"
)

// NotifierPrometheus records flag changes as Prometheus metrics.
type NotifierPrometheus struct {
	metricsService Metrics
}

// NewPrometheusNotifier returns a notifier that records flag changes in metricsService.
func NewPrometheusNotifier(metricsService Metrics) *NotifierPrometheus {
	return &NotifierPrometheus{
		metricsService: metricsService,
	}
}

// Notify records the flag changes of diff in the metrics.
func (n *NotifierPrometheus) Notify(diff notifier.DiffCache) error {
	if !diff.HasDiff() {
		return nil
	}

	n.metricsService.IncFlagChange()

	for flagName := range diff.Deleted {
		n.metricsService.IncFlagDeleted(flagName)
	}

	for flagName := range diff.Added {
		n.metricsService.IncFlagCreated(flagName)
	}

	for flagName := range diff.Updated {
		n.metricsService.IncFlagUpdated(flagName)
	}

	return nil
}
