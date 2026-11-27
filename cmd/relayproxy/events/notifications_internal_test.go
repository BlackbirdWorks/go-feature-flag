package events

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/notifier"
)

// gatedNotifier records the keys of the diffs it gets, after an optional gate.
type gatedNotifier struct {
	gate    chan struct{}
	entered chan struct{}
	got     chan struct{}
	log     *orderLog
	label   string
}

type orderLog struct {
	order []string
	mu    sync.Mutex
}

func (o *orderLog) add(entry string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.order = append(o.order, entry)
}

func (g *gatedNotifier) Notify(diff notifier.DiffCache) error {
	if g.entered != nil {
		select {
		case g.entered <- struct{}{}:
		default:
		}
	}

	if g.gate != nil {
		<-g.gate
	}

	for key := range diff.Added {
		g.log.add(g.label + key)
	}

	if g.got != nil {
		g.got <- struct{}{}
	}

	return nil
}

func addedChange(key string) flagruntime.Change {
	return flagruntime.Change{FlagSet: "set", Diff: notifier.DiffCache{Added: map[string]flagdef.Flag{key: nil}}}
}

func await(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(20 * time.Second):
		require.FailNow(t, "timed out waiting for "+what)
	}
}

// TestNotifications_ReloadKeepsTheOrderOfAFlagSet replaces a notifier while changes are queued for it: the new
// instance calls its notifier only after the old one delivered what it held, so the changes of the flag set keep
// their order across the swap. The test waits for the swap to be published before it sends the next change.
func TestNotifications_ReloadKeepsTheOrderOfAFlagSet(t *testing.T) {
	t.Parallel()

	log := &orderLog{}
	oldN := &gatedNotifier{gate: newGate(t), entered: make(chan struct{}, 1), label: "old-", log: log}
	newN := &gatedNotifier{got: make(chan struct{}, 8), label: "new-", log: log}

	notes := NewNotifications(&NotificationsConfig{QueueSize: 8})
	changes := make(chan flagruntime.Change)

	require.NoError(t, notes.Start(context.Background(), changes))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		_ = notes.Shutdown(ctx)
	})

	_, err := notes.Apply(context.Background(), []NotifierDef{NewNotifierDef("n", "webhook", "v1", oldN)})
	require.NoError(t, err)

	changes <- addedChange("0")

	await(t, oldN.entered, "the old notifier to take change 0")

	changes <- addedChange("1")

	// change 1 is queued behind the one the old notifier holds: the consumer takes the next change only after
	// it dispatched this one, so the fence below proves it
	changes <- addedChange("fence")

	applied := make(chan struct{})

	go func() {
		defer close(applied)

		_, applyErr := notes.Apply(context.Background(), []NotifierDef{NewNotifierDef("n", "webhook", "v2", newN)})
		assert.NoError(t, applyErr)
	}()

	// the swap is published when the replaced instance is in the retired set
	deadline := time.After(20 * time.Second)

	for len(notes.state.Load().retired) == 0 {
		select {
		case <-deadline:
			require.FailNow(t, "timed out waiting for the swap")
		default:
			runtime.Gosched()
		}
	}

	changes <- addedChange("2")

	close(oldN.gate)
	await(t, applied, "the reload")
	await(t, newN.got, "the new notifier to get change 2")

	log.mu.Lock()
	defer log.mu.Unlock()

	assert.Equal(t, []string{"old-0", "old-1", "old-fence", "new-2"}, log.order,
		"the changes in order, across the reload: the old instance drains before the new one is called")
}

// TestNotificationsWaitDrainsSeesTheDrainOfAShutdown holds a notifier in its drain while the notifications shut
// down, and calls WaitDrains meanwhile: it waits for that drain, and returns the error of its context when the
// context ends first.
func TestNotificationsWaitDrainsSeesTheDrainOfAShutdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ctx          func() context.Context
		beforeResult func(release func())
		wantErr      error
		name         string
		wantOrder    []string
	}{
		{
			name: "the drain ends first", ctx: context.Background,
			beforeResult: func(release func()) { release() },
			wantErr:      nil, wantOrder: []string{"gate-released", "wait-returned"},
		},
		{
			name: "the context ends first",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()

				return ctx
			},
			beforeResult: func(func()) {},
			wantErr:      context.Canceled, wantOrder: []string{"wait-returned", "gate-released"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			log := &orderLog{}
			stuck := &gatedNotifier{gate: newGate(t), entered: make(chan struct{}, 1), label: "n-", log: &orderLog{}}

			notes := NewNotifications(&NotificationsConfig{})
			changes := make(chan flagruntime.Change)

			require.NoError(t, notes.Start(context.Background(), changes))

			var once sync.Once

			release := func() {
				once.Do(func() {
					log.add("gate-released")
					close(stuck.gate)
				})
			}

			// registered before anything can fail: the notifier is released and the notifications are stopped
			t.Cleanup(func() {
				release()

				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()

				_ = notes.Shutdown(ctx)
			})

			_, err := notes.Apply(context.Background(), []NotifierDef{NewNotifierDef("n", "webhook", "v1", stuck)})
			require.NoError(t, err)

			changes <- addedChange("0")

			await(t, stuck.entered, "the notifier to take its change")

			go func() { _ = notes.Shutdown(context.Background()) }()

			// the shutdown published the notifier as draining
			waitUntil(
				t,
				func() bool { return len(notes.state.Load().retired) > 0 },
				"the shutdown to publish its drains",
			)

			result := make(chan error, 1)
			waitCtx := tt.ctx()

			go func() {
				waitErr := notes.WaitDrains(waitCtx)

				log.add("wait-returned")

				result <- waitErr
			}()

			tt.beforeResult(release)

			select {
			case got := <-result:
				require.ErrorIs(t, got, tt.wantErr)
			case <-time.After(20 * time.Second):
				require.FailNow(t, "timed out waiting for WaitDrains")
			}

			release()

			log.mu.Lock()
			defer log.mu.Unlock()

			assert.Equal(t, tt.wantOrder, log.order)
		})
	}
}

// waitUntil waits for a condition on the internal state that has no channel to wait on: it yields the processor
// between two checks, and the wait is bounded by a failure timeout.
func waitUntil(t *testing.T, cond func() bool, what string) {
	t.Helper()

	deadline := time.After(20 * time.Second)

	for !cond() {
		select {
		case <-deadline:
			require.FailNow(t, "timed out waiting for "+what)
		default:
			runtime.Gosched()
		}
	}
}
