package events_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/notifier"
)

// errRemoteDown is the cause of a failing notifier.
var errRemoteDown = errors.New("remote down")

func ndef(name, identity string, n notifier.Notifier) events.NotifierDef {
	return events.NotifierDef{Name: name, Kind: kindWebhook, Identity: identity, Notifier: n}
}

// startNotifications returns a started worker that reads the returned channel.
func startNotifications(
	tb testing.TB, cfg *events.NotificationsConfig,
) (*events.Notifications, chan flagruntime.Change) {
	tb.Helper()

	changes := make(chan flagruntime.Change)
	notes := events.NewNotifications(cfg)

	require.NoError(tb, notes.Start(context.Background(), changes))
	tb.Cleanup(func() { _ = notes.Shutdown(canceled()) })

	return notes, changes
}

// seqs returns, per flag set, the sequence numbers a notifier received, in the order it received them.
func seqs(tb testing.TB, diffs []notifier.DiffCache, flagSets map[string]string) map[string][]int {
	tb.Helper()

	out := map[string][]int{}

	for _, diff := range diffs {
		for key := range diff.Added {
			seq, err := strconv.Atoi(key)
			require.NoError(tb, err)

			name := flagSets[key]
			out[name] = append(out[name], seq)
		}
	}

	return out
}

func TestNotifications_EveryNotifierGetsEveryChangeInOrder(t *testing.T) {
	t.Parallel()

	const (
		perSet = 40
		sets   = 3
	)

	first, second := &recordingNotifier{got: make(chan notifier.DiffCache, 1000)}, &recordingNotifier{
		got: make(chan notifier.DiffCache, 1000),
	}

	notes, changes := startNotifications(t, &events.NotificationsConfig{QueueSize: sets * perSet})

	_, err := notes.Apply(context.Background(), []events.NotifierDef{ndef("a", "v1", first), ndef("b", "v1", second)})
	require.NoError(t, err)

	owner := map[string]string{} // sequence number -> flag set
	want := map[string][]int{}

	// the sequence numbers are unique, the changes of the flag sets are interleaved
	for i := range perSet {
		for s := range sets {
			name := "set-" + strconv.Itoa(s)
			seq := i*sets + s
			owner[strconv.Itoa(seq)] = name
			want[name] = append(want[name], seq)

			changes <- change(name, seq)
		}
	}

	for range sets * perSet {
		await(t, first.got, "the first notifier")
		await(t, second.got, "the second notifier")
	}

	assert.Equal(t, want, seqs(t, first.received(), owner), "first notifier: each change once, in order per flag set")
	assert.Equal(t, want, seqs(t, second.received(), owner), "second notifier: each change once, in order per flag set")
}

