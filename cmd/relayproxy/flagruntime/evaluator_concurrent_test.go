package flagruntime_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/ffcontext"
)

// Evaluations that run while the engine swaps generations see one whole generation. Many goroutines evaluate the
// same object flag with nested metadata and encode everything they got (the value, the metadata, the states, the
// hashes), the way the handlers do, while reloads replace the flags: under -race this proves that the shared parts
// (the value and the nested metadata containers are the flag's own) are only ever read, and that each result is
// internally consistent: the value, the metadata and the number of the same generation.
func TestEvaluator_ConcurrentReadersAndSwaps(t *testing.T) {
	t.Parallel()

	const (
		readers     = 6
		generations = 40
	)

	flagsOf := func(gen int) []byte {
		return fmt.Appendf(
			nil,
			"obj:\n  variations:\n    v:\n      gen: %d\n      list: [%d, {deep: [%d]}]\n  defaultRule:\n    variation: v\n"+
				"  metadata:\n    gen: %d\n    nested:\n      gen: %d\n      tags: [a, b]\n"+
				"num:\n  variations:\n    v: %d\n  defaultRule:\n    variation: v\n",
			gen,
			gen,
			gen,
			gen,
			gen,
			gen,
		)
	}
	build := func(gen int) *flagruntime.Content {
		return content(uint64(gen), objs{
			flagsName: obj(flagruntime.KindFlags, flagsOf(gen)),
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

					evaluateAndEncode(t, eval, set, ctx)

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

// evaluateAndEncode evaluates the flags of a flag set in every way and encodes the results.
func evaluateAndEncode(t *testing.T, eval *flagruntime.Evaluator, set *flagruntime.FlagSet, ctx ffcontext.Context) {
	t.Helper()

	raw, _ := eval.RawVariation(t.Context(), set, "obj", ctx, nil)
	num, _ := eval.IntVariationDetails(t.Context(), set, "num", ctx, -1)
	typed, _ := eval.JSONVariationDetails(t.Context(), set, "obj", ctx, nil)
	states := eval.AllFlagsState(t.Context(), set, ctx)
	meta := eval.ResponseMetadata(&raw)

	encoded, err := json.Marshal([]any{raw, typed, states, meta})
	if err != nil {
		assert.Fail(t, "the results do not encode", err.Error())

		return
	}

	assert.NotEmpty(t, encoded)

	change, err := set.Change()
	if err != nil {
		assert.Fail(t, "the change answer", err.Error())

		return
	}

	assert.Len(t, change.Flags, 2)

	conf, err := json.Marshal(set.Configuration(nil))
	if err != nil {
		assert.Fail(t, "the configuration does not encode", err.Error())

		return
	}

	assert.NotEmpty(t, conf)

	val, _ := raw.Value.(map[string]any)
	nested, _ := raw.Metadata["nested"].(map[string]any)

	// the value, the metadata and the number are those of one generation
	assert.EqualValues(t, num.Value, val["gen"])
	assert.EqualValues(t, num.Value, raw.Metadata["gen"])
	assert.EqualValues(t, num.Value, nested["gen"])
	assert.Equal(t, val["gen"], typed.Value["gen"])
}
