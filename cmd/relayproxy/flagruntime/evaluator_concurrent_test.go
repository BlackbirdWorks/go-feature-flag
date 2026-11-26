package flagruntime_test

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/ffcontext"
)

// Evaluations that run while the engine swaps generations always see one whole generation: the flag set they
// routed to answers with a value of its own generation (never a mix), a key never stops routing while it is listed,
// and a returned object can be changed by its caller without a reader of another goroutine seeing it. Run with
// -race: it is the data race detector that proves the absence of a shared write.
func TestEvaluator_ConcurrentWithSwaps(t *testing.T) {
	t.Parallel()

	const (
		readers     = 4
		generations = 40
	)

	objectFlag := func(gen int) []byte {
		return fmt.Appendf(nil,
			"obj:\n  variations:\n    v:\n      gen: %d\n      list: [%d]\n  defaultRule:\n    variation: v\n"+
				"num:\n  variations:\n    v: %d\n  defaultRule:\n    variation: v\n", gen, gen, gen)
	}
	build := func(gen int) *flagruntime.Content {
		return content(uint64(gen), objs{
			flagsName: obj(flagruntime.KindFlags, objectFlag(gen)),
			setsName:  setsObj(t, fset("a", src(flagsName), keyA), fset("b", src(flagsName), keyB)),
		})
	}

	harness := newRigLogger(t, slog.New(slog.DiscardHandler), flagruntime.Hooks{})
	harness.start()
	harness.feed(build(1))

	eval := flagruntime.NewEvaluator(newService(t), nil)

	var (
		stop   atomic.Bool
		wg     sync.WaitGroup
		served atomic.Int64
	)

	for range readers {
		wg.Go(func() {
			ctx := ffcontext.NewEvaluationContext("user")

			// at least one pass, whatever the scheduler does
			for first := true; first || !stop.Load(); first = false {
				run := harness.holder.Load()

				for _, key := range []string{keyA, keyB} {
					set, route := run.Route(key)
					if !assert.Equal(t, flagruntime.RouteFlagSet, route) {
						return
					}

					obj, _ := eval.RawVariation(set, "obj", ctx, nil)
					num, _ := eval.IntVariationDetails(set, "num", ctx, -1)

					val, ok := obj.Value.(map[string]any)
					if !assert.True(t, ok) {
						return
					}

					// both flags come from the same generation: the object and the number agree
					assert.EqualValues(t, num.Value, val["gen"])

					// the caller owns the value: this write would race with the other readers if it was shared
					val["gen"] = -1
					val["list"].([]any)[0] = -1 //nolint:forcetypeassert,errcheck // the shape is known

					served.Add(1)
				}
			}
		})
	}

	for gen := 2; gen <= generations; gen++ {
		harness.feed(build(gen))
	}

	stop.Store(true)
	wg.Wait()

	assert.Positive(t, served.Load())
	assert.EqualValues(t, generations, harness.holder.Load().Generation)
}
