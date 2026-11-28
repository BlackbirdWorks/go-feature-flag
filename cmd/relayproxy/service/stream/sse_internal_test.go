package stream

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// deadlineResponse counts the write deadlines set on a response, the state the server clears when a request ends.
type deadlineResponse struct {
	http.ResponseWriter

	deadlines atomic.Int64
}

//nolint:unparam // the SetWriteDeadline of a response
func (d *deadlineResponse) SetWriteDeadline(time.Time) error {
	d.deadlines.Add(1)

	return nil
}

// A revocation touches the response of a client only while its handler is running: after the handler returned, the
// server has cleared the state of the response (HTTP/2) and a deadline call on it is not safe. The request is
// cancelled in both cases.
func TestSSEDisconnectRevokedLeavesAFinishedResponseAlone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		done          bool
		wantDeadlines int64
	}{
		{name: "the handler is running: the blocked write is ended", done: false, wantDeadlines: 1},
		{name: "the handler returned: the response is not touched", done: true, wantDeadlines: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := NewSSEService()
			t.Cleanup(svc.Close)

			response := &deadlineResponse{}
			cancelled := atomic.Bool{}
			client := &keyedClient{
				cancel: func() { cancelled.Store(true) }, streamID: "set", keyID: svc.hasher.id("key"),
				rc: http.NewResponseController(response), done: tt.done,
			}

			svc.keyed[client] = struct{}{}

			svc.DisconnectRevoked(func(func(string, string) bool) {})

			assert.Equal(t, tt.wantDeadlines, response.deadlines.Load())
			assert.True(t, cancelled.Load(), "the request is cancelled")
			assert.True(t, client.revoked.Load())
		})
	}
}
