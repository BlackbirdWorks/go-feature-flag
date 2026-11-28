package corehttp_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// hungStore is a store that the relay can connect to and that never answers a lookup: it refuses the commands of
// the handshake (a reply of the server, which is all that the probe of the relay needs), and holds every other command,
// so a lookup stays in the store until the relay gives up.
type hungStore struct {
	held    chan struct{}
	address string
}

func newHungStore(tb testing.TB) *hungStore {
	tb.Helper()

	var lc net.ListenConfig

	listener, err := lc.Listen(tb.Context(), "tcp", "127.0.0.1:0")
	require.NoError(tb, err)

	store := &hungStore{held: make(chan struct{}, 64), address: listener.Addr().String()}

	var (
		mu    sync.Mutex
		conns []net.Conn
		wg    sync.WaitGroup
	)

	wg.Go(func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}

			mu.Lock()

			conns = append(conns, conn)

			mu.Unlock()

			wg.Go(func() { store.serve(conn) })
		}
	})

	tb.Cleanup(func() {
		assert.NoError(tb, listener.Close())

		mu.Lock()
		for _, conn := range conns {
			assert.NoError(tb, conn.Close())
		}
		mu.Unlock()

		wg.Wait()
	})

	return store
}

// serve answers the handshake commands of one connection (HELLO, CLIENT and PING) with an error, which is all that the
// relay needs to take the store for reachable, and holds every other command: the lookups.
func (s *hungStore) serve(conn net.Conn) {
	reader := bufio.NewReader(conn)

	for {
		command, err := readCommand(reader)
		if err != nil {
			return
		}

		if !slices.ContainsFunc([]string{"HELLO", "CLIENT", "PING"}, func(name string) bool {
			return strings.EqualFold(command, name)
		}) {
			s.held <- struct{}{}

			continue
		}

		_, err = conn.Write([]byte("-ERR unsupported\r\n"))
		if err != nil {
			return
		}
	}
}

// readCommand reads one command of the protocol of the store and returns its name.
func readCommand(reader *bufio.Reader) (string, error) {
	header, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read a command: %w", err)
	}

	count, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(header, "*")))
	if err != nil {
		return "", fmt.Errorf("read the size of a command: %w", err)
	}

	var name string

	for i := range count {
		sizeLine, readErr := reader.ReadString('\n')
		if readErr != nil {
			return "", fmt.Errorf("read an argument: %w", readErr)
		}

		size, convErr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(sizeLine, "$")))
		if convErr != nil {
			return "", fmt.Errorf("read the size of an argument: %w", convErr)
		}

		arg := make([]byte, size+2)

		_, readErr = io.ReadFull(reader, arg)
		if readErr != nil {
			return "", fmt.Errorf("read an argument: %w", readErr)
		}

		if i == 0 {
			name = string(arg[:size])
		}
	}

	return name, nil
}

// connection is the line of the bootstrap file of a redis store at the address, with timeouts that no test reaches.
func (s *hungStore) connection() string {
	return "  audiences: {type: redis, addresses: [\"" + s.address + "\"], lookupTimeout: 1m, " +
		"timeouts: {connect: 1m, read: 1m, write: 1m}}\n"
}

// A client that disconnects while the store holds its lookup ends the wait of the evaluation, on every endpoint that
// evaluates: the lookup is counted as waiting (the caller left), not as a failure of the store, and the breaker stays
// closed.
func TestE2E_ClientDisconnectEndsTheSegmentLookups(t *testing.T) {
	t.Parallel()

	const flagBody = `{"context":{"targetingKey":"alice"}}`

	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "OFREP single", path: pathOFREPOne + "dyn-flag", body: flagBody},
		{name: "OFREP bulk", path: pathOFREPBulk, body: flagBody},
		{
			name: "OFREP bulk with a flag list", path: pathOFREPBulk,
			body: `{"context":{"targetingKey":"alice","flagList":["dyn-flag"]}}`,
		},
		{
			name: "GOFF eval", path: "/v1/feature/dyn-flag/eval",
			body: `{"evaluationContext":{"key":"alice"},"defaultValue":false}`,
		},
		{name: "GOFF all flags", path: pathAllFlags, body: `{"evaluationContext":{"key":"alice"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newHungStore(t)
			rig := newV2With(t, &v2Options{
				connections: store.connection(), cold: true, extra: "segments:\n  resolver: {callTimeout: 1m}\n",
			})
			rig.flags[dynFlagsObject] = []byte(dynFlagsYAML)
			rig.segments = map[string][]byte{segObjectName: []byte(segDocGood)}
			rig.sets = []controlplane.FlagSet{{
				Name: setName, Sources: []string{flagsObject, dynFlagsObject}, SegmentSources: []string{segObjectName},
				APIKeys: []string{tenantKey},
			}}

			rig.publish(t)
			rig.poll(t)

			// a lookup is held by the store only when the relay has taken the store for reachable
			require.EventuallyWithT(t, func(collect *assert.CollectT) {
				stores := infoOf(t, rig).SegmentStores
				if assert.Len(collect, stores, 1) {
					assert.Equal(collect, "connected", stores[0].State)
				}
			}, waitBound, time.Millisecond)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, http.MethodPost, rig.url+tt.path, strings.NewReader(tt.body))
			require.NoError(t, err)

			req.Header = keyHeader(tenantKey)

			failed := make(chan error, 1)

			go func() {
				resp, doErr := http.DefaultClient.Do(req)
				if doErr == nil {
					doErr = resp.Body.Close()
				}

				failed <- doErr
			}()

			<-store.held // the lookup is in the store, which holds it
			cancel()

			require.ErrorIs(t, <-failed, context.Canceled)

			require.EventuallyWithT(t, func(collect *assert.CollectT) {
				waiting, _ := series(t, rig, "semaphore_segment_unknown_total", "reason", "waiting")
				assert.InDelta(collect, 1, waiting, 0, "the evaluation left with its request")
			}, waitBound, time.Millisecond)

			failedCalls, _ := series(t, rig, "semaphore_segment_store_failed_calls_total", "", "")
			storeUnknown, _ := series(t, rig, "semaphore_segment_unknown_total", "reason", "store")
			open, _ := series(t, rig, "semaphore_segment_breaker_transitions_total", "to", "open")

			assert.InDelta(t, 0, failedCalls, 0, "a client that left is not a failure of the store")
			assert.InDelta(t, 0, storeUnknown, 0)
			assert.InDelta(t, 0, open, 0)
		})
	}
}
