package relay_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/thomaspoignant/go-feature-flag/cmd/relayproxy/config"
	controller "github.com/thomaspoignant/go-feature-flag/cmd/relayproxy/handler/goff"
	"github.com/thomaspoignant/go-feature-flag/cmd/relayproxy/handler/ofrep"
	"github.com/thomaspoignant/go-feature-flag/internal/loadtest"
	"github.com/thomaspoignant/go-feature-flag/internal/loadtest/relay"
)

// invoke runs the handler once the way the router would, without any network.
func invoke(b *testing.B, e *echo.Echo, h func(*echo.Context) error, ep relay.Endpoint, key string, i int) {
	req := httptest.NewRequest(http.MethodPost, ep.Path(key), strings.NewReader(ep.Body(i)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPathValues(echo.PathValues{{Name: "flagKey", Value: key}})
	if err := h(c); err != nil {
		b.Error(err)
		return
	}
	if rec.Code != http.StatusOK {
		b.Errorf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
}

// BenchmarkHandlers calls the relay proxy handlers in-process (no router, no middleware, no
// network) for a flag with event tracking OFF and ON, sequentially and in parallel.
func BenchmarkHandlers(b *testing.B) {
	r, err := relay.Start(relay.Options{
		FlagsYAML:    loadtest.FlagsYAML(),
		ExporterMode: loadtest.ExporterDiscard,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close()
	fm, metrics := r.Services.FlagsetManager, r.Services.Metrics
	goffEval := controller.NewFlagEval(fm, metrics)
	goffAll := controller.NewAllFlags(fm, metrics)
	ofrepCtrl := ofrep.NewOFREPEvaluate(fm, metrics, config.OfrepEventStream{})
	handlers := map[relay.Endpoint]func(*echo.Context) error{
		relay.GOFFEval:     goffEval.Handler,
		relay.GOFFAllFlags: goffAll.Handler,
		relay.OFREPEval:    ofrepCtrl.Evaluate,
		relay.OFREPBulk:    ofrepCtrl.BulkEvaluate,
	}
	e := echo.New()
	for _, ep := range relay.Endpoints {
		for _, track := range []bool{false, true} {
			if (ep == relay.GOFFAllFlags || ep == relay.OFREPBulk) && track {
				continue
			}
			key := loadtest.FlagKey("rule", "bool", track)
			h := handlers[ep]
			b.Run(fmt.Sprintf("%s/track=%t", ep, track), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
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
