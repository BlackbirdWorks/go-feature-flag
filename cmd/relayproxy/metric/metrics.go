package metric

import (
	"sort"
	"strings"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	versioncollector "github.com/prometheus/client_golang/prometheus/collectors/version"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/protobuf/proto"
)

const (
	// metricsPerFamily is the expected number of metric families produced for a family (the legacy aliases).
	metricsPerFamily = 2
)

const (
	flagNameLabel = "flag_name"
	outcomeLabel  = "outcome"
)

// Outcomes of a call to the validate flags endpoint.
const (
	// ValidateFlagsOutcomeValid is used when all the flags of the file are valid.
	ValidateFlagsOutcomeValid = "valid"
	// ValidateFlagsOutcomeInvalid is used when at least one flag of the file is invalid.
	ValidateFlagsOutcomeInvalid = "invalid"
	// ValidateFlagsOutcomeError is used when the file could not be validated (bad content type, too large, unparseable).
	ValidateFlagsOutcomeError = "error"
)

// FlagNotFoundLabelValue is the value of the flag_name label used for the evaluation of flags that do not exist.
const FlagNotFoundLabelValue = "FLAG_NOT_FOUND"

type MetricsOpts struct {
	// Version is the version that semaphore_build_info reports (the one of the binary, set at build
	// time, when empty).
	Version string
	// enables per-flag metrics for bulk evaluation endpoints
	EnableBulkMetricFlagNames bool
}

const (
	// SubSystem is the name of the prefix we are using for all the metrics.
	SubSystem = "semaphore"
	// LegacySubSystem is the prefix GO Feature Flag used for the same metrics. The series are only
	// exposed under it when the configuration asks for it (exposeLegacyMetrics), see Metrics.Gatherer.
	LegacySubSystem = "gofeatureflag"
)

// Metrics is a struct containing all custom prometheus metrics.
type Metrics struct {
	flagConfigurationCounter prom.Counter
	flagUpdateCounter        prom.Counter
	getManifestCounter       prom.Counter
	allFlagCounter           prom.Counter
	flagDeleteCounter        prom.Counter
	collectEvalDataCounter   prom.Counter
	flagChange               prom.Counter
	flagCreateCounter        prom.Counter
	forceRefreshCounter      prom.Counter
	Registry                 *prom.Registry
	allFlagCounterWithFlag   prom.CounterVec
	validateFlagsCounterVec  prom.CounterVec
	flagUpdateCounterVec     prom.CounterVec
	flagDeleteCounterVec     prom.CounterVec
	flagCreateCounterVec     prom.CounterVec
	flagEvaluationCounter    prom.CounterVec
	opts                     MetricsOpts
}

