package events_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"runtime"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
	signal chan struct{}
	// stopEntered gets a token when Stop is called.
	stopEntered chan struct{}
	gate        chan struct{}
	stopGate    chan struct{}
	entered     chan struct{}
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

	if f.stopEntered != nil {
		select {
		case f.stopEntered <- struct{}{}:
		default:
		}
	}

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
	stopEntered chan struct{}
	gate        chan struct{}
	stopGate    chan struct{}
	entered     chan struct{}
	managers    map[string][]*fakeManager
	mu          sync.Mutex
	built       int
}

func newFactory() *factory { return &factory{managers: map[string][]*fakeManager{}} }

func (f *factory) build( //nolint:ireturn // it implements events.NewManagerFunc
	name string, cfg exporter.Config, _ *slog.Logger,
) events.FeatureManager {
	f.mu.Lock()
	defer f.mu.Unlock()

	exp, _ := cfg.Exporter.(exporter.Exporter)

	mgr := &fakeManager{
		exp:         exp,
		name:        name,
		gate:        f.gate,
		stopGate:    f.stopGate,
		entered:     f.entered,
		stopEntered: f.stopEntered,
		signal:      make(chan struct{}, 1<<16),
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
	cleanupBounded(tb, func() { _, _ = disp.Shutdown(canceled()) })

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

	got, ok := def.Inner.Unseal().(T)
	require.True(tb, ok, "the exporter of kind %s is a %T", def.Kind, def.Inner.Unseal())

	return got
}

// assertExporterAccounting checks, for each exporter of the status, that every event handed to it is exported,
// dropped by one reason or still queued, and that nothing is counted twice: handed = exported + overflow + drain +
// queued, with no figure above what was handed.
func assertExporterAccounting(tb testing.TB, status *events.Status, when string) {
	tb.Helper()

	for i := range status.Exporters {
		exp := &status.Exporters[i]
		gone := exp.Exported + exp.Dropped.Overflow + exp.Dropped.Drain

		assert.LessOrEqual(
			tb,
			gone,
			exp.Handed,
			"%s: %s: exported and dropped events are more than what was handed",
			when,
			exp.Name,
		)
		assert.Equal(
			tb,
			exp.Handed,
			gone+uint64(exp.Queued),
			"%s: %s: handed = exported + overflow + drain + queued",
			when,
			exp.Name,
		)
	}
}

// assertGlobalAccounting checks the accounting of the shared channel for a set of exporters that did not change:
// every accepted event was handed to an exporter once (handed of any of them), counted as dropped, or is still in
// the channel.
func assertGlobalAccounting(tb testing.TB, status *events.Status, handed uint64, when string) {
	tb.Helper()

	dropped := status.Queue.Dropped

	assert.Equal(tb, status.Queue.Accepted, handed+dropped.NoExporter+dropped.Drain+uint64(status.Queue.Length),
		"%s: accepted = handed + no_exporter + drain + in the channel", when)
}

// statusOf returns the status of the exporter named name.
func statusOf(tb testing.TB, status *events.Status, name string) events.ExporterStatus {
	tb.Helper()

	for i := range status.Exporters {
		if status.Exporters[i].Name == name {
			return status.Exporters[i]
		}
	}

	require.FailNow(tb, "no exporter named "+name)

	return events.ExporterStatus{}
}

// gatedExporter is an engine exporter that waits for a gate before it exports.
type gatedExporter struct {
	gate     chan struct{}
	exported atomic.Uint64
}

func (*gatedExporter) IsBulk() bool { return true }

func (g *gatedExporter) Export(_ context.Context, _ *slog.Logger, batch []exporter.ExportableEvent) error {
	<-g.gate
	g.exported.Add(uint64(len(batch)))

	return nil
}

// tapManager wraps an engine manager and signals one token per event it is given, so that a test can wait for the
// events to reach the manager.
type tapManager struct {
	events.FeatureManager

	tap chan struct{}
}

func (t *tapManager) QueueEvents(batch []*exporter.FeatureEvent) {
	if async, ok := t.FeatureManager.(exporter.AsyncManager[*exporter.FeatureEvent]); ok {
		async.QueueEvents(batch)
	}

	for range batch {
		select {
		case t.tap <- struct{}{}:
		default:
		}
	}
}

func (t *tapManager) DroppedEvents() int64 {
	if counter, ok := t.FeatureManager.(exporter.DroppedEventsCounter); ok {
		return counter.DroppedEvents()
	}

	return 0
}

// tapped returns a manager constructor for the engine manager, and the channel of its tokens.
func tapped() (events.NewManagerFunc, chan struct{}) {
	tap := make(chan struct{}, 1<<16)

	return func(_ string, cfg exporter.Config, logger *slog.Logger) events.FeatureManager {
		return &tapManager{
			FeatureManager: exporter.NewManager[*exporter.FeatureEvent]([]exporter.Config{cfg}, 0, logger),
			tap:            tap,
		}
	}, tap
}

// eventKeys returns the sorted keys of the events with the identifiers 0 to n-1.
func eventKeys(n int) []string {
	all := make([]*exporter.FeatureEvent, 0, n)
	for i := range n {
		all = append(all, ev(i))
	}

	return keys(all)
}

// addedKeys returns the sorted keys of the flags the diffs added.
func addedKeys(diffs []notifier.DiffCache) []string {
	var out []string

	for _, diff := range diffs {
		for key := range diff.Added {
			out = append(out, key)
		}
	}

	slices.Sort(out)

	return out
}

// newDispatcher returns a dispatcher that is shut down when the test ends.
func newDispatcher(tb testing.TB, cfg *events.DispatcherConfig) *events.Dispatcher {
	tb.Helper()

	disp := events.NewDispatcher(cfg)

	cleanupBounded(tb, func() { _, _ = disp.Shutdown(canceled()) })

	return disp
}

// newNotifications returns a worker that is shut down when the test ends.
func newNotifications(tb testing.TB, cfg *events.NotificationsConfig) *events.Notifications {
	tb.Helper()

	notes := events.NewNotifications(cfg)

	cleanupBounded(tb, func() { _ = notes.Shutdown(canceled()) })

	return notes
}

// newRuntime returns a runtime that is shut down when the test ends.
func newRuntime(tb testing.TB, cfg *events.RuntimeConfig) *events.Runtime {
	tb.Helper()

	rt := events.NewRuntime(cfg)

	cleanupBounded(tb, func() { _, _ = rt.Shutdown(canceled()) })

	return rt
}

// creatorPrefix starts the "created by" line of a goroutine that the package under test started.
const creatorPrefix = "created by github.com/blackbirdworks/semaphore/cmd/relayproxy/events."

// leakedGoroutines returns the stacks of the goroutines of the package (the workers, the goroutines of the
// exporters and of the notifiers, the consumers) that are still running. The goroutines of the goroutine
// dump are classified by their creator: see [leakedIn].
func leakedGoroutines() []string {
	buf := make([]byte, 1<<20)

	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]

			break
		}

		buf = make([]byte, 2*len(buf))
	}

	return leakedIn(string(buf))
}

