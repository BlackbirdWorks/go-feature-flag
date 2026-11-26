package events_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/notifier"
)

// wait is the longest a test waits for something that must happen: a missing event fails the test instead of
// hanging it. It is a bound, never a pause: every wait returns as soon as its condition holds.
const wait = 20 * time.Second

// await fails the test when ch does not receive within wait.
func await[T any]( //nolint:ireturn // a generic helper returns what the channel carries
	tb testing.TB, ch <-chan T, what string) T {
	tb.Helper()

	select {
	case v := <-ch:
		return v
	case <-time.After(wait):
		require.FailNow(tb, "timed out waiting for "+what)

		var zero T

		return zero
	}
}

// ev builds an event whose key identifies it.
func ev(id int) *exporter.FeatureEvent {
	return &exporter.FeatureEvent{Kind: "feature", Key: "flag-" + strconv.Itoa(id), UserKey: "u" + strconv.Itoa(id)}
}

// keys returns the sorted keys of the events.
func keys(list []*exporter.FeatureEvent) []string {
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, e.Key)
	}

	slices.Sort(out)

	return out
}

// fakeManager is a manager that records what it is given. A closed gate lets QueueEvents and Stop through.
type fakeManager struct {
	// signal gets one token per recorded event.
	signal   chan struct{}
	gate     chan struct{}
	stopGate chan struct{}
	entered  chan struct{}
	// exp is the exporter of the manager: Stop flushes the events to it, like the engine manager does.
	exp     exporter.Exporter
	name    string
	events  []*exporter.FeatureEvent
	mu      sync.Mutex
	stops   int
	starts  int
	dropped atomic.Int64
}

func (f *fakeManager) AddEvent(event *exporter.FeatureEvent) {
	f.QueueEvents([]*exporter.FeatureEvent{event})
}

func (f *fakeManager) QueueEvents(batch []*exporter.FeatureEvent) {
	if f.entered != nil {
		select {
		case f.entered <- struct{}{}:
		default:
		}
	}

	if f.gate != nil {
		<-f.gate
	}

	f.mu.Lock()
	f.events = append(f.events, slices.Clone(batch)...)
	f.mu.Unlock()

	for range batch {
		select {
		case f.signal <- struct{}{}:
		default:
		}
	}
}

func (f *fakeManager) Start() {
	f.mu.Lock()
	f.starts++
	f.mu.Unlock()
}

func (f *fakeManager) Stop() {
	f.mu.Lock()
	f.stops++
	f.mu.Unlock()

	if f.stopGate != nil {
		<-f.stopGate
	}

	f.mu.Lock()
	pending := make([]exporter.ExportableEvent, 0, len(f.events))

	for _, event := range f.events {
		pending = append(pending, event)
	}

	f.mu.Unlock()

	if f.exp != nil && len(pending) > 0 {
		_ = f.exp.Export(context.Background(), nil, pending)
	}
}

func (f *fakeManager) DroppedEvents() int64 { return f.dropped.Load() }

// waitFor waits until the manager recorded n more events than it had at the previous call.
func (f *fakeManager) waitFor(tb testing.TB, n int) {
	tb.Helper()

	for range n {
		await(tb, f.signal, "an event to reach the manager of "+f.name)
	}
}

func (f *fakeManager) got() []*exporter.FeatureEvent {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.events)
}

func (f *fakeManager) stopCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.stops
}

// factory builds fake managers and remembers them, by exporter name, in the order they were built.
type factory struct {
	gate     chan struct{}
	stopGate chan struct{}
	entered  chan struct{}
	managers map[string][]*fakeManager
	mu       sync.Mutex
	built    int
}

func newFactory() *factory { return &factory{managers: map[string][]*fakeManager{}} }

func (f *factory) build( //nolint:ireturn // it implements events.NewManagerFunc
	name string, cfg exporter.Config, _ *slog.Logger,
) events.FeatureManager {
	f.mu.Lock()
	defer f.mu.Unlock()

	exp, _ := cfg.Exporter.(exporter.Exporter)

	mgr := &fakeManager{
		exp:      exp,
		name:     name,
		gate:     f.gate,
		stopGate: f.stopGate,
		entered:  f.entered,
		signal:   make(chan struct{}, 1<<16),
	}
	f.managers[name] = append(f.managers[name], mgr)
	f.built++

	return mgr
}

func (f *factory) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.built
}

func (f *factory) of(name string, generation int) *fakeManager {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.managers[name][generation]
}

func (f *factory) generations(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.managers[name])
}

