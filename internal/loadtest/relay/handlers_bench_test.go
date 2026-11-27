package relay_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/ofrep"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/internal/loadtest"
	"github.com/blackbirdworks/semaphore/internal/loadtest/relay"
)

// invoke runs the handler once the way the router would, without any network.
func invoke(
	b *testing.B,
	handler func(http.ResponseWriter, *http.Request) error,
	endpoint relay.Endpoint,
	key string,
	i int,
) {
	b.Helper()

	req := httptest.NewRequestWithContext(b.Context(), http.MethodPost, endpoint.Path(key),
		strings.NewReader(endpoint.Body(i)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", relay.APIKey)

	rec := httptest.NewRecorder()

	req.SetPathValue("flagKey", key)

	err := handler(rec, req)
	if err != nil {
		assert.NoError(b, err)

		return
	}

	assert.Equal(b, http.StatusOK, rec.Code, "unexpected status: %s", rec.Body.String())
}

// BenchmarkHandlers calls the relay proxy handlers in-process (no router, no middleware, no
// network) for a flag with event tracking OFF and ON, sequentially and in parallel.
func BenchmarkHandlers(b *testing.B) {
	r, err := relay.Start(relay.Options{
		FlagsYAML:    loadtest.FlagsYAML(),
		ExporterMode: loadtest.ExporterDiscard,
	})
	require.NoError(b, err)

	defer r.Close()

	fm, metrics := r.FlagSets, r.Metrics
	semEval := controller.NewFlagEval(fm, metrics)
	semAll := controller.NewAllFlags(fm, metrics)
	ofrepCtrl := ofrep.NewOFREPEvaluate(fm, metrics, ofrep.EventStream{})
	handlers := map[relay.Endpoint]func(http.ResponseWriter, *http.Request) error{
		relay.SemaphoreEval:     semEval.Handler,
		relay.SemaphoreAllFlags: semAll.Handler,
		relay.OFREPEval:         ofrepCtrl.Evaluate,
		relay.OFREPBulk:         ofrepCtrl.BulkEvaluate,
	}

	for _, endpoint := range relay.Endpoints() {
		for _, track := range []bool{false, true} {
			if (endpoint == relay.SemaphoreAllFlags || endpoint == relay.OFREPBulk) && track {
				continue
			}

			key := loadtest.FlagKey("rule", "bool", track)
			handler := handlers[endpoint]
			b.Run(fmt.Sprintf("%s/track=%t", endpoint, track), func(b *testing.B) {
				b.ReportAllocs()

				for i := range b.N {
					invoke(b, handler, endpoint, key, i)
				}
			})
			b.Run(fmt.Sprintf("%s/track=%t/parallel", endpoint, track), func(b *testing.B) {
				b.ReportAllocs()
				b.RunParallel(func(pb *testing.PB) {
					i := 0
					for pb.Next() {
						i++
						invoke(b, handler, endpoint, key, i)
					}
				})
			})
		}
	}
}