// TestNotifications_BlockedNotifierDelaysOnlyItself gates one notifier: the other gets everything, the channel is
// consumed all the time, and the blocked one drops what its bounded queue cannot hold.
func TestNotifications_BlockedNotifierDelaysOnlyItself(t *testing.T) {
	t.Parallel()

	const (
		queue = 8
		total = 30
	)

	blocked := &recordingNotifier{gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	healthy := &recordingNotifier{got: make(chan notifier.DiffCache, total)}

	notes, changes := startNotifications(t, &events.NotificationsConfig{QueueSize: queue})

	_, err := notes.Apply(
		context.Background(),
		[]events.NotifierDef{ndef("blocked", "v1", blocked), ndef("healthy", "v1", healthy)},
	)
	require.NoError(t, err)

	changes <- change("set", 0)

	await(t, blocked.entered, "the blocked notifier to take its first change")

	for i := 1; i < total; i++ {
		// the unbuffered channel is read at once: a stalled consumer fails the test by timeout
		changes <- change("set", i)

		await(t, healthy.got, "the healthy notifier")
	}

	await(t, healthy.got, "the healthy notifier, first change")

	status := notes.Status()
	require.Len(t, status.Notifiers, 2)

	byName := map[string]events.NotifierStatus{}
	for _, n := range status.Notifiers {
		byName[n.Name] = n
	}

	assert.Equal(t, uint64(total), byName["healthy"].Delivered)
	assert.Zero(t, byName["healthy"].Dropped)
	assert.Equal(t, queue, byName["blocked"].Queued)
	assert.Equal(t, uint64(total-1-queue), byName["blocked"].Dropped, "what the bounded queue could not hold")

	close(blocked.gate)
}

func TestNotifications_PanicIsRecoveredLoggedOnceAndCounted(t *testing.T) {
	t.Parallel()

	rec, lines := newLogRecorder()
	panicky := &recordingNotifier{panicOn: "1", got: make(chan notifier.DiffCache, 4)}

	notes, changes := startNotifications(t, &events.NotificationsConfig{Logger: slog.New(rec)})

	_, err := notes.Apply(context.Background(), []events.NotifierDef{ndef("p", "v1", panicky)})
	require.NoError(t, err)

	changes <- change("set", 0)

	changes <- change("set", 1) // panics

	changes <- change("set", 2)

	await(t, panicky.got, "change 0")
	await(t, panicky.got, "change 2")

	require.NoError(t, notes.Shutdown(bounded(t)))

	status := notes.Status().Notifiers[0]
	assert.Equal(t, uint64(1), status.Panics)
	assert.Equal(t, uint64(2), status.Delivered, "the changes around the panic were delivered")

	var panics []string

	for _, line := range lines() {
		if strings.Contains(line, "panicked") {
			panics = append(panics, line)
		}
	}

	require.Len(t, panics, 1, "logged once")
	assert.Contains(t, panics[0], "notifier=p")
	assert.Contains(t, panics[0], "boom")
}

func TestNotifications_ErrorsAreCountedAndLoggedWithoutTheMessage(t *testing.T) {
	t.Parallel()

	rec, lines := newLogRecorder()
	failing := &recordingNotifier{
		err: fmt.Errorf("post https://hooks.example/CANARY-WEBHOOK-TOKEN: %w", errRemoteDown),
		got: make(chan notifier.DiffCache, 2),
	}

	notes, changes := startNotifications(t, &events.NotificationsConfig{Logger: slog.New(rec)})

	_, err := notes.Apply(context.Background(), []events.NotifierDef{ndef("f", "v1", failing)})
	require.NoError(t, err)

	changes <- change("set", 0)

	await(t, failing.got, "the change")
	require.NoError(t, notes.Shutdown(bounded(t)))

	status := notes.Status().Notifiers[0]
	assert.Equal(t, uint64(1), status.Errors)
	assert.Zero(t, status.Delivered)
	assert.Equal(t, events.ErrorOther, status.LastError)

	joined := strings.Join(lines(), "\n")
	assert.Contains(t, joined, "the notifier failed")
	assert.NotContains(t, joined, "CANARY-WEBHOOK-TOKEN", "the message of a notifier error can carry its URL")
}

func TestNotifications_BroadcastHook(t *testing.T) {
	t.Parallel()

	tests := []struct {
		hook       func(got chan<- flagruntime.Change) func(flagruntime.Change)
		name       string
		wantLogged string
		changes    []flagruntime.Change
		wantFlags  []string
	}{
		{
			name:    "every change with a diff, none without",
			changes: []flagruntime.Change{change("a", 1), {FlagSet: "empty"}, change("b", 2)},
			hook: func(got chan<- flagruntime.Change) func(flagruntime.Change) {
				return func(c flagruntime.Change) { got <- c }
			},
			wantFlags: []string{"a", "b"},
		},
		{
			name:    "a panic of the hook is recovered and the next change is delivered",
			changes: []flagruntime.Change{change("boom", 1), change("b", 2)},
			hook: func(got chan<- flagruntime.Change) func(flagruntime.Change) {
				return func(c flagruntime.Change) {
					if c.FlagSet == "boom" {
						panic("hook")
					}

					got <- c
				}
			},
			wantFlags:  []string{"b"},
			wantLogged: "the broadcast hook panicked",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec, lines := newLogRecorder()
			got := make(chan flagruntime.Change, 4)
			notes, changes := startNotifications(
				t,
				&events.NotificationsConfig{Logger: slog.New(rec), Broadcast: tt.hook(got)},
			)

			for _, c := range tt.changes {
				changes <- c
			}

			var flagSets []string
			for range tt.wantFlags {
				flagSets = append(flagSets, await(t, got, "the hook").FlagSet)
			}

			require.NoError(t, notes.Shutdown(bounded(t)))
			assert.Equal(t, tt.wantFlags, flagSets)
			assert.Empty(t, got, "no other change reached the hook")

			if tt.wantLogged != "" {
				assert.Contains(t, strings.Join(lines(), "\n"), tt.wantLogged)
			}
		})
	}
}

func TestNotifications_Reload(t *testing.T) {
	t.Parallel()

	keep := &recordingNotifier{got: make(chan notifier.DiffCache, 8)}
	oldB := &recordingNotifier{got: make(chan notifier.DiffCache, 8)}
	newB := &recordingNotifier{got: make(chan notifier.DiffCache, 8)}
	gone := &recordingNotifier{got: make(chan notifier.DiffCache, 8)}

	notes, changes := startNotifications(t, &events.NotificationsConfig{})

	_, err := notes.Apply(context.Background(), []events.NotifierDef{
		ndef("keep", "v1", keep), ndef("b", "v1", oldB), ndef("gone", "v1", gone),
	})
	require.NoError(t, err)

	changes <- change("set", 1)

	for _, n := range []*recordingNotifier{keep, oldB, gone} {
		await(t, n.got, "the first change")
	}

	set, err := notes.Apply(context.Background(), []events.NotifierDef{
		ndef("keep", "v1", &recordingNotifier{}), ndef("b", "v2", newB), ndef("added", "v1", &recordingNotifier{}),
	})
	require.NoError(t, err)
	assert.Equal(
		t,
		events.ReloadSet{
			Kept:     []string{"keep"},
			Replaced: []string{"b"},
			Added:    []string{"added"},
			Removed:  []string{"gone"},
		},
		set,
	)

	changes <- change("set", 2)

	await(t, keep.got, "the second change")
	await(t, newB.got, "the second change")

	assert.Len(t, keep.received(), 2, "the kept notifier is the same one")
	assert.Len(t, oldB.received(), 1, "the replaced notifier got nothing after the reload")
	assert.Len(t, gone.received(), 1, "the removed notifier got nothing after the reload")
	assert.Len(t, newB.received(), 1)

	states := map[string]string{}
	for _, n := range notes.Status().Notifiers {
		states[n.Name+"/"+n.State] = n.Kind
	}

	assert.Contains(t, states, "gone/"+events.StateStopped)
	assert.Contains(t, states, "keep/"+events.StateRunning)
}

