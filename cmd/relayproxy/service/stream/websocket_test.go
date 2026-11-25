package stream_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

var (
	errErrorWebsocketConnection = errors.New("error websocket connection")
)

type mockConn struct {
	writeJSONFunc func(v any) error
	throwError    bool
}

func (m *mockConn) WriteJSON(val any) error {
	if m.throwError {
		return errErrorWebsocketConnection
	}

	if m.writeJSONFunc != nil {
		return m.writeJSONFunc(val)
	}

	return nil
}

func sensitiveDiffCache() notifier.DiffCache {
	beforeQuery := `user.email eq "before@example.com"`
	afterQuery := `user.email eq "after@example.com"`
	enabled := fixtures.VariationEnabled

	return notifier.DiffCache{
		Deleted: map[string]flag.Flag{
			"deleted-flag": &flag.InternalFlag{
				Variations: &map[string]*any{fixtures.VariationEnabled: new(any(true))},
				Rules: &[]flag.Rule{
					{Query: &beforeQuery, VariationResult: &enabled},
				},
			},
		},
		Added: map[string]flag.Flag{
			"added-flag": &flag.InternalFlag{
				Variations: &map[string]*any{fixtures.VariationEnabled: new(any(true))},
				Rules: &[]flag.Rule{
					{Query: &afterQuery, VariationResult: &enabled},
				},
			},
		},
		Updated: map[string]notifier.DiffUpdated{
			"updated-flag": {
				Before: &flag.InternalFlag{Rules: &[]flag.Rule{{Query: &beforeQuery}}},
				After:  &flag.InternalFlag{Rules: &[]flag.Rule{{Query: &afterQuery}}},
			},
		},
	}
}

