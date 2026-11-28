//go:build bench

package corehttp_test

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
)

// trackingFlags are two flags that differ only in trackEvents.
const trackingFlags = `
tracked:
  variations: {a: alpha, b: beta}
  targeting:
    - {name: vip, query: 'tier eq "vip"', variation: b}
  defaultRule: {name: default, variation: a}
  trackEvents: true
untracked:
  variations: {a: alpha, b: beta}
  targeting:
    - {name: vip, query: 'tier eq "vip"', variation: b}
  defaultRule: {name: default, variation: a}
  trackEvents: false
`

// discardEvents is the manager of the exporters of the benchmark: it takes the events and keeps nothing, so the cost
// measured is the one of the evaluation and of the pipeline up to the manager.
type discardEvents struct{}

func (discardEvents) AddEvent(*exporter.FeatureEvent)      {}
func (discardEvents) QueueEvents([]*exporter.FeatureEvent) {}
func (discardEvents) Start()                               {}
func (discardEvents) Stop()                                {}

// BenchmarkEvaluationTracking measures the evaluation of one flag with its tracking off and on. The criterion of the
// event pipeline: the tracked evaluation stays within 5 % of the untracked one, and neither allocates more than it
// did before the pipeline had its overflow policies.
func BenchmarkEvaluationTracking(b *testing.B) {
	rig := buildV2(b, &v2Options{cold: true}, func(string, exporter.Config, *slog.Logger) events.FeatureManager {
		return discardEvents{}
	})
	rig.flags = map[string][]byte{flagsObject: []byte(trackingFlags)}
	rig.publish(b)
	rig.poll(b)

	resolver := corehttp.NewAdapter(rig.core)

	for _, flag := range []string{"untracked", "tracked"} {
		b.Run(flag, func(b *testing.B) {
			flagSet, err := resolver.FlagSet(tenantKey)
			require.NoError(b, err)

			evalCtx := ffcontext.NewEvaluationContext("user-1")

			b.ReportAllocs()

			for b.Loop() {
				_, err = flagSet.RawVariation(flag, evalCtx, nil)
				require.NoError(b, err)
			}
		})
	}

	// the same through the HTTP server of the relay: what a client of the OFREP endpoint pays
	for _, flag := range []string{"untracked", "tracked"} {
		b.Run("request/"+flag, func(b *testing.B) {
			userCtx := `{"context":{"targetingKey":"user-1"}}`
			url := rig.url + pathOFREPOne + flag

			b.ReportAllocs()

			for b.Loop() {
				status, _, _ := do(b, http.MethodPost, url, userCtx, keyHeader(tenantKey))
				require.Equal(b, http.StatusOK, status)
			}
		})
	}
}
