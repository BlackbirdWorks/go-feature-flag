package corehttp_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
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
		source  refreshSource
		wantErr error
		name    string
		want    service.RefreshOutcome
	}{
		{
			name: "applied",
			source: refreshSource{result: relaycore.RefreshResult{
				Poll: manifestreader.RefreshResult{
					Poll: manifestreader.PollStatus{Result: manifestreader.PollUpdated}, Generation: 4, Changed: true,
				},
				Engine: applied,
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
				Engine: rejected,
			}},
			want: service.RefreshOutcome{
				Result: manifestreader.PollUpdated, Generation: 5, Changed: true, Applied: false, AppliedGeneration: 5,
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

func anonymousRuntime(name string, state flagruntime.SetState) *flagruntime.Runtime {
	status := &flagruntime.Status{}
	if name != "" {
		status.Anonymous = &flagruntime.AnonymousStatus{FlagSet: name, State: state}
	}

	return &flagruntime.Runtime{Status: status}
}

// Revalidate closes the streams of the clients without a key that the runtime does not entitle: all of them when it
// names no anonymous flag set or its flag set is not held, the ones of another flag set otherwise. The clients with a
// key stay, and the counts say who is connected.
func TestStreams_Revalidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		runtime       *flagruntime.Runtime
		name          string
		wantWebsocket int
	}{
		{name: "no anonymous flag set", runtime: anonymousRuntime("", ""), wantWebsocket: 1},
		{name: "the flag set is held", runtime: anonymousRuntime("a", flagruntime.StateServing), wantWebsocket: 2},
		{name: "another flag set", runtime: anonymousRuntime("b", flagruntime.StateServing), wantWebsocket: 1},
		{name: "the flag set is not held", runtime: anonymousRuntime("a", flagruntime.StateMissing), wantWebsocket: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			websockets, sse := stream.NewWebsocketService(), stream.NewSSEService()
			streams := corehttp.NewStreamsWith(
				&corehttp.StreamsConfig{Websocket: websockets, SSE: sse, Label: t.Name()},
			)

			t.Cleanup(func() { _ = streams.Close(context.Background()) })

			websockets.RegisterAnonymous(&noopConn{}, "a")
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
