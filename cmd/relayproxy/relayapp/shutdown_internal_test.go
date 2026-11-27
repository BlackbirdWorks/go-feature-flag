package relayapp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"runtime/pprof"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

const (
	drainExtra = "server:\n  shutdown: {drainDelay: 20s}\n"
	// slowBody is the body of the slow request, sent when the test says so.
	slowBody = `{"context":{"targetingKey":"u"}}`
)

// readSignalListener reports the connections that the server reads a second time: the first read took the headers,
// the second is the handler waiting for the body, so the request is in flight.
type readSignalListener struct {
	net.Listener

	reads chan string
}

func (l *readSignalListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, fmt.Errorf("accept: %w", err)
	}

	return &readSignalConn{Conn: conn, reads: l.reads}, nil
}

type readSignalConn struct {
	net.Conn

	reads chan string
	calls atomic.Int32
}

func (c *readSignalConn) Read(buf []byte) (int, error) {
	const bodyRead = 2

	if c.calls.Add(1) == bodyRead {
		c.reads <- c.Conn.RemoteAddr().String()
	}

	n, err := c.Conn.Read(buf)

	return n, err
}

// slowRequest is an evaluation whose body is sent in two parts.
type slowRequest struct {
	conn net.Conn
	rig  *relayRig
}

// startSlowRequest sends the headers and returns once the handler of the server waits for the body.
func startSlowRequest(t *testing.T, rig *relayRig, reads <-chan string) *slowRequest {
	t.Helper()

	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", strings.TrimPrefix(rig.url, "http://"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	head := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: relay\r\nX-Api-Key: %s\r\nContent-Type: application/json\r\n"+
		"Content-Length: %d\r\n\r\n", evalPath, tenantKey, len(slowBody))
	_, err = conn.Write([]byte(head))
	require.NoError(t, err)

	want := conn.LocalAddr().String()

	for {
		select {
		case got := <-reads:
			if got == want {
				return &slowRequest{conn: conn, rig: rig}
			}
		case <-time.After(waitBound):
			require.FailNow(t, "the server did not read the request")
		}
	}
}

// finish sends the body and returns the status of the answer.
func (s *slowRequest) finish(t *testing.T) int {
	t.Helper()

	_, err := s.conn.Write([]byte(slowBody))
	require.NoError(t, err)

	resp, err := http.ReadResponse(bufio.NewReader(s.conn), nil)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	return resp.StatusCode
}

func (r *relayRig) awaitCode() int {
	r.store.t.Helper()

	select {
	case code := <-r.done:
		return code
	case <-time.After(waitBound):
		require.FailNow(r.store.t, "the relay did not exit", r.stderr.String())

		return 0
	}
}

// goroutinesOf counts the goroutines that carry the label of the rig.
func goroutinesOf(label string) int { return goroutinesRunning(label, "") }

// goroutinesRunning counts the goroutines that carry the label of the rig and have the function in their stack.
func goroutinesRunning(label, function string) int {
	var dump bytes.Buffer

	_ = pprof.Lookup("goroutine").WriteTo(&dump, 1)

	count := 0

	for block := range strings.SplitSeq(dump.String(), "\n\n") {
		if strings.Contains(block, fmt.Sprintf("%q:%q", rigLabel, label)) && strings.Contains(block, function) {
			count++
		}
	}

	return count
}

// A request in flight when the signal arrives completes; the log lists the steps in order.
func TestShutdownLetsARequestInFlightFinish(t *testing.T) {
	t.Parallel()

	reads := make(chan string, 64)
	rig := startRelay(t, &rigOptions{reads: reads})
	rig.awaitGeneration(1)

	slow := startSlowRequest(t, rig, reads)

	rig.cancel()
	assert.Equal(t, bootstrap.DefaultShutdownTimeout, rig.awaitTimeout())

	assert.Equal(t, http.StatusOK, slow.finish(t), "the request in flight was served, not cut")
	assert.Equal(t, 0, rig.awaitCode())

	log := rig.stderr.String()
	last := -1

	for _, line := range []string{
		"shutdown: readiness off", "shutdown: change streams closed", "shutdown: API server stopped",
		"shutdown: core stopped",
	} {
		at := strings.Index(log, line)
		require.Positive(t, at, "%q is logged", line)
		assert.Greater(t, at, last, "%q comes after the previous step", line)

		last = at
	}

	assert.NotContains(t, log, "stopped hard")
}

// A request that outlives the timeout is cut, the log says how many, and the exit code is not zero.
func TestShutdownCutsARequestThatOutlivesTheTimeout(t *testing.T) {
	t.Parallel()

	reads := make(chan string, 64)
	rig := startRelay(t, &rigOptions{reads: reads})
	rig.awaitGeneration(1)

	slow := startSlowRequest(t, rig, reads)
	_ = startSlowRequest(t, rig, reads)

	rig.cancel()
	assert.Equal(t, bootstrap.DefaultShutdownTimeout, rig.awaitTimeout())

	// the timeout ends while the request is still in flight
	(<-rig.timeoutEnd)()

	assert.Equal(t, 1, rig.awaitCode())

	_, err := slow.conn.Write([]byte(slowBody))
	if err == nil {
		var resp *http.Response

		resp, err = http.ReadResponse(bufio.NewReader(slow.conn), nil)
		if err == nil {
			_ = resp.Body.Close()
		}
	}

	require.Error(t, err, "the connection was cut")

	log := rig.stderr.String()
	assert.Contains(t, log, `"inFlightRequests":2`)
	assert.Contains(t, log, "shutdown: stopped hard")
}

