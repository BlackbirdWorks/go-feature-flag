//go:build bench

package stream_test

import (
	"fmt"
	"testing"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/notifier"
)

// BenchmarkWebsocketBroadcast is the cost of one broadcast to 10,000 connected clients of a flag set. A client that
// connected with a key is registered like the others: revoking keys adds nothing to the path of a message.
func BenchmarkWebsocketBroadcast(b *testing.B) {
	svc := stream.NewWebsocketService()

	for i := range 10000 {
		registerActive(b, svc, &mockConn{}, "set", fmt.Sprintf("key-%d", i))
	}

	diff := notifier.DiffCache{}

	b.ReportAllocs()

	for b.Loop() {
		svc.BroadcastFlagChanges("set", diff)
	}
}
