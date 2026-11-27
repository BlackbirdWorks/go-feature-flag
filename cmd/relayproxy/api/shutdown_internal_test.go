package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/testhelpers/slogutil"
)

// Two listeners with one held request each: the shutdown that ends before they finish cuts 2 requests in all, and
// the log line of each listener shows its own 1.
func TestCutRequestsAreCountedPerServer(t *testing.T) {
	t.Parallel()

	logger, logs := slogutil.NewObserver(slog.LevelDebug)
	srv := &Server{logger: logger, readTimeout: time.Minute, shutdown: &shutdownState{}}

	release := make(chan struct{})
	entered := make(chan struct{}, 2)

	hold := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		entered <- struct{}{}

		<-release
	})

	serveCtx, stopServing := context.WithCancel(t.Context())

	var running, serving sync.WaitGroup

	// registered first, so it runs last: the held handlers end, then every goroutine is joined
	t.Cleanup(running.Wait)
	t.Cleanup(serving.Wait)
	t.Cleanup(func() { close(release) })

	urls := make([]string, 0, 2)
	outcomes := make(chan error, 2)

	for _, name := range []string{"api", "ops"} {
		listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)

		urls = append(urls, "http://"+listener.Addr().String())

		serving.Go(func() { assert.NoError(t, srv.serve(serveCtx, hold, listener, false, name)) })
	}

	for _, url := range urls {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
		require.NoError(t, err)

		running.Go(func() {
			resp, doErr := http.DefaultClient.Do(req)
			if doErr == nil {
				_ = resp.Body.Close()
			}

			outcomes <- doErr
		})
	}

	for range 2 {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			require.FailNow(t, "a request did not reach its handler")
		}
	}

	// the shutdown context is already over: the servers close hard
	over, end := context.WithCancel(t.Context())
	end()
	srv.shutdown.ctx.Store(&over)

	stopServing()

	serving.Wait() // both servers closed hard and logged what they cut

	assert.Equal(t, 2, srv.shutdown.cutCount())

	perServer := map[string]int64{}

	for _, entry := range logs.All() {
		if entry.Message != "Error shutting down the server, cut" {
			continue
		}

		fields := entry.ContextMap()
		name, _ := fields["server"].(string)
		count, _ := fields["inFlightRequests"].(int64)
		perServer[name] = count
	}

	assert.Equal(t, map[string]int64{"api": 1, "ops": 1}, perServer)

	// both held requests were disconnected by the hard stop
	for range 2 {
		select {
		case outcome := <-outcomes:
			require.Error(t, outcome, "the request was cut, not answered")
		case <-time.After(10 * time.Second):
			require.FailNow(t, "a cut request was not disconnected")
		}
	}
}

// Stop keeps a bound when its context has none; a context that has a deadline is left as it is.
func TestBoundedStop(t *testing.T) {
	t.Parallel()

	withDeadline, cancel := context.WithTimeout(t.Context(), time.Hour)
	t.Cleanup(cancel)

	tests := []struct {
		ctx          context.Context //nolint:containedctx // the table gives each row its context
		name         string
		wantSameCtx  bool
		wantDeadline bool
	}{
		{name: "no deadline gets one", ctx: context.Background(), wantDeadline: true},
		{name: "a deadline stays", ctx: withDeadline, wantSameCtx: true, wantDeadline: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, end := boundedStop(tt.ctx, time.Minute)
			defer end()

			_, hasDeadline := got.Deadline()

			assert.Equal(t, tt.wantDeadline, hasDeadline)
			assert.Equal(t, tt.wantSameCtx, got == tt.ctx)
		})
	}
}
