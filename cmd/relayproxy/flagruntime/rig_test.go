package flagruntime_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

// waitBound bounds every wait of a test on the engine. It only triggers when the engine does not answer, that is
// when a test fails; the tests never depend on it to pass.
const waitBound = 30 * time.Second

// rig runs an engine. Every round ends with a result on rounds, and every change handed to the consumer ends
// with a signal on emitted, so a test waits for the engine's own signals, bounded by waitBound and by the end of
// the engine goroutine.
type rig struct {
	t       *testing.T
	holder  *flagruntime.Holder
	eng     *flagruntime.Engine
	rounds  chan flagruntime.Reload
	emitted chan flagruntime.Change
	done    chan struct{}
	cancel  context.CancelFunc
	label   string
	// parsed is appended by the engine goroutine; a test reads it after receiving a round result.
	parsed []flagruntime.ParseEvent
	once   sync.Once
}

func newRig(t *testing.T, hooks flagruntime.Hooks, opts ...flagruntime.Option) *rig {
	t.Helper()

	return newRigLogger(t, nil, hooks, opts...)
}

func newRigLogger(t *testing.T, logger *slog.Logger, hooks flagruntime.Hooks, opts ...flagruntime.Option) *rig {
	t.Helper()

	harness := &rig{
		t:       t,
		holder:  flagruntime.NewHolder(),
		rounds:  make(chan flagruntime.Reload, 1024),
		emitted: make(chan flagruntime.Change, 1024),
		done:    make(chan struct{}),
		label:   fmt.Sprintf("%s-%p", t.Name(), t),
	}

	user := hooks
	hooks.OnParse = func(event flagruntime.ParseEvent) {
		harness.parsed = append(harness.parsed, event)

		if user.OnParse != nil {
			user.OnParse(event)
		}
	}
	hooks.AfterRound = func(res flagruntime.Reload) {
		if user.AfterRound != nil {
			user.AfterRound(res)
		}

		harness.rounds <- res
	}
	hooks.OnEmit = func(change flagruntime.Change) { harness.emitted <- change }

	harness.eng = flagruntime.NewEngine(
		logger,
		newService(t),
		harness.holder,
		append([]flagruntime.Option{flagruntime.WithHooks(hooks)}, opts...)...)

	t.Cleanup(harness.stop)

	return harness
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

// labelledGoroutines counts the goroutines (not the groups of the profile) of the process that carry the label
// of this rig's engine: a group line starts with the number of goroutines of the group.
func (r *rig) labelledGoroutines() int {
	var buf bytes.Buffer

	_ = pprof.Lookup("goroutine").WriteTo(&buf, 1)

	count, group := 0, 0

	for line := range strings.SplitSeq(buf.String(), "\n") {
		if size, _, found := strings.Cut(line, " @ "); found {
			group, _ = strconv.Atoi(size)

			continue
		}

		if strings.HasPrefix(line, "# labels:") && strings.Contains(line, `"engine":"`+r.label+`"`) {
			count += group
		}
	}

	return count
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

// bound returns the channel of a timer of waitBound and the function that stops it.
func bound() (<-chan time.Time, func() bool) {
	timer := time.NewTimer(waitBound)

	return timer.C, timer.Stop
}

// nextRound waits for the result of the next round.
func (r *rig) nextRound() flagruntime.Reload {
	r.t.Helper()

	expired, stop := bound()
	defer stop()

	select {
	case res := <-r.rounds:
		return res
	case <-r.done:
		require.FailNow(r.t, "the engine stopped before the round ended")
	case <-expired:
		require.FailNow(r.t, "no round ended: the engine does not answer")
	}

	return flagruntime.Reload{}
}

// nextChange waits for the next change the consumer reads from the channel of the engine.
func (r *rig) nextChange() flagruntime.Change {
	r.t.Helper()

	expired, stop := bound()
	defer stop()

	select {
	case change, open := <-r.eng.Changes():
		require.True(r.t, open, "the change channel is closed")

		return change
	case <-r.done:
		require.FailNow(r.t, "the engine stopped before a change arrived")
	case <-expired:
		require.FailNow(r.t, "no change arrived: the engine does not answer")
	}

	return flagruntime.Change{}
}

// nextEmitted waits until the engine has handed one more change to the channel (without reading it).
func (r *rig) nextEmitted() {
	r.t.Helper()

	expired, stop := bound()
	defer stop()

	select {
	case <-r.emitted:
	case <-r.done:
		require.FailNow(r.t, "the engine stopped before it emitted a change")
	case <-expired:
		require.FailNow(r.t, "no change was emitted: the engine does not answer")
	}
}

// feed hands a content to the engine and waits for the round to end.
func (r *rig) feed(content *flagruntime.Content) flagruntime.Reload {
	r.t.Helper()

	r.eng.Submit(content)

	return r.nextRound()
}

// reload hands a bootstrap configuration to the engine and waits for the round to end.
func (r *rig) reload(cfg *bootstrap.Config) flagruntime.Reload {
	r.t.Helper()

	r.eng.Reload(cfg)

	return r.nextRound()
}

func bootstrapFromYAML(t *testing.T, text string) *bootstrap.Config {
	t.Helper()

	var cfg bootstrap.Config

	require.NoError(t, yaml.Unmarshal([]byte(text), &cfg))

	return &cfg
}
