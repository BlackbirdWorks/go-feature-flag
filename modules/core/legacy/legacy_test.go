package legacy_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/legacy"
)

func TestWarnerLogsOncePerKind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		kinds []string
		want  []string
	}{
		{name: "nothing used", kinds: nil, want: nil},
		{name: "same kind many times", kinds: []string{"a", "a", "a"}, want: []string{"a"}},
		{name: "two kinds", kinds: []string{"a", "b", "a", "b"}, want: []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var got []string
			w := legacy.NewWarner(func(kind, _ string) {
				mu.Lock()
				defer mu.Unlock()
				got = append(got, kind)
			})
			for _, k := range tt.kinds {
				w.Warn(k, "legacy name")
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestWarnerConcurrentUse(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	count := 0
	w := legacy.NewWarner(func(_, _ string) {
		mu.Lock()
		defer mu.Unlock()
		count++
	})
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			w.Warn("k", "m")
		})
	}
	wg.Wait()
	assert.Equal(t, 1, count)
}

func TestWarnerDefaultAndSetLogFunc(t *testing.T) {
	t.Parallel()
	// A nil LogFunc falls back to slog, which must not panic.
	w := legacy.NewWarner(nil)
	w.Warn("k", "goes to slog")

	var got []string
	w.SetLogFunc(func(kind, _ string) { got = append(got, kind) })
	w.Warn("other", "goes to the new destination")
	w.Warn("other", "ignored")
	assert.Equal(t, []string{"other"}, got)
}
