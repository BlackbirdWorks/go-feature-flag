package runtime_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/runtime"
)

// rig runs an engine. Every round ends with a result on rounds, so a test waits for the engine's own signal.
type rig struct {
	holder *runtime.Holder
	eng    *runtime.Engine
	rounds chan runtime.Reload
	done   chan struct{}
	cancel context.CancelFunc
	label  string
	// parsed is appended by the engine goroutine; a test reads it after receiving a round result.
	parsed []runtime.ParseEvent
	once   sync.Once
}

func newRig(t *testing.T, hooks runtime.Hooks, opts ...runtime.Option) *rig {
	t.Helper()

	return newRigLogger(t, nil, hooks, opts...)
}

func newRigLogger(t *testing.T, logger *slog.Logger, hooks runtime.Hooks, opts ...runtime.Option) *rig {
	t.Helper()

	r := &rig{
		holder: runtime.NewHolder(),
		rounds: make(chan runtime.Reload, 1024),
		done:   make(chan struct{}),
		label:  fmt.Sprintf("%s-%p", t.Name(), t),
	}

	user := hooks
	hooks.OnParse = func(e runtime.ParseEvent) {
		r.parsed = append(r.parsed, e)

		if user.OnParse != nil {
			user.OnParse(e)
		}
	}
	hooks.AfterRound = func(res runtime.Reload) {
		if user.AfterRound != nil {
			user.AfterRound(res)
		}

		r.rounds <- res
	}

	r.eng = runtime.NewEngine(
		logger,
		newService(t),
		r.holder,
		append([]runtime.Option{runtime.WithHooks(hooks)}, opts...)...)

	t.Cleanup(r.stop)

	return r
}

func (r *rig) start() {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel

	go func() {
		defer close(r.done)

		// the label is inherited by every goroutine that Run would start: a leak shows up under it
		pprof.Do(ctx, pprof.Labels("engine", r.label), func(ctx context.Context) { r.eng.Run(ctx) })
	}()
}

// labelledGoroutines counts the goroutines of the process that carry the label of this rig's engine.
func (r *rig) labelledGoroutines() int {
	var buf bytes.Buffer

	_ = pprof.Lookup("goroutine").WriteTo(&buf, 1)

	return strings.Count(buf.String(), `"engine":"`+r.label+`"`)
}

func (r *rig) stop() {
	r.once.Do(func() {
		if r.cancel == nil {
			return
		}

		r.cancel()
		<-r.done
	})
}

// feed hands a content to the engine and waits for the round to end.
func (r *rig) feed(c *runtime.Content) runtime.Reload {
	r.eng.Submit(c)

	return <-r.rounds
}

// reload hands a bootstrap configuration to the engine and waits for the round to end.
func (r *rig) reload(cfg *bootstrap.Config) runtime.Reload {
	r.eng.Reload(cfg)

	return <-r.rounds
}

func bootstrapFromYAML(t *testing.T, text string) *bootstrap.Config {
	t.Helper()

	var cfg bootstrap.Config

	require.NoError(t, yaml.Unmarshal([]byte(text), &cfg))

	return &cfg
}
