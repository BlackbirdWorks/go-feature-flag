package events_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/notifier"
)

const pkgPath = "github.com/blackbirdworks/semaphore/cmd/relayproxy/events"

// TestLeakedIn checks the classification of the leak check on goroutine dumps: a goroutine belongs to the package
// when a frame of its stack, or its creator, is a function of the package that is not a test.
func TestLeakedIn(t *testing.T) {
	t.Parallel()

	const (
		lifecycle = pkgPath + ".(*Dispatcher)"
		testFrame = "\n\t/x/a_test.go:30 +0x1\n"
	)

	frame := func(function string) string { return function + "(0xc0)\n\t/x/f.go:1 +0x1\n" }
	created := func(function string) string { return "created by " + function + " in goroutine 5\n\t/x/f.go:2 +0x2" }
	stack := func(header string, lines ...string) string { return header + ":\n" + strings.Join(lines, "") }

	workerOnly := stack("goroutine 11 [chan receive]",
		frame(pkgPath+".(*managed).consume"), created(lifecycle+".launch"))
	waitGroupWorker := stack("goroutine 12 [chan receive]",
		frame(pkgPath+".(*managed).release"), frame(lifecycle+".awaitInstance"),
		frame(lifecycle+".waitClosed.func1"), created("sync.(*WaitGroup).Go"))
	stuckInDouble := stack("goroutine 13 [chan receive]",
		frame(pkgPath+"_test.(*recordingNotifier).Notify"), frame(pkgPath+".(*notifierRun).loop"),
		created(pkgPath+".(*Notifications).launch"))
	blockedTestGoroutine := stack("goroutine 14 [select]",
		frame(pkgPath+".(*Dispatcher).Shutdown"), frame(pkgPath+"_test.TestSomething.func1"),
		created(pkgPath+"_test.TestSomething"))
	returnedTestGoroutine := stack("goroutine 15 [select]",
		frame(pkgPath+"_test.TestSomething.func1"), created(pkgPath+"_test.TestSomething"))
	testGoroutine := stack("goroutine 16 [chan receive]",
		frame("testing.(*T).Run"), created("testing.(*T).Run"))
	internalTestGoroutine := stack("goroutine 17 [select]",
		frame(pkgPath+".TestInternal.func1"), created(pkgPath+".TestInternal"))
	runtimeGoroutine := stack("goroutine 2 [force gc (idle)]", frame("runtime.gopark"), created("runtime.init.7"))
	mainGoroutine := stack("goroutine 1 [running]", frame("main.main"))
	checker := stack("goroutine 18 [running]",
		frame(pkgPath+".(*Dispatcher).Status"), frame(pkgPath+"_test.leakedGoroutines"), strings.TrimSpace(testFrame))

	// a dump that the runtime wrote (go1.27.4) while Dispatcher.Shutdown waited for an exporter whose close was held:
	// the worker that sync.WaitGroup.Go started, the goroutine of the shutdown that waits for it, and the closer
	// that is held inside the manager of a test. The goroutine of the test, of the test framework and the
	// runtime are in the dump as well.
	captured, err := os.ReadFile("testdata/waitclosed_dump.txt")
	require.NoError(t, err)

	tests := []struct {
		name        string
		dump        string
		wantHeaders []string
	}{
		{
			name: "a dump of a shutdown that waits for a held exporter", dump: string(captured),
			wantHeaders: []string{
				"goroutine 35 [select]", "goroutine 23 [sync.WaitGroup.Wait]", "goroutine 34 [chan receive]",
			},
		},
		{name: "empty dump", dump: "", wantHeaders: nil},
		{name: "a worker of the package", dump: workerOnly, wantHeaders: []string{"goroutine 11 [chan receive]"}},
		{
			name: "a worker started by WaitGroup.Go, blocked in the drain of an exporter", dump: waitGroupWorker,
			wantHeaders: []string{"goroutine 12 [chan receive]"},
		},
		{
			name: "a worker blocked in a test double that the package called", dump: stuckInDouble,
			wantHeaders: []string{"goroutine 13 [chan receive]"},
		},
		{
			name: "a goroutine of a test, blocked inside the package", dump: blockedTestGoroutine,
			wantHeaders: []string{"goroutine 14 [select]"},
		},
		{name: "a goroutine of a test that called the package and returned", dump: returnedTestGoroutine},
		{name: "a goroutine of the test framework", dump: testGoroutine},
		{name: "a goroutine of an internal test", dump: internalTestGoroutine},
		{name: "a runtime goroutine", dump: runtimeGoroutine},
		{name: "the main goroutine", dump: mainGoroutine},
		{name: "the goroutine of the checker", dump: checker},
		{
			name: "a mixed dump",
			dump: strings.Join([]string{
				mainGoroutine, runtimeGoroutine, workerOnly, returnedTestGoroutine, waitGroupWorker, testGoroutine,
				checker, stuckInDouble, internalTestGoroutine, blockedTestGoroutine,
			}, "\n\n"),
			wantHeaders: []string{
				"goroutine 11 [chan receive]", "goroutine 12 [chan receive]", "goroutine 13 [chan receive]",
				"goroutine 14 [select]",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got []string

			for _, found := range leakedIn(tt.dump) {
				got = append(got, strings.SplitN(found, ":\n", 2)[0])
			}

			assert.Equal(t, tt.wantHeaders, got)
		})
	}
}

// TestLeakCheckSeesAWorkerStuckInANotifier holds a real worker of the package inside the notifier of a test, and
// checks that the leak check of the live process reports it.
func TestLeakCheckSeesAWorkerStuckInANotifier(t *testing.T) {
	t.Parallel()

	gate := newGate(t)
	stuck := &leakProbeNotifier{gate: gate, entered: make(chan struct{}, 1)}

	notes, changes := startNotifications(t, &events.NotificationsConfig{})

	_, err := notes.Apply(context.Background(), []events.NotifierDef{ndef("n", "v1", stuck)})
	require.NoError(t, err)

	changes <- change("set", 1)

	await(t, stuck.entered, "the notifier to take its change")

	stacks := stacksWith(leakedGoroutines(), "leakProbeNotifier).Notify")
	assert.Len(t, stacks, 1, "the worker that is held inside the notifier is a goroutine of the package")

	// the notifier is released, so the cleanup of the notifications does not wait
	safeClose(gate)
}

func stacksWith(stacks []string, text string) []string {
	var out []string

	for _, stack := range stacks {
		if strings.Contains(stack, text) {
			out = append(out, stack)
		}
	}

	return out
}

// leakProbeNotifier is a notifier that only this test uses: its Notify frame is in the stack of the worker that this
// test holds, and in no other goroutine, so the test finds its own worker among the goroutines of the process.
type leakProbeNotifier struct {
	gate    chan struct{}
	entered chan struct{}
}

func (p *leakProbeNotifier) Notify(notifier.DiffCache) error {
	select {
	case p.entered <- struct{}{}:
	default:
	}

	<-p.gate

	return nil
}
