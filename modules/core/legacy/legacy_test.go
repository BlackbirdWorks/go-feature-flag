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

func TestWarnerWithoutDestination(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		warner *legacy.Warner
		// wantPanics is whether warning through this warner panics: neither of them does, the warning is dropped
		wantPanics bool
	}{
		{name: "nil log function", warner: legacy.NewWarner(nil), wantPanics: false},
		{name: "nil warner", warner: nil, wantPanics: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantPanics, panics(func() { tt.warner.Warn("k", "dropped") }))
		})
	}
}

func TestWarnersDoNotShareState(t *testing.T) {
	t.Parallel()
	var first, second []string
	w1 := legacy.NewWarner(func(kind, _ string) { first = append(first, kind) })
	w2 := legacy.NewWarner(func(kind, _ string) { second = append(second, kind) })
	w1.Warn("k", "m")
	w2.Warn("k", "m")
	w1.Warn("k", "m")
	assert.Equal(t, []string{"k"}, first)
	assert.Equal(t, []string{"k"}, second)
}

// panics reports whether f panics.
func panics(f func()) bool {
	panicked := true
	func() {
		defer func() { _ = recover() }()
		f()
		panicked = false
	}()

	return panicked
}
