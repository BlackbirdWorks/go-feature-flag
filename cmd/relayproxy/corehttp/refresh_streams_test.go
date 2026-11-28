package corehttp_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/notifier"
)

var errRefreshBoom = errors.New("boom")

// refreshSource is a source of the adapter whose refresh answers what the test says.
type refreshSource struct {
	err    error
	holder *flagruntime.Holder
	result relaycore.RefreshResult
}

func (s *refreshSource) Holder() *flagruntime.Holder     { return s.holder }
func (*refreshSource) Evaluator() *flagruntime.Evaluator { return nil }
func (*refreshSource) Status() relaycore.Status          { return relaycore.Status{} }
func (*refreshSource) Collect(events.Event) bool         { return false }
func (*refreshSource) DropNotPublic(int)                 {}
func (s *refreshSource) Refresh(context.Context) (relaycore.RefreshResult, error) {
	return s.result, s.err
}

// The adapter answers a refresh with what the poll found and what the engine decided, and tells a relay that does not
// poll from any other failure.
func TestAdapter_Refresh(t *testing.T) {
	t.Parallel()

	rejected := flagruntime.Reload{
		Outcome:    flagruntime.OutcomeRejected,
		Code:       flagruntime.CodeBuildFailed,
		Generation: 5,
	}
	applied := flagruntime.Reload{Outcome: flagruntime.OutcomeApplied, Generation: 4}

	tests := []struct {
		wantErr error
		name    string
		want    service.RefreshOutcome
		source  refreshSource
	}{
		{
			name: "applied",
			source: refreshSource{result: relaycore.RefreshResult{
				Poll: manifestreader.RefreshResult{
					Poll: manifestreader.PollStatus{Result: manifestreader.PollUpdated}, Generation: 4, Changed: true,
				},
				Engine: applied, Running: 4,
			}},
			want: service.RefreshOutcome{
				Result: manifestreader.PollUpdated, Generation: 4, Changed: true, Applied: true, AppliedGeneration: 4,
			},
		},
		{
			name: "rejected by the engine",
			source: refreshSource{result: relaycore.RefreshResult{
				Poll: manifestreader.RefreshResult{
					Poll: manifestreader.PollStatus{Result: manifestreader.PollUpdated}, Generation: 5, Changed: true,
				},
				Engine: rejected, Running: 4,
			}},
			want: service.RefreshOutcome{
				Result: manifestreader.PollUpdated, Generation: 5, Changed: true, Applied: false, AppliedGeneration: 4,
				Rejection: flagruntime.CodeBuildFailed,
			},
		},
		{
			name:    "the reader does not poll",
			source:  refreshSource{err: manifestreader.ErrNotPolling},
			wantErr: service.ErrNotPolling,
		},
		{name: "another failure", source: refreshSource{err: errRefreshBoom}, wantErr: errRefreshBoom},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := tt.source
			source.holder = flagruntime.NewHolder()

			got, err := corehttp.NewAdapter(&source).Refresh(t.Context())
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				if errors.Is(tt.wantErr, errRefreshBoom) {
					assert.NotErrorIs(t, err, service.ErrNotPolling)
				}

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func publicRuntime(present bool) *flagruntime.Runtime {
	rt := &flagruntime.Runtime{Status: &flagruntime.Status{}}
	if present {
		rt.Public = &flagruntime.FlagSet{Name: flagruntime.PublicName}
	}

	return rt
}

// Revalidate closes the streams of the clients without a key when the runtime has no public flags, and keeps them while
// it has. The clients with a key stay, and the counts say who is connected.
func TestStreams_Revalidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		runtime       *flagruntime.Runtime
		name          string
		wantWebsocket int
	}{
		{name: "no public flags", runtime: publicRuntime(false), wantWebsocket: 1},
		{name: "public flags", runtime: publicRuntime(true), wantWebsocket: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			websockets, sse := stream.NewWebsocketService(), stream.NewSSEService()
			streams := corehttp.NewStreamsWith(
				&corehttp.StreamsConfig{Websocket: websockets, SSE: sse, Label: t.Name()},
			)

			t.Cleanup(func() { _ = streams.Close(context.Background()) })

			websockets.RegisterAnonymous(&noopConn{}, flagruntime.PublicName)
			websockets.Register(&noopConn{}, "a")

			require.Equal(t, corehttp.ClientCounts{Websocket: 2, SSE: 0}, streams.Connected())

			streams.Revalidate(tt.runtime)

			assert.Equal(t, corehttp.ClientCounts{Websocket: tt.wantWebsocket, SSE: 0}, streams.Connected())
		})
	}
}

