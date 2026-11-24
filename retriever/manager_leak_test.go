package retriever_test

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/internal/notification"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

// goroutinesLabeled counts the goroutines that carry the pprof label key=value. The goroutines started by
// code that runs under pprof.Do inherit its labels, so the count is scoped to what one test started
// and does not depend on the other tests running in parallel.
func goroutinesLabeled(key, value string) int {
	var buf bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&buf, 1); err != nil {
		return -1
	}
	want := fmt.Sprintf("%q:%q", key, value)
	total, current := 0, 0
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if n, _, ok := strings.Cut(line, " @ "); ok {
			if v, err := strconv.Atoi(n); err == nil {
				current = v
			}

			continue
		}
		if strings.HasPrefix(line, "# labels:") && strings.Contains(line, want) {
			total += current
		}
	}

	return total
}

// gatedRetriever retrieves valid flags once its gate is closed, then marks itself done.
type gatedRetriever struct {
	gate <-chan struct{}
	done *sync.WaitGroup
}

func (r gatedRetriever) Retrieve(ctx context.Context) ([]byte, error) {
	defer r.done.Done()
	select {
	case <-r.gate:
	case <-ctx.Done():
	}

	return []byte(persistedFlags), nil
}

// TestManager_ForceRefresh_ErrorDoesNotLeakGoroutines checks that a retrieval that fails on the first error
// leaves no goroutine behind once the slower retrievers are done: they must not block on sending their
// result to a caller that already returned. The goroutines are counted by pprof label, so only the ones
// started by the test are looked at.
func TestManager_ForceRefresh_ErrorDoesNotLeakGoroutines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		nbSlow      int
		wantRefresh bool
		wantLeaked  int
	}{
		{name: "one failing and one slow retriever", nbSlow: 1, wantRefresh: false, wantLeaked: 0},
		{name: "one failing and several slow retrievers", nbSlow: 10, wantRefresh: false, wantLeaked: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gate := make(chan struct{})
			var retrieved sync.WaitGroup
			retrieved.Add(tt.nbSlow)
			retrievers := []retriever.Retriever{failingRetriever{}}
			for range tt.nbSlow {
				retrievers = append(retrievers, gatedRetriever{gate: gate, done: &retrieved})
			}
			logger := fflog.FFLogger{}
			cacheManager := cache.New(notification.NewService([]notifier.Notifier{}), "", &logger)
			manager := retriever.NewManager(retriever.ManagerConfig{}, retrievers, cacheManager, &logger)

			var gotRefresh bool
			label := t.Name()
			// The timeout is only a bound: the slow retrievers give up with the context if the refresh
			// ever stops returning on the first error, so the test fails instead of hanging.
			refreshCtx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			pprof.Do(refreshCtx, pprof.Labels("leakcheck", label), func(ctx context.Context) {
				gotRefresh = manager.ForceRefresh(ctx)
			})
			// The refresh returned on the first error, not because the deadline released the slow retrievers.
			require.NoError(t, refreshCtx.Err())
			// The slow retrievers answer after the refresh already returned its error.
			close(gate)
			// Every slow retriever has returned its flags: the only thing left for its goroutine is to hand
			// the result over and return, which is what must not block.
			retrieved.Wait()

			assert.Equal(t, tt.wantRefresh, gotRefresh)
			// The condition is polled as fast as possible and the test passes as soon as it holds: the
			// timeout is only the bound after which a goroutine still blocked is reported as leaked.
			assert.Eventually(t, func() bool {
				runtime.Gosched()

				return goroutinesLabeled("leakcheck", label) == tt.wantLeaked
			}, 30*time.Second, time.Millisecond, "the retrievers goroutines must all return")
		})
	}
}
