package controlsource_test

import (
	"bytes"
	"context"
	"fmt"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/controlsource"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// waitBound bounds every wait of a test; it only triggers when a test fails.
const waitBound = 30 * time.Second

// fakeReader is a manifest reader that the test drives: the channel is the one of the real reader (room for one).
type fakeReader struct {
	snaps  chan *manifestreader.Snapshot
	status manifestreader.Status
}

func newFakeReader() *fakeReader {
	return &fakeReader{snaps: make(chan *manifestreader.Snapshot, 1)}
}

func (f *fakeReader) Snapshots() <-chan *manifestreader.Snapshot { return f.snaps }
func (f *fakeReader) Status() manifestreader.Status              { return f.status }

// send hands a snapshot to the source, waiting for room (bounded).
func (f *fakeReader) send(tb testing.TB, snap *manifestreader.Snapshot) {
	tb.Helper()

	timer := time.NewTimer(waitBound)
	defer timer.Stop()

	select {
	case f.snaps <- snap:
	case <-timer.C:
		require.FailNow(tb, "the source does not take snapshots")
	}
}

// signalEngine wraps the engine: every Submit is announced on submitted, after the engine took it.
type signalEngine struct {
	*flagruntime.Engine

	submitted chan uint64
}

func (s *signalEngine) Submit(content *flagruntime.Content) {
	s.Engine.Submit(content)

	s.submitted <- content.Generation
}

// rig is a source, an engine and a fake reader. The engine runs only after startEngine, so a test can pile
// snapshots up in front of it.
type rig struct {
	t         *testing.T
	reader    *fakeReader
	holder    *flagruntime.Holder
	engine    *signalEngine
	source    *controlsource.Source
	rounds    chan flagruntime.RoundResult
	parsed    chan flagruntime.ParseEvent
	engineEnd chan struct{}
	srcEnd    chan struct{}
	cancel    context.CancelFunc
	label     string
	stopOnce  sync.Once
	started   bool
}

func newRig(t *testing.T) *rig {
	t.Helper()

	svc := flagdef.NewService()
	t.Cleanup(svc.Close)

	harness := &rig{
		t:         t,
		reader:    newFakeReader(),
		holder:    flagruntime.NewHolder(),
		rounds:    make(chan flagruntime.RoundResult, 1024),
		parsed:    make(chan flagruntime.ParseEvent, 1024),
		engineEnd: make(chan struct{}),
		srcEnd:    make(chan struct{}),
		label:     fmt.Sprintf("%s-%p", t.Name(), t),
	}

	eng := flagruntime.NewEngine(nil, svc, harness.holder, flagruntime.WithHooks(flagruntime.Hooks{
		OnParse:    func(event flagruntime.ParseEvent) { harness.parsed <- event },
		AfterRound: func(res flagruntime.RoundResult) { harness.rounds <- res },
	}))
	harness.engine = &signalEngine{Engine: eng, submitted: make(chan uint64, 1024)}
	harness.source = controlsource.New(harness.reader, harness.engine, harness.holder, nil)

	ctx, cancel := context.WithCancel(context.Background())
	harness.cancel = cancel

	t.Cleanup(harness.stop)

	// the source runs from the start, the engine on request
	go func() {
		defer close(harness.srcEnd)

		pprof.Do(ctx, pprof.Labels("source", harness.label), func(ctx context.Context) { harness.source.Run(ctx) })
	}()

	return harness
}

func (r *rig) startEngine() {
	r.started = true

	ctx, cancel := context.WithCancel(context.Background())
	prev := r.cancel

	r.cancel = func() {
		prev()
		cancel()
	}

	go func() {
		defer close(r.engineEnd)

		r.engine.Run(ctx)
	}()
}

func (r *rig) stop() {
	r.stopOnce.Do(func() {
		r.cancel()

		timer := time.NewTimer(waitBound)
		defer timer.Stop()

		select {
		case <-r.srcEnd:
		case <-timer.C:
			assert.Fail(r.t, "the source did not stop")
		}

		if !r.started {
			return
		}

		select {
		case <-r.engineEnd:
		case <-timer.C:
			assert.Fail(r.t, "the engine did not stop")
		}
	})
}

// submitted waits until the source has handed n more snapshots to the engine and returns their generations.
func (r *rig) submitted(count int) []uint64 {
	r.t.Helper()

	timer := time.NewTimer(waitBound)
	defer timer.Stop()

	out := make([]uint64, 0, count)

	for range count {
		select {
		case gen := <-r.engine.submitted:
			out = append(out, gen)
		case <-timer.C:
			require.FailNow(r.t, "the source did not submit")
		}
	}

	return out
}

// nextRound waits for the next round result of the engine.
func (r *rig) nextRound() flagruntime.RoundResult {
	r.t.Helper()

	timer := time.NewTimer(waitBound)
	defer timer.Stop()

	select {
	case res := <-r.rounds:
		return res
	case <-timer.C:
		require.FailNow(r.t, "no round ended")
	}

	return flagruntime.RoundResult{}
}

// parses returns what the engine parsed so far (events are queued by the hook; a round ends after its parses).
func (r *rig) parses() []flagruntime.ParseEvent {
	var out []flagruntime.ParseEvent

	for {
		select {
		case event := <-r.parsed:
			out = append(out, event)
		default:
			return out
		}
	}
}

// labelled counts the goroutines that carry the label of this rig's source.
func (r *rig) labelled() int {
	var buf bytes.Buffer

	_ = pprof.Lookup("goroutine").WriteTo(&buf, 1)

	count, group := 0, 0

	for line := range strings.SplitSeq(buf.String(), "\n") {
		if size, _, found := strings.Cut(line, " @ "); found {
			group, _ = strconv.Atoi(size)

			continue
		}

		if strings.HasPrefix(line, "# labels:") && strings.Contains(line, `"source":"`+r.label+`"`) {
			count += group
		}
	}

	return count
}

// feed sends a snapshot and waits for the round that applies it.
func (r *rig) feed(snap *manifestreader.Snapshot) flagruntime.RoundResult {
	r.t.Helper()

	r.reader.send(r.t, snap)
	r.submitted(1)

	return r.nextRound()
}

// ---- fixtures ----

func flagsYAML(value int, names ...string) []byte {
	var out strings.Builder

	for _, name := range names {
		fmt.Fprintf(&out, "%s:\n  variations:\n    v: %d\n  defaultRule:\n    variation: v\n", name, value)
	}

	return []byte(out.String())
}

func flagsObject(data []byte) manifestreader.ObjectState {
	return manifestreader.ObjectState{
		Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Checksum: controlplane.ChecksumOf(data),
		State: manifestreader.StateOK, Bytes: data,
	}
}

func setsObject(tb testing.TB, sets ...controlplane.FlagSet) manifestreader.ObjectState {
	tb.Helper()

	data, err := controlplane.MarshalFlagSet(&controlplane.FlagSetDocument{FlagSets: sets})
	require.NoError(tb, err)

	return manifestreader.ObjectState{
		Kind: controlplane.KindFlagSets, Format: controlplane.FormatYAML, Checksum: controlplane.ChecksumOf(data),
		State: manifestreader.StateOK, Bytes: data,
	}
}

func fset(name string, sources []string, keys ...string) controlplane.FlagSet {
	return controlplane.FlagSet{Name: name, Sources: sources, APIKeys: keys}
}

func snapshot(gen uint64, objects map[string]manifestreader.ObjectState) *manifestreader.Snapshot {
	return &manifestreader.Snapshot{Objects: objects, Channel: "prod", Generation: gen}
}

// failed turns an object into a failed one: stale keeps its bytes, missing has none.
func failed(data []byte, state manifestreader.State, reason string) manifestreader.ObjectState {
	out := flagsObject(data)
	out.State, out.Reason = state, reason

	if state == manifestreader.StateMissing {
		out.Bytes = nil
	}

	return out
}