// NewMetrics is the constructor for the custom metrics
//
//nolint:funlen
func NewMetrics(opts ...MetricsOpts) (Metrics, error) {
	// default opts
	mo := &MetricsOpts{
		EnableBulkMetricFlagNames: false,
	}

	if len(opts) > 0 {
		mo = &opts[0]
	}
	customRegistry := prom.NewRegistry()

	// counts the number of flag evaluations
	flagEvaluationCounter := prom.NewCounterVec(prom.CounterOpts{
		Name:      "flag_evaluations_total",
		Help:      "Counter events for number of flag evaluation.",
		Subsystem: SubSystem,
	}, []string{flagNameLabel})

	// counts the number of call to the all flag endpoint
	allFlagCounter := prom.NewCounter(prom.CounterOpts{
		Name:      "all_flag_evaluations_total",
		Help:      "Counter events for number of all flags requests.",
		Subsystem: SubSystem,
	})

	// counts the number of bulk evaluations along with flag
	allFlagCounterWithFlag := prom.NewCounterVec(prom.CounterOpts{
		Name:      "all_flags_evaluations_with_flag_total",
		Help:      "Counter events for all flags bulk evaluations with individual flag names",
		Subsystem: SubSystem,
	}, []string{flagNameLabel})

	// counts the number of tracking events collected through the API
	collectEvalDataCounter := prom.NewCounter(prom.CounterOpts{
		Name:      "collect_eval_data_total",
		Help:      "Counter events for data collector.",
		Subsystem: SubSystem,
	})

	// counts the number of flag changes (create, update, delete) from your configuration
	flagChange := prom.NewCounter(prom.CounterOpts{
		Name:      "flag_changes_total",
		Help:      "Counter that counts the number of flag changes.",
		Subsystem: SubSystem,
	})

	// counts the number of new flag created from your configuration
	flagCreateCounter := prom.NewCounter(prom.CounterOpts{
		Name:      "flag_create_total",
		Help:      "Counter that counts the number of flag created.",
		Subsystem: SubSystem,
	})

	// counts the number of flag deleted from your configuration
	flagDeleteCounter := prom.NewCounter(prom.CounterOpts{
		Name:      "flag_delete_total",
		Help:      "Counter that counts the number of flag deleted.",
		Subsystem: SubSystem,
	})

	// counts the number of flag updated from your configuration
	flagUpdateCounter := prom.NewCounter(prom.CounterOpts{
		Name:      "flag_update_total",
		Help:      "Counter that counts the number of flag updated.",
		Subsystem: SubSystem,
	})

	// counts the number of update per flag
	flagUpdateCounterVec := prom.NewCounterVec(prom.CounterOpts{
		Name:      "flag_update_by_flag_total",
		Help:      "Counter events for number of update per flag.",
		Subsystem: SubSystem,
	}, []string{flagNameLabel})

	// counts the number of delete per flag
	flagDeleteCounterVec := prom.NewCounterVec(prom.CounterOpts{
		Name:      "flag_delete_by_flag_total",
		Help:      "Counter events for number of delete per flag.",
		Subsystem: SubSystem,
	}, []string{flagNameLabel})

	// flagCreateCounterVec counts the number of create per flag
	flagCreateCounterVec := prom.NewCounterVec(prom.CounterOpts{
		Name:      "flag_create_by_flag_total",
		Help:      "Counter events for number of create per flag.",
		Subsystem: SubSystem,
	}, []string{flagNameLabel})

	// counts the number of flag updated from your configuration
	forceRefreshCounter := prom.NewCounter(prom.CounterOpts{
		Name:      "force_refresh_total",
		Help:      "Counter that counts the number of force refresh.",
		Subsystem: SubSystem,
	})

	// counts the number of call to the validate flags endpoint per outcome
	validateFlagsCounterVec := prom.NewCounterVec(prom.CounterOpts{
		Name:      "validate_flags_total",
		Help:      "Counter events for number of calls to the validate flags endpoint, per outcome.",
		Subsystem: SubSystem,
	}, []string{outcomeLabel})

	// counts the number of call to the flag configuration endpoint
	flagConfigurationCounter := prom.NewCounter(prom.CounterOpts{
		Name:      "flag_configuration_total",
		Help:      "Counter events for number of configuration api requests.",
		Subsystem: SubSystem,
	})

	// counts the number of calls to the manifest endpoint
	getManifestCounter := prom.NewCounter(prom.CounterOpts{
		Name:      "get_manifest_total",
		Help:      "Counter events for number of getManifest api requests.",
		Subsystem: SubSystem,
	})

	metricToRegister := []prom.Collector{
		flagEvaluationCounter,
		allFlagCounter,
		allFlagCounterWithFlag,
		collectEvalDataCounter,
		flagChange,
		flagCreateCounter,
		flagDeleteCounter,
		flagUpdateCounter,
		flagUpdateCounterVec,
		flagDeleteCounterVec,
		flagCreateCounterVec,
		forceRefreshCounter,
		validateFlagsCounterVec,
		flagConfigurationCounter,
		getManifestCounter,
		versioncollector.NewCollector(SubSystem, versionOptions(mo.Version)...),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewGoCollector(),
	}

	// register all the metric in the custom registry
	for _, metric := range metricToRegister {
		if err := customRegistry.Register(metric); err != nil {
			return Metrics{}, err
		}
	}

	return Metrics{
		opts:                     *mo,
		flagEvaluationCounter:    *flagEvaluationCounter,
		allFlagCounter:           allFlagCounter,
		allFlagCounterWithFlag:   *allFlagCounterWithFlag,
		collectEvalDataCounter:   collectEvalDataCounter,
		flagChange:               flagChange,
		flagCreateCounter:        flagCreateCounter,
		flagDeleteCounter:        flagDeleteCounter,
		flagUpdateCounter:        flagUpdateCounter,
		flagUpdateCounterVec:     *flagUpdateCounterVec,
		flagDeleteCounterVec:     *flagDeleteCounterVec,
		flagCreateCounterVec:     *flagCreateCounterVec,
		forceRefreshCounter:      forceRefreshCounter,
		validateFlagsCounterVec:  *validateFlagsCounterVec,
		flagConfigurationCounter: flagConfigurationCounter,
		getManifestCounter:       getManifestCounter,
		Registry:                 customRegistry,
	}, nil
}

func (m *Metrics) IncFlagEvaluation(flagName string) {
	if m.flagEvaluationCounter.MetricVec != nil {
		labels := prom.Labels{flagNameLabel: flagName}
		m.flagEvaluationCounter.With(labels).Inc()
	}
}

// IncFlagNotFoundEvaluation increment the number of evaluation of flags that do not exist.
// We are not using the flag key as label, because every unknown key sent by a client would create
// a new time series that is never deleted.
func (m *Metrics) IncFlagNotFoundEvaluation() {
	m.IncFlagEvaluation(FlagNotFoundLabelValue)
}

// IncAllFlag increment the number call to AllFlag.
func (m *Metrics) IncAllFlag(flagNames ...string) {
	if m.allFlagCounter != nil {
		m.allFlagCounter.Inc()
	}

	if m.ShouldCollectBulkMetrics() {
		for _, flagName := range flagNames {
			m.allFlagCounterWithFlag.With(prom.Labels{flagNameLabel: flagName}).Inc()
		}
	}
}

// IncForceRefresh increment the number call to ForceRefresh.
func (m *Metrics) IncForceRefresh() {
	if m.forceRefreshCounter != nil {
		m.forceRefreshCounter.Inc()
	}
}