// During the drain delay /readyz answers 503 while the API still answers 200, and the timeout starts only after it.
func TestShutdownKeepsServingDuringTheDrainDelay(t *testing.T) {
	t.Parallel()

	delays := make(chan time.Duration, 1)
	release := make(chan struct{})
	rig := startRelay(t, &rigOptions{
		extra: drainExtra,
		drainWait: func(ctx context.Context, delay time.Duration) error {
			delays <- delay

			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return fmt.Errorf("test gate: %w", ctx.Err())
			}
		},
	})
	rig.awaitGeneration(1)

	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	rig.cancel()

	select {
	case got := <-delays:
		assert.Equal(t, 20*time.Second, got)
	case <-time.After(waitBound):
		require.FailNow(t, "the drain delay did not start")
	}

	status, body := doRequest(t, http.MethodGet, rig.url+readyPath, "", nil)
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.JSONEq(t, `{"ready":false,"reason":"`+reasonDown+`"}`, body)

	status, value := rig.evaluate(tenantKey)
	assert.Equal(t, http.StatusOK, status, "the API answers during the drain delay")
	assert.NotNil(t, value)

	assert.Empty(t, rig.timeoutFor, "the timeout of the stop has not started during the drain delay")

	close(release)

	assert.Equal(t, bootstrap.DefaultShutdownTimeout, rig.awaitTimeout())
	assert.Equal(t, 0, rig.awaitCode())
}

// A start that failed (the port is in use) skips the drain delay: nothing serves, so there is nothing to drain.
func TestShutdownSkipsTheDrainWhenTheStartFailed(t *testing.T) {
	t.Parallel()

	rig := startRelay(t, &rigOptions{
		extra:       drainExtra,
		listenFails: true,
		drainWait: func(context.Context, time.Duration) error {
			assert.Fail(t, "the drain delay ran after a failed start")

			return nil
		},
	})

	assert.Equal(t, 1, rig.awaitCode())
	assert.Contains(t, rig.stderr.String(), errPortInUse.Error())
	assert.NotContains(t, rig.stderr.String(), "still serving")
}

// While the start of the server is held (a slow initialisation that ignores its context), the shutdown still ends
// at its timeout: the core is stopped, the exit code is not zero, and the held start is joined once it is released.
func TestShutdownIsBoundedWhileTheStartIsHeld(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	gate := make(chan struct{})
	rig := startRelay(t, &rigOptions{serverOptions: []api.Option{api.WithStartupStep(func(context.Context) {
		close(entered)
		<-gate
	})}})

	// registered after the rig: it runs first, so the held start returns and the rig can join it
	t.Cleanup(func() { close(gate) })

	select {
	case <-entered:
	case <-time.After(waitBound):
		require.FailNow(t, "the start of the server did not reach its startup step")
	}

	rig.cancel()
	assert.Equal(t, bootstrap.DefaultShutdownTimeout, rig.awaitTimeout())
	(<-rig.timeoutEnd)()

	assert.Equal(t, 1, rig.awaitCode())

	log := rig.stderr.String()
	assert.Contains(t, log, "shutdown: core", "the core was stopped although the server was held")
	assert.Contains(t, log, "shutdown: stopped hard")
}

// The second signal during the drain delay stops hard at once.
func TestShutdownSecondSignalStopsHard(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	rig := startRelay(t, &rigOptions{
		extra: drainExtra,
		drainWait: func(ctx context.Context, _ time.Duration) error {
			close(entered)
			<-ctx.Done()

			return fmt.Errorf("test gate: %w", ctx.Err())
		},
	})
	rig.awaitGeneration(1)
	rig.cancel()

	select {
	case <-entered:
	case <-time.After(waitBound):
		require.FailNow(t, "the drain delay did not start")
	}

	close(rig.force)

	assert.Equal(t, 1, rig.awaitCode())

	log := rig.stderr.String()
	assert.Contains(t, log, "shutdown: second stop signal, stopping hard")
	assert.Contains(t, log, "shutdown: the drain delay was cut short")
	assert.Contains(t, log, "shutdown: stopped hard")
}

// The context given to the shutdown is not the signal context: it is alive when the signal context is done.
func TestShutdownContextIsNotTheSignalContext(t *testing.T) {
	t.Parallel()

	type seen struct {
		shutdownErr, signalErr error
	}

	got := make(chan seen, 1)

	var rig *relayRig

	rig = startRelay(t, &rigOptions{onDraining: func(ctx context.Context) {
		got <- seen{shutdownErr: ctx.Err(), signalErr: rig.ctx.Err()}
	}})
	rig.awaitGeneration(1)
	rig.cancel()

	assert.Equal(t, 0, rig.awaitCode())

	select {
	case s := <-got:
		require.ErrorIs(t, s.signalErr, context.Canceled, "the signal context is done")
		require.NoError(t, s.shutdownErr, "the shutdown context is not cancelled by it")
	default:
		require.FailNow(t, "the shutdown hook did not run")
	}
}

// Every goroutine of the relay is joined when run returns.
func TestShutdownJoinsEveryGoroutine(t *testing.T) {
	t.Parallel()

	rig := startRelay(t, &rigOptions{})
	rig.awaitGeneration(1)
	assert.Positive(t, goroutinesOf(rig.label), "the relay runs goroutines under the label")

	// the workers of the two streams (websocket and SSE) keep the label that started them: wait until both entered
	// their run loops, then count
	started := <-rig.started
	require.NoError(t, started.streams.AwaitStarted(t.Context()))

	assert.Equal(t, 2, goroutinesRunning(rig.label, "(*lane).run"), "the stream workers carry the label")

	rig.cancel()
	assert.Equal(t, 0, rig.awaitCode())

	<-rig.finished

	// a goroutine that signalled its end may still be returning: wait, bounded, until none is left
	assert.Eventually(t, func() bool { return goroutinesOf(rig.label) == 0 }, waitBound, time.Millisecond,
		"goroutines of the relay are left")
}
