package relay_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/labstack/echo/v5"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/ofrep"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/internal/loadtest"
	"github.com/blackbirdworks/semaphore/internal/loadtest/relay"
)

// invoke runs the handler once the way the router would, without any network.
func invoke(b *testing.B, e *echo.Echo, h func(*echo.Context) error, ep relay.Endpoint, key string, i int) {
	req := httptest.NewRequest(http.MethodPost, ep.Path(key), strings.NewReader(ep.Body(i)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPathValues(echo.PathValues{{Name: "flagKey", Value: key}})
	if err := h(c); err != nil {
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
	fm, metrics := r.Services.FlagsetManager, r.Services.Metrics
	semEval := controller.NewFlagEval(fm, metrics)
	semAll := controller.NewAllFlags(fm, metrics)
	ofrepCtrl := ofrep.NewOFREPEvaluate(fm, metrics, config.OfrepEventStream{})
	handlers := map[relay.Endpoint]func(*echo.Context) error{
		relay.SemaphoreEval:     semEval.Handler,
		relay.SemaphoreAllFlags: semAll.Handler,
		relay.OFREPEval:         ofrepCtrl.Evaluate,
		relay.OFREPBulk:         ofrepCtrl.BulkEvaluate,
	}
	e := echo.New()
	for _, ep := range relay.Endpoints {
		for _, track := range []bool{false, true} {
			if (ep == relay.SemaphoreAllFlags || ep == relay.OFREPBulk) && track {
				continue
			}
			key := loadtest.FlagKey("rule", "bool", track)
			h := handlers[ep]
			b.Run(fmt.Sprintf("%s/track=%t", ep, track), func(b *testing.B) {
				b.ReportAllocs()
				for i := range b.N {
					invoke(b, e, h, ep, key, i)
				}
			})
			b.Run(fmt.Sprintf("%s/track=%t/parallel", ep, track), func(b *testing.B) {
				b.ReportAllocs()
				b.RunParallel(func(pb *testing.PB) {
					i := 0
					for pb.Next() {
						i++
						invoke(b, e, h, ep, key, i)
					}
				})
			})
		}
	}
}
