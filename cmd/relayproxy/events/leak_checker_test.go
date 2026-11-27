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
// when the package created it, whatever the frames above are.
func TestLeakedIn(t *testing.T) {
	t.Parallel()

	workerOnly := "goroutine 11 [chan receive]:\n" + pkgPath + ".(*managed).consume(0xc0)\n\t/x/managed.go:10 +0x1\n" +
		"created by " + pkgPath + ".(*Dispatcher).launch in goroutine 5\n\t/x/lifecycle.go:20 +0x2"
	stuckInDouble := "goroutine 12 [chan receive]:\n" +
		pkgPath + "_test.(*recordingNotifier).Notify(0xc0)\n\t/x/helpers_test.go:370 +0x1\n" +
		pkgPath + ".(*notifierRun).loop(0xc0)\n\t/x/notifier_run.go:80 +0x2\n" +
		"created by " + pkgPath + ".(*Notifications).launch in goroutine 5\n\t/x/notifications.go:200 +0x3"
	stuckInTestManager := "goroutine 13 [chan receive]:\n" +
		pkgPath + "_test.(*fakeManager).Stop(0xc0)\n\t/x/helpers_test.go:140 +0x1\n" +
		"created by " + pkgPath + ".(*managed).startClose in goroutine 5\n\t/x/managed.go:180 +0x2"
	startedByATest := "goroutine 14 [select]:\n" + pkgPath + ".(*Dispatcher).Collect(0xc0)\n\t/x/dispatcher.go:50 +0x1\n" +
		"created by " + pkgPath + "_test.TestSomething.func1 in goroutine 6\n\t/x/a_test.go:30 +0x2"
	startedByAnInternalTest := "goroutine 15 [select]:\n" +
		pkgPath + ".(*Dispatcher).Collect(0xc0)\n\t/x/dispatcher.go:50 +0x1\n" +
		"created by " + pkgPath + ".TestInternal.func1 in goroutine 6\n\t/x/b_test.go:30 +0x2"
	runtimeOnly := "goroutine 2 [force gc (idle)]:\nruntime.gopark(0x0)\n\t/go/proc.go:1 +0x1\n" +
		"created by runtime.init.7 in goroutine 1\n\t/go/proc.go:2 +0x2"
	noCreator := "goroutine 1 [running]:\nmain.main()\n\t/x/main.go:1 +0x1"

	tests := []struct {
		name        string
		dump        string
		wantHeaders []string
	}{
		{name: "empty dump", dump: "", wantHeaders: nil},
		{name: "a worker of the package", dump: workerOnly, wantHeaders: []string{"goroutine 11"}},
		{
			name: "a worker stuck inside a notifier of a test", dump: stuckInDouble,
			wantHeaders: []string{"goroutine 12"},
		},
		{
			name: "a closer stuck inside a manager of a test", dump: stuckInTestManager,
			wantHeaders: []string{"goroutine 13"},
		},
		{name: "a goroutine that a test started", dump: startedByATest, wantHeaders: nil},
		{name: "a goroutine that an internal test started", dump: startedByAnInternalTest, wantHeaders: nil},
		{name: "a runtime goroutine", dump: runtimeOnly, wantHeaders: nil},
		{name: "the main goroutine", dump: noCreator, wantHeaders: nil},
		{
			name: "a mixed dump",
			dump: strings.Join([]string{
				noCreator,
				runtimeOnly,
				workerOnly,
				startedByATest,
				stuckInDouble,
				startedByAnInternalTest,
				stuckInTestManager,
			}, "\n\n"),
			wantHeaders: []string{"goroutine 11", "goroutine 12", "goroutine 13"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got []string

			for _, stack := range leakedIn(tt.dump) {
				got = append(got, strings.SplitN(stack, " [", 2)[0])
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

func stacksWith(stacks []string, text string) []string {
	var out []string

	for _, stack := range stacks {
		if strings.Contains(stack, text) {
			out = append(out, stack)
		}
	}

	return out
}
