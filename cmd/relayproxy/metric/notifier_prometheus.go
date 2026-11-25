package metric

import (
	"github.com/blackbirdworks/semaphore/notifier"
)

type NotifierPrometheus struct {
	metricsService Metrics
}

func NewPrometheusNotifier(metricsService Metrics) *NotifierPrometheus {
	return &NotifierPrometheus{
		metricsService: metricsService,
	}
}

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
