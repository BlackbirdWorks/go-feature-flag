package retriever_test

import (
	"bytes"
	"context"
	"fmt"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

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

// gatedRetriever retrieves valid flags once its gate is closed.
type gatedRetriever struct{ gate <-chan struct{} }

func (r gatedRetriever) Retrieve(ctx context.Context) ([]byte, error) {
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
			retrievers := []retriever.Retriever{failingRetriever{}}
			for range tt.nbSlow {
				retrievers = append(retrievers, gatedRetriever{gate: gate})
			}
			logger := fflog.FFLogger{}
			cacheManager := cache.New(notification.NewService([]notifier.Notifier{}), "", &logger)
			manager := retriever.NewManager(retriever.ManagerConfig{}, retrievers, cacheManager, &logger)

			var gotRefresh bool
			label := t.Name()
			pprof.Do(t.Context(), pprof.Labels("leakcheck", label), func(ctx context.Context) {
				gotRefresh = manager.ForceRefresh(ctx)
			})
			// The slow retrievers answer after the refresh already returned its error.
			close(gate)

			assert.Equal(t, tt.wantRefresh, gotRefresh)
			assert.Eventually(t, func() bool { return goroutinesLabeled("leakcheck", label) == tt.wantLeaked },
				5*time.Second, 10*time.Millisecond, "the retrievers goroutines must all return")
		})
	}
}