// IncValidateFlags increment the number of call to the validate flags endpoint for the given outcome
// (ValidateFlagsOutcomeValid, ValidateFlagsOutcomeInvalid or ValidateFlagsOutcomeError).
func (m *Metrics) IncValidateFlags(outcome string) {
	if m.validateFlagsCounterVec.MetricVec != nil {
		m.validateFlagsCounterVec.With(prom.Labels{outcomeLabel: outcome}).Inc()
	}
}

// IncCollectEvalData is collecting the number of events collected through the API.
func (m *Metrics) IncCollectEvalData(numberEvents float64) {
	if m.collectEvalDataCounter != nil {
		m.collectEvalDataCounter.Add(numberEvents)
	}
}

// IncFlagUpdated is incrementing the counters when a flag is updated.
func (m *Metrics) IncFlagUpdated(flagName string) {
	if m.flagUpdateCounterVec.MetricVec != nil {
		m.flagUpdateCounterVec.With(prom.Labels{flagNameLabel: flagName}).Inc()
		m.flagUpdateCounter.Inc()
	}
}

// IncFlagDeleted is incrementing the counters when a flag is deleted.
func (m *Metrics) IncFlagDeleted(flagName string) {
	if m.flagDeleteCounterVec.MetricVec != nil {
		m.flagDeleteCounterVec.With(prom.Labels{flagNameLabel: flagName}).Inc()
		m.flagDeleteCounter.Inc()
	}
}

// IncFlagCreated is incrementing the counters when a flag is created.
func (m *Metrics) IncFlagCreated(flagName string) {
	if m.flagCreateCounterVec.MetricVec != nil {
		m.flagCreateCounterVec.With(prom.Labels{flagNameLabel: flagName}).Inc()
		m.flagCreateCounter.Inc()
	}
}

// IncFlagChange is incrementing the counters when a flag is created, updated or deleted.
func (m *Metrics) IncFlagChange() {
	if m.flagChange != nil {
		m.flagChange.Inc()
	}
}

// IncFlagConfigurationCall is incrementing the counters when the flag configuration endpoint is called.
func (m *Metrics) IncFlagConfigurationCall() {
	if m.flagConfigurationCounter != nil {
		m.flagConfigurationCounter.Inc()
	}
}

// IncGetManifestCall is incrementing the counters when the manifest endpoint is called.
func (m *Metrics) IncGetManifestCall() {
	if m.getManifestCounter != nil {
		m.getManifestCounter.Inc()
	}
}

func (m *Metrics) ShouldCollectBulkMetrics() bool {
	return m.opts.EnableBulkMetricFlagNames && m.allFlagCounterWithFlag.MetricVec != nil
}

// Gatherer returns what the /metrics endpoint serves. With exposeLegacy it also serves every series of
// the SubSystem under its LegacySubSystem name (semaphore_flag_evaluations_total is also exposed as
// gofeatureflag_flag_evaluations_total), so the dashboards and alerts of GO Feature Flag keep working.
func (m *Metrics) Gatherer(exposeLegacy bool) prom.Gatherer {
	if !exposeLegacy {
		return m.Registry
	}

	return legacyGatherer{source: m.Registry}
}

// legacyGatherer gathers the source once and serves its families with, next to each family of the SubSystem,
// a copy named with the LegacySubSystem prefix. Both names so carry the values of the same collection.
type legacyGatherer struct {
	source prom.Gatherer
}

func (g legacyGatherer) Gather() ([]*dto.MetricFamily, error) {
	families, err := g.source.Gather()
	prefix := SubSystem + "_"
	out := make([]*dto.MetricFamily, 0, len(families)*metricsPerFamily)
	out = append(out, families...)
	for _, family := range families {
		name := family.GetName()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		clone, ok := proto.Clone(family).(*dto.MetricFamily)
		if !ok {
			continue
		}
		clone.Name = new(LegacySubSystem + "_" + legacyMetricName(strings.TrimPrefix(name, prefix)))
		out = append(out, clone)
	}
	// the exposition format wants the families sorted by name, as prom.Gatherers does.
	sort.Slice(out, func(i, j int) bool { return out[i].GetName() < out[j].GetName() })

	return out, err
}

// legacyMetricName is the name GO Feature Flag gave to the metric that Semaphore names name (without the
// prefix). The five metrics that were renamed to follow the Prometheus naming conventions keep their historical
// names under the legacy prefix, so the existing dashboards and alerts keep working.
func legacyMetricName(name string) string {
	switch name {
	case "all_flags_evaluations_with_flag_total":
		return "all_flags_evaluations_total_with_flag"
	case "flag_update_by_flag_total":
		return "flag_update"
	case "flag_delete_by_flag_total":
		return "flag_delete"
	case "flag_create_by_flag_total":
		return "flag_create"
	case "force_refresh_total":
		return "force_refresh"
	default:
		return name
	}
}

// versionOptions overrides the version label of semaphore_build_info with version, if any.
func versionOptions(version string) []versioncollector.Option {
	if version == "" {
		return nil
	}

	return []versioncollector.Option{versioncollector.WithExtraConstLabels(prom.Labels{"version": version})}
}