// noopConn is a websocket connection that takes everything.
type noopConn struct{ _ byte }

func (*noopConn) WriteJSON(any) error { return nil }
func (*noopConn) Close() error        { return nil }

// gatedWebsocket is a websocket service whose delivery waits for the test: it records the flag sets it was asked to
// broadcast, the way a service does when its clients do not read.
type gatedWebsocket struct {
	stream.WebsocketService

	entered chan string
	release chan struct{}
	sent    []string
	mu      sync.Mutex
}

func (g *gatedWebsocket) BroadcastFlagChanges(flagset string, diff notifier.DiffCache) {
	g.entered <- flagset

	<-g.release

	g.mu.Lock()
	g.sent = append(g.sent, flagset+":"+strings.Join(slices.Sorted(maps.Keys(diff.Added)), ","))
	g.mu.Unlock()
}

func (*gatedWebsocket) DisconnectAnonymous(string) {}
func (*gatedWebsocket) Close()                     {}

func (g *gatedWebsocket) delivered() []string {
	g.mu.Lock()
	defer g.mu.Unlock()

	return slices.Clone(g.sent)
}

func publicChange(generation uint64, flag string) flagruntime.Change {
	return flagruntime.Change{
		FlagSet: flagruntime.PublicName, Generation: generation,
		Diff: notifier.DiffCache{Added: map[string]flagdef.Flag{flag: &flagdef.InternalFlag{}}},
	}
}

// What waits for the clients without a key when their access is withdrawn is dropped with them: a client that does not
// read leaves a public diff queued, the public flags are withdrawn and come back, and nothing queued before the
// withdrawal is delivered to the clients of the new public flags.
func TestStreams_QueuedPublicChangesDieWithTheWithdrawal(t *testing.T) {
	t.Parallel()

	gated := &gatedWebsocket{entered: make(chan string, 8), release: make(chan struct{})}
	streams := corehttp.NewStreamsWith(&corehttp.StreamsConfig{
		Websocket: gated, SSE: stream.NewSSEService(), Label: t.Name(),
	})

	t.Cleanup(func() {
		close(gated.release)

		_ = streams.Close(context.Background())
	})

	other := flagruntime.Change{
		FlagSet: "other", Generation: 1,
		Diff: notifier.DiffCache{Added: map[string]flagdef.Flag{"g": &flagdef.InternalFlag{}}},
	}

	// the worker takes the change of another flag set and waits on its client; the public change queues behind it
	streams.Broadcast(other)
	assert.Equal(t, "other", gated.awaitEntered(t), "the worker is held on the change of another flag set")

	streams.Broadcast(publicChange(2, "old"))

	// the public flags are withdrawn: the queued change goes, and so does the deletion that follows
	streams.Revalidate(publicRuntime(false))
	streams.Broadcast(publicChange(3, "dropped"))

	// they come back with another flag set: the new change is delivered, the old one never is
	streams.Revalidate(publicRuntime(true))
	streams.Broadcast(publicChange(4, "new"))

	gated.releaseOne(t) // the delivery of the other flag set ends

	assert.Equal(t, flagruntime.PublicName, gated.awaitEntered(t), "the change of the new public flags")

	gated.releaseOne(t)

	require.Eventually(t, func() bool { return len(gated.delivered()) == 2 }, 5*time.Second, time.Millisecond)
	assert.Equal(t, []string{"other:g", flagruntime.PublicName + ":new"}, gated.delivered(),
		"one public change reached the clients, with the flag of the new public flags only")
}

// awaitEntered waits for the worker to enter the next delivery and returns the flag set of it.
func (g *gatedWebsocket) awaitEntered(t *testing.T) string {
	t.Helper()

	select {
	case flagset := <-g.entered:
		return flagset
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the worker did not enter a delivery")
	}

	return ""
}

// releaseOne ends the delivery the worker is held in.
func (g *gatedWebsocket) releaseOne(t *testing.T) {
	t.Helper()

	select {
	case g.release <- struct{}{}:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "no delivery was waiting to end")
	}
}