// nopExporter is an engine exporter that records what it exports.
type nopExporter struct {
	err    error
	events [][]exporter.ExportableEvent
	mu     sync.Mutex
}

func (*nopExporter) IsBulk() bool { return true }

func (n *nopExporter) Export(_ context.Context, _ *slog.Logger, batch []exporter.ExportableEvent) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.events = append(n.events, slices.Clone(batch))

	return n.err
}

// def is the definition of an exporter with a fake engine exporter.
func def(name, identity string) events.ExporterDef {
	return events.NewExporterDef(name, "file", identity, &nopExporter{}, exporter.Config{})
}

// names returns def(name, "v1") for each name.
func defs(names ...string) []events.ExporterDef {
	out := make([]events.ExporterDef, 0, len(names))
	for _, n := range names {
		out = append(out, def(n, "v1"))
	}

	return out
}

// startDispatcher returns a started dispatcher with the fake manager factory.
func startDispatcher(tb testing.TB, fac *factory, cfg *events.DispatcherConfig) *events.Dispatcher {
	tb.Helper()

	cfg.NewManager = fac.build
	disp := events.NewDispatcher(cfg)

	require.NoError(tb, disp.Start(context.Background()))
	tb.Cleanup(func() { _, _ = disp.Shutdown(canceled()) })

	return disp
}

// canceled returns a context that is already done: a drain under it is cut at once.
func canceled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	return ctx
}

// bounded returns a context that ends after wait, so that a drain that must succeed fails instead of hanging.
func bounded(tb testing.TB) context.Context {
	tb.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), wait)
	tb.Cleanup(cancel)

	return ctx
}

// labelled counts the goroutines that carry the label value, with the goroutine profile. A goroutine inherits
// the labels of its creator, so the goroutines a component starts under [pprof.Do] are exactly those with the
// label: the count does not depend on the other tests that run in parallel.
func labelled(value string) int {
	var buf bytes.Buffer

	_ = pprof.Lookup("goroutine").WriteTo(&buf, 1)

	header := regexp.MustCompile(`^(\d+) @`)
	label := `# labels: {"events-test":"` + value + `"}`
	total, current := 0, 0

	scanner := bufio.NewScanner(&buf)
	for scanner.Scan() {
		line := scanner.Text()
		if m := header.FindStringSubmatch(line); m != nil {
			current, _ = strconv.Atoi(m[1])

			continue
		}

		if strings.TrimSpace(line) == label {
			total += current
		}
	}

	return total
}

var labelSeq atomic.Int64 //nolint:gochecknoglobals // a counter of test labels

// labeledRun runs fn under a unique label and returns the label value.
func labeledRun(tb testing.TB, fn func(ctx context.Context)) string {
	tb.Helper()

	value := fmt.Sprintf("%s-%d", tb.Name(), labelSeq.Add(1))

	pprof.Do(context.Background(), pprof.Labels("events-test", value), fn)

	return value
}

// change builds a change of a flag set; seq is the key of the flag it adds.
func change(flagSet string, seq int) flagruntime.Change {
	return flagruntime.Change{
		FlagSet: flagSet,
		Diff: notifier.DiffCache{
			Added: map[string]flagdef.Flag{strconv.Itoa(seq): nil},
		},
	}
}

// recordingNotifier records the changes it gets. A closed gate lets Notify through.
type recordingNotifier struct {
	err     error
	gate    chan struct{}
	entered chan struct{}
	got     chan notifier.DiffCache
	panicOn string
	diffs   []notifier.DiffCache
	calls   atomic.Int64
	mu      sync.Mutex
}

func (r *recordingNotifier) Notify(diff notifier.DiffCache) error {
	r.calls.Add(1)

	if r.entered != nil {
		select {
		case r.entered <- struct{}{}:
		default:
		}
	}

	if r.gate != nil {
		<-r.gate
	}

	if _, bad := diff.Added[r.panicOn]; r.panicOn != "" && bad {
		panic("boom")
	}

	r.mu.Lock()
	r.diffs = append(r.diffs, diff)
	r.mu.Unlock()

	if r.got != nil {
		r.got <- diff
	}

	return r.err
}

func (r *recordingNotifier) received() []notifier.DiffCache {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.diffs)
}

// logRecorder is a slog handler that keeps the lines it is given.
type logRecorder struct {
	// onLine, when set, gets each line: it is a signal for a test that must act after a log line.
	onLine func(string)
	lines  *[]string
	mu     *sync.Mutex
	attrs  []slog.Attr
}

