package events_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
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

	tests := []struct {
		name        string
		dump        string
		wantHeaders []string
	}{
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
	stuck := &recordingNotifier{gate: gate, entered: make(chan struct{}, 1)}

	notes, changes := startNotifications(t, &events.NotificationsConfig{})

	_, err := notes.Apply(context.Background(), []events.NotifierDef{ndef("n", "v1", stuck)})
	require.NoError(t, err)

	changes <- change("set", 1)

	await(t, stuck.entered, "the notifier to take its change")

	stacks := stacksWith(leakedGoroutines(), "recordingNotifier).Notify")
	assert.NotEmpty(t, stacks, "the worker that is held inside the notifier is a goroutine of the package")
}

// TestLeakCheckSeesAWorkerOfAWaitGroup leaks a worker that the package starts with [sync.WaitGroup.Go]: a shutdown
// that waits for an exporter whose close is held. The worker that waits for the exporter is the one reported.
func TestLeakCheckSeesAWorkerOfAWaitGroup(t *testing.T) {
	t.Parallel()

	fac := newFactory()
	fac.stopGate = newGate(t)
	fac.stopEntered = make(chan struct{}, 1)

	disp := newDispatcher(t, &events.DispatcherConfig{NewManager: fac.build})
	require.NoError(t, disp.Start(context.Background()))

	_, err := disp.Apply(context.Background(), defs("a"))
	require.NoError(t, err)

	shutdown := make(chan struct{})

	go func() {
		defer close(shutdown)

		_, _ = disp.Shutdown(context.Background())
	}()

	await(t, fac.stopEntered, "the exporter to be closed")

	stacks := stacksWith(leakedGoroutines(), "waitClosed.func1")
	assert.NotEmpty(t, stacks, "the worker that waits for the held exporter was started by WaitGroup.Go")

	// the exporter is released, so the shutdown ends and the cleanup of the dispatcher does not wait
	safeClose(fac.stopGate)
	await(t, shutdown, "the shutdown to end")
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
