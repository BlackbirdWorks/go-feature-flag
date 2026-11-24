package metric

import (
	"strconv"
	"strings"

	prom "github.com/prometheus/client_golang/prometheus"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

const flagLoadedEvaluationKey = "semaphore-flag-loaded-metric"

// FlagsetSource gives access to the flagsets currently loaded by the relay proxy.
type FlagsetSource interface {
	AllFlagSets() (map[string]*ffclient.GoFeatureFlag, error)
}

type flagLoadedCollector struct {
	source FlagsetSource
	desc   *prom.Desc
}

func newFlagLoadedCollector(source FlagsetSource) *flagLoadedCollector {
	return &flagLoadedCollector{
		source: source,
		desc: prom.NewDesc(
			prom.BuildFQName("", SubSystem, "flag_loaded"),
			"Flags currently loaded in the relay proxy cache, one series per flag with value 1.",
			[]string{"flagset", flagNameLabel, "disabled"},
			nil,
		),
	}
}

func (c *flagLoadedCollector) Describe(ch chan<- *prom.Desc) {
	ch <- c.desc
}

func (c *flagLoadedCollector) Collect(ch chan<- prom.Metric) {
	flagsets, err := c.source.AllFlagSets()
	if err != nil {
		return
	}
	for flagsetName, flagset := range flagsets {
		if flagset == nil {
			continue
		}
		// the flags are only evaluated: not copying them keeps the memoized state of the scheduled rollouts.
		flags, err := flagset.GetFlagsFromCacheReadOnly()
		if err != nil {
			continue
		}
		svc := flagset.EvaluationService()
		for flagName, f := range flags {
			metric, err := prom.NewConstMetric(
				c.desc,
				prom.GaugeValue,
				1,
				flagsetName,
				flagName,
				strconv.FormatBool(isEffectivelyDisabled(svc, flagName, f)),
			)
			if err != nil {
				continue
			}
			ch <- metric
		}
	}
}

// isEffectivelyDisabled evaluates the flag so scheduled steps and experimentation windows are taken into account.
func isEffectivelyDisabled(svc *flag.Service, flagName string, f flag.Flag) bool {
	ctx := ffcontext.NewEvaluationContext(flagLoadedEvaluationKey)
	if internal, ok := f.(*flag.InternalFlag); ok && internal.GetBucketingKey() != "" {
		addNestedAttribute(ctx, internal.GetBucketingKey(), flagLoadedEvaluationKey)
	}
	_, details := f.Value(flagName, ctx, flag.Context{Service: svc})
	if details.Reason == flag.ReasonError {
		return f.IsDisable()
	}

	return details.Reason == flag.ReasonDisabled
}

// addNestedAttribute sets a dotted key such as "company.id" as nested maps, the shape GetNestedFieldValue reads.
func addNestedAttribute(ctx ffcontext.EvaluationContext, dottedKey string, value any) {
	parts := strings.Split(dottedKey, ".")
	current := ctx.GetCustom()
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	current[parts[len(parts)-1]] = value
}

// RegisterFlagLoaded exposes semaphore_flag_loaded, computed from the flag cache on every scrape.
func (m *Metrics) RegisterFlagLoaded(source FlagsetSource) error {
	if m.Registry == nil || source == nil {
		return nil
	}

	return m.Registry.Register(newFlagLoadedCollector(source))
}