func newLogRecorder() (*logRecorder, func() []string) {
	rec := &logRecorder{lines: &[]string{}, mu: &sync.Mutex{}}

	return rec, func() []string {
		rec.mu.Lock()
		defer rec.mu.Unlock()

		return slices.Clone(*rec.lines)
	}
}

func (*logRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (l *logRecorder) Handle(_ context.Context, record slog.Record) error { //nolint:gocritic // slog.Handler signature
	var line strings.Builder

	line.WriteString(record.Level.String() + " " + record.Message)

	write := func(a slog.Attr) bool {
		fmt.Fprintf(&line, " %s=%v", a.Key, a.Value.Resolve())

		return true
	}

	for _, a := range l.attrs {
		write(a)
	}

	record.Attrs(write)

	l.mu.Lock()
	*l.lines = append(*l.lines, line.String())
	l.mu.Unlock()

	if l.onLine != nil {
		l.onLine(line.String())
	}

	return nil
}

func (l *logRecorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &logRecorder{onLine: l.onLine, lines: l.lines, mu: l.mu, attrs: append(slices.Clone(l.attrs), attrs...)}
}

func (l *logRecorder) WithGroup(string) slog.Handler { return l }

// unknownKindExporter is an exporters section with one entry of a kind the builder does not know.
func unknownKindExporter() map[string]bootstrap.Exporter {
	return map[string]bootstrap.Exporter{nameBad: {Kind: kindNope}}
}

// parseDoc parses a bootstrap document with the secrets of env.
func parseDoc(tb testing.TB, doc string, env map[string]string) (*bootstrap.Config, bootstrap.LoadOptions) {
	tb.Helper()

	opts := bootstrap.LoadOptions{
		LookupEnv: func(name string) (string, bool) {
			v, ok := env[name]

			return v, ok
		},
		ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
	}

	cfg, err := bootstrap.Parse([]byte(doc), nil, opts)
	require.NoError(tb, err)

	return cfg, opts
}

// connectionFactory returns a factory that knows the connections of cfg. It is what the relay will do when it
// switches to config v2: turn each connection of the bootstrap file into a spec.
func connectionFactory(
	tb testing.TB, cfg *bootstrap.Config, opts bootstrap.LoadOptions, options ...connection.Option,
) *connection.Factory {
	tb.Helper()

	fac := connection.New(options...)
	require.NoError(tb, fac.Replace(connectionFactorySpecs(tb, cfg, opts)))

	return fac
}

// connectionFactorySpecs turns the connections of cfg into specs.
func connectionFactorySpecs(
	tb testing.TB,
	cfg *bootstrap.Config,
	opts bootstrap.LoadOptions,
) map[string]connection.Spec {
	tb.Helper()

	specs := map[string]connection.Spec{}

	for name, conn := range cfg.Connections {
		var aws *bootstrap.AWS

		switch {
		case conn.S3 != nil:
			aws = &conn.S3.AWS
		case conn.Kinesis != nil:
			aws = &conn.Kinesis.AWS
		case conn.SQS != nil:
			aws = &conn.SQS.AWS
		default:
			aws = &conn.Redshift.AWS
		}

		common := connection.Common{Endpoint: aws.Endpoint, Region: aws.Region}

		if creds := aws.Credentials; creds != nil && creds.Type == bootstrap.CredentialsStatic {
			id, err := creds.AccessKeyID.Resolve(opts)
			require.NoError(tb, err)

			secret, err := creds.SecretAccessKey.Resolve(opts)
			require.NoError(tb, err)

			common.Credentials = connection.Credentials{
				Type:            connection.CredentialsStatic,
				AccessKeyID:     connection.NewSecret(id.Reveal()),
				SecretAccessKey: connection.NewSecret(secret.Reveal()),
			}
		}

		if conn.S3 != nil {
			specs[name] = &connection.S3Spec{Common: common, Bucket: conn.S3.Bucket, PathStyle: conn.S3.PathStyle}

			continue
		}

		specs[name] = &connection.AWSSpec{Common: common, Type: conn.Type()}
	}

	return specs
}

// inner returns the engine exporter of a definition as the type T.
func inner[T exporter.Exporter]( //nolint:ireturn // a generic helper returns the type it is asked for
	tb testing.TB, def *events.ExporterDef) T {
	tb.Helper()

	got, ok := def.Inner.(T)
	require.True(tb, ok, "the exporter of kind %s is a %T", def.Kind, def.Inner)

	return got
}