func TestNotifications_ReloadDrainPastDeadline(t *testing.T) {
	t.Parallel()

	stuck := &recordingNotifier{gate: make(chan struct{}), entered: make(chan struct{}, 1)}

	notes, changes := startNotifications(t, &events.NotificationsConfig{QueueSize: 8})

	_, err := notes.Apply(context.Background(), []events.NotifierDef{ndef("stuck", "v1", stuck)})
	require.NoError(t, err)

	for i := range 4 {
		changes <- change("set", i)
	}

	await(t, stuck.entered, "the notifier to take a change")

	set, err := notes.Apply(canceled(), nil)
	require.NoError(t, err)
	assert.Equal(t, events.ReloadSet{Removed: []string{"stuck"}, Cut: []string{"stuck"}}, set)

	close(stuck.gate)

	status := notes.Status().Notifiers[0]
	assert.Equal(t, events.StateCut, status.State)
}

func TestNotifications_Lifecycle(t *testing.T) {
	t.Parallel()

	changes := make(chan flagruntime.Change)
	notes := events.NewNotifications(&events.NotificationsConfig{})

	rec := &recordingNotifier{got: make(chan notifier.DiffCache, 4)}

	// the goroutines are started under the label: the consumer by Start, the one of the notifier by Apply
	label := labeledRun(t, func(ctx context.Context) {
		require.NoError(t, notes.Start(ctx, changes))

		_, err := notes.Apply(ctx, []events.NotifierDef{ndef("a", "v1", rec)})
		require.NoError(t, err)
	})

	require.ErrorIs(t, notes.Start(context.Background(), changes), events.ErrAlreadyStarted)

	changes <- change("set", 1)

	await(t, rec.got, "the change")
	assert.Equal(t, 2, labelled(label), "the consumer and the goroutine of the notifier")

	require.NoError(t, notes.Shutdown(bounded(t)))
	require.NoError(t, notes.Shutdown(bounded(t)), "the second call does nothing")
	<-notes.Done()
	assert.Zero(t, labelled(label), "no goroutine is left")

	require.ErrorIs(t, notes.Start(context.Background(), changes), events.ErrClosed)

	_, err := notes.Apply(context.Background(), nil)
	require.ErrorIs(t, err, events.ErrClosed)
}

func TestNotifications_ClosedChannelAndContextStopTheConsumer(t *testing.T) {
	t.Parallel()

	t.Run("channel closed", func(t *testing.T) {
		t.Parallel()

		var notes *events.Notifications

		changes := make(chan flagruntime.Change)

		label := labeledRun(t, func(ctx context.Context) {
			notes = events.NewNotifications(&events.NotificationsConfig{})
			require.NoError(t, notes.Start(ctx, changes))
		})

		close(changes)
		require.NoError(t, notes.Shutdown(bounded(t)))
		assert.Zero(t, labelled(label))
	})

	t.Run("context done", func(t *testing.T) {
		t.Parallel()

		var (
			notes  *events.Notifications
			cancel context.CancelFunc
		)

		label := labeledRun(t, func(ctx context.Context) {
			ctx, cancel = context.WithCancel(ctx)
			notes = events.NewNotifications(&events.NotificationsConfig{})
			require.NoError(t, notes.Start(ctx, make(chan flagruntime.Change)))
		})

		cancel()
		require.NoError(t, notes.Shutdown(bounded(t)))
		assert.Zero(t, labelled(label))
	})
}

func TestNotifications_ShutdownDeliversWhatIsQueued(t *testing.T) {
	t.Parallel()

	rec := &recordingNotifier{gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	notes, changes := startNotifications(t, &events.NotificationsConfig{QueueSize: 16})

	_, err := notes.Apply(context.Background(), []events.NotifierDef{ndef("a", "v1", rec)})
	require.NoError(t, err)

	for i := range 5 {
		changes <- change("set", i)
	}

	await(t, rec.entered, "the notifier to take a change")

	done := make(chan error, 1)

	go func() { done <- notes.Shutdown(bounded(t)) }()

	close(rec.gate)
	require.NoError(t, await(t, done, "the shutdown"))

	assert.Len(t, rec.received(), 5, "the queued changes were delivered before the notifier stopped")
	assert.Equal(t, events.StateStopped, notes.Status().Notifiers[0].State)
}