func TestBroadcastFlagChangesPayload(t *testing.T) {
	t.Parallel()

	diff := sensitiveDiffCache()
	tests := []struct {
		expected any
		name     string
		options  []stream.Option
	}{
		{
			name:     "includes flag details by default",
			expected: diff,
		},
		{
			name:    "omits flag details when disabled",
			options: []stream.Option{stream.WithFlagDetails(false)},
			expected: map[string]map[string]struct{}{
				"deleted": {"deleted-flag": {}},
				"added":   {"added-flag": {}},
				"updated": {"updated-flag": {}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			websocketService := stream.NewWebsocketService(tt.options...)
			conn := &mockConn{}

			var received any

			conn.writeJSONFunc = func(v any) error {
				received = v

				return nil
			}
			websocketService.Register(conn, fixtures.FlagsetDefault)

			websocketService.BroadcastFlagChanges(fixtures.FlagsetDefault, diff)

			actualJSON, err := json.Marshal(received)
			require.NoError(t, err)
			expectedJSON, err := json.Marshal(tt.expected)
			require.NoError(t, err)
			assert.JSONEq(t, string(expectedJSON), string(actualJSON))
		})
	}
}

func TestBroadcastFlagChanges(t *testing.T) {
	t.Parallel()

	// Create the websocketService instance
	websocketService := stream.NewWebsocketService()

	// Prepare the input data
	diff := notifier.DiffCache{} // You need to define an appropriate DiffCache

	// Create mock connections
	conn1 := &mockConn{}
	conn2 := &mockConn{}

	// Register the mock connections
	websocketService.Register(conn1, fixtures.FlagsetDefault)
	websocketService.Register(conn2, fixtures.FlagsetDefault)

	// Set up a flag to track if the WriteJSON function is called on the connections
	conn1WriteJSONCalled := false
	conn2WriteJSONCalled := false

	// Set the function to be executed when WriteJSON is called on the connections
	conn1.writeJSONFunc = func(_ any) error {
		conn1WriteJSONCalled = true

		return nil
	}

	conn2.writeJSONFunc = func(_ any) error {
		conn2WriteJSONCalled = true

		return nil
	}

	// Call the BroadcastFlagChanges function
	websocketService.BroadcastFlagChanges(fixtures.FlagsetDefault, diff)

	// BroadcastFlagChanges writes synchronously: the connections have been written when it returns

	// Assertions
	assert.True(t, conn1WriteJSONCalled, "WriteJSON should be called on conn1")
	assert.True(t, conn2WriteJSONCalled, "WriteJSON should be called on conn2")
}

func TestBroadcastFlagChangesPerFlagset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want             map[string]bool
		name             string
		broadcastFlagset string
	}{
		{
			name:             "broadcast on flagset-a only reaches flagset-a clients",
			broadcastFlagset: fixtures.FlagsetLower,
			want:             map[string]bool{"a1": true, "a2": true, "b1": false},
		},
		{
			name:             "broadcast on flagset-b only reaches flagset-b clients",
			broadcastFlagset: "flagset-b",
			want:             map[string]bool{"a1": false, "a2": false, "b1": true},
		},
		{
			name:             "broadcast on a flagset without client reaches nobody",
			broadcastFlagset: "flagset-c",
			want:             map[string]bool{"a1": false, "a2": false, "b1": false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			websocketService := stream.NewWebsocketService()
			clients := map[string]string{"a1": fixtures.FlagsetLower, "a2": fixtures.FlagsetLower, "b1": "flagset-b"}

			got := map[string]bool{}
			for name, flagsetName := range clients {
				got[name] = false

				websocketService.Register(&mockConn{writeJSONFunc: func(_ any) error {
					got[name] = true

					return nil
				}}, flagsetName)
			}

			websocketService.BroadcastFlagChanges(tt.broadcastFlagset, notifier.DiffCache{})

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDeregister(t *testing.T) {
	t.Parallel()

	// Create the websocketService instance
	websocketService := stream.NewWebsocketService()

	// Create a mock connection
	conn := &mockConn{}

	// Set the function to be executed when WriteJSON is called on the connections
	conn1WriteJSONCalled := false
	conn.writeJSONFunc = func(_ any) error {
		conn1WriteJSONCalled = true

		return nil
	}

	// Register the mock connection
	websocketService.Register(conn, fixtures.FlagsetDefault)

	// Call the Deregister function
	websocketService.Deregister(conn)

	// Call the BroadcastFlagChanges function after deregistering the connection
	diff := notifier.DiffCache{} // You need to define an appropriate DiffCache
	websocketService.BroadcastFlagChanges(fixtures.FlagsetDefault, diff)

	// BroadcastFlagChanges writes synchronously: the connections have been written when it returns

	// Assertions
	assert.False(t, conn1WriteJSONCalled, "WriteJSON should not be called after deregistering")
}

func TestClose(t *testing.T) {
	t.Parallel()

	// Create the websocketService instance
	websocketService := stream.NewWebsocketService()

	// Create mock connections
	conn1 := &mockConn{}
	conn2 := &mockConn{}

	// Set up a flag to track if the WriteJSON function is called on the connections
	conn1WriteJSONCalled := false
	conn2WriteJSONCalled := false

	// Set the function to be executed when WriteJSON is called on the connections
	conn1.writeJSONFunc = func(_ any) error {
		conn1WriteJSONCalled = true

		return nil
	}

	conn2.writeJSONFunc = func(_ any) error {
		conn2WriteJSONCalled = true

		return nil
	}

	// Register the mock connections
	websocketService.Register(conn1, fixtures.FlagsetDefault)
	websocketService.Register(conn2, fixtures.FlagsetDefault)

	// Call the Close function
	websocketService.Close()

	// Call the BroadcastFlagChanges function after closing the connections
	diff := notifier.DiffCache{} // You need to define an appropriate DiffCache
	websocketService.BroadcastFlagChanges(fixtures.FlagsetDefault, diff)

	// BroadcastFlagChanges writes synchronously: the connections have been written when it returns

	// Assertions
	assert.False(t, conn1WriteJSONCalled, "WriteJSON should not be called after closing")
	assert.False(t, conn2WriteJSONCalled, "WriteJSON should not be called after closing")
}

func TestBroadcastFlagChangesDeadLock(t *testing.T) {
	t.Parallel()

	// Create the websocketService instance
	websocketService := stream.NewWebsocketService()
	diff := notifier.DiffCache{} // You need to define an appropriate DiffCache
	conn1 := &mockConn{}

	// the mock will return an error when WriteJSON is called
	// this will trigger the deregister of the connection, and the BroadcastFlagChanges will try to lock the mutex
	// in the past we had a deadlock here (see: https://github.com/thomaspoignant/go-feature-flag/issues/3144)
	conn2 := &mockConn{throwError: true}

	websocketService.Register(conn1, fixtures.FlagsetDefault)
	websocketService.Register(conn2, fixtures.FlagsetDefault)

	conn1WriteJSONCalled := false
	conn1.writeJSONFunc = func(_ any) error {
		conn1WriteJSONCalled = true

		return nil
	}

	websocketService.BroadcastFlagChanges(fixtures.FlagsetDefault, diff)
	assert.True(t, conn1WriteJSONCalled, "WriteJSON should be called on conn1")

	// We are not testing the error here, we are testing that the function does not deadlock
}
