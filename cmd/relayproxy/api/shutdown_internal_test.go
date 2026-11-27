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

	for _, name := range []string{"api", "ops"} {
		listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)

		urls = append(urls, "http://"+listener.Addr().String())

		serving.Go(func() { assert.NoError(t, srv.serve(serveCtx, hold, listener, false, name)) })
	}

	for _, url := range urls {
		running.Go(func() {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
			assert.NoError(t, err)

			if err != nil {
				return
			}

			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				_ = resp.Body.Close() // the request is cut: an error is the expected outcome
			}
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
}