// leakedIn returns the goroutines of a goroutine dump that the package under test started. A goroutine is the
// package's when its creator, the "created by" frame, is in the package and is not a test. Its other frames do not
// matter: a worker that is stuck in a test double (a notifier or a manager of a test) has frames of the test, and
// is as much a leak as any other. A goroutine that a test started (its creator is in the test) is not one of the
// package, even when it calls into it.
func leakedIn(dump string) []string {
	var leaked []string

	for stack := range strings.SplitSeq(dump, "\n\n") {
		creator := ""

		for line := range strings.SplitSeq(stack, "\n") {
			if strings.HasPrefix(line, "created by ") {
				creator = line
			}
		}

		name, found := strings.CutPrefix(creator, creatorPrefix)
		if !found || strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") {
			continue
		}

		leaked = append(leaked, stack)
	}

	return leaked
}

// exitAfterLeakCheck returns the exit code of the test binary: the code of the tests, or 1 when a goroutine of the
// package is still running once they ended (it waits for them, bounded, as they end by themselves).
func exitAfterLeakCheck(code int) int {
	if code != 0 {
		return code
	}

	deadline := time.After(30 * time.Second)

	for {
		leaked := leakedGoroutines()
		if len(leaked) == 0 {
			return 0
		}

		select {
		case <-deadline:
			fmt.Fprintf(
				os.Stderr,
				"goroutines of the package still running after the tests:\n%s\n",
				strings.Join(leaked, "\n\n"),
			)

			return 1
		default:
			runtime.Gosched()
		}
	}
}

// plainFactory builds the connection factory of the connections of cfg.
func plainFactory(tb testing.TB, cfg *bootstrap.Config, opts bootstrap.LoadOptions) *connection.Factory {
	tb.Helper()

	return connectionFactory(tb, cfg, opts)
}

// noSecrets is a reader of secrets that reads none: every reference is unresolved.
func noSecrets(bootstrap.LoadOptions) bootstrap.LoadOptions { return bootstrap.LoadOptions{} }

// safeClose closes a gate that may be closed already.
func safeClose(gate chan struct{}) {
	defer func() { _ = recover() }()

	close(gate)
}

// newGate returns a gate that blocks the workers of a test until the test opens it. It is opened when the test
// ends, whatever the outcome, so that a failure cannot leave a worker blocked: register it before anything that
// can fail. The test opens it with safeClose, or close if it opens it only once.
func newGate(tb testing.TB) chan struct{} {
	tb.Helper()

	gate := make(chan struct{})

	tb.Cleanup(func() { safeClose(gate) })

	return gate
}

// cleanupBounded runs a shutdown when the test ends and waits for it for a bounded time. A shutdown that waits
// for a worker held on a gate cannot hang the test: the gates are opened by their own cleanups, which run after
// this one, and the shutdown then ends by itself.
func cleanupBounded(tb testing.TB, shutdown func()) {
	tb.Helper()

	tb.Cleanup(func() {
		done := make(chan struct{})

		go func() {
			defer close(done)

			shutdown()
		}()

		select {
		case <-done:
		case <-time.After(10 * time.Second):
			tb.Log("the shutdown is held; it ends when the gates of the test are opened")
		}
	})
}
