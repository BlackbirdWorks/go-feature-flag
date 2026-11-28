package corehttp

import (
	"context"
	"runtime/pprof"
	"sync"
)

// reaper runs the closes of the connections that a revalidation took out of the lists. A close can take time (a
// closing handshake on a slow network), and the swap of the runtime must not wait for it: the connections are out of
// the lists before they are handed over, so they receive nothing meanwhile.
type reaper struct {
	wake chan struct{}
	stop chan struct{}
	done chan struct{}
	// queue is the closes that wait.
	queue  []func()
	mu     sync.Mutex
	closed bool
}

func startReaper(labels context.Context, label string) *reaper {
	out := &reaper{wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}

	go func() {
		defer close(out.done)

		pprof.Do(
			labelsOrBackground(labels),
			pprof.Labels("corehttp_streams", label),
			func(context.Context) { out.run() },
		)
	}()

	return out
}

// offer queues closes and returns at once. After close, the closes run on the caller: nothing is left open.
func (r *reaper) offer(closers []func()) {
	if len(closers) == 0 {
		return
	}

	r.mu.Lock()

	if r.closed {
		r.mu.Unlock()

		for _, closeOne := range closers {
			closeOne()
		}

		return
	}

	r.queue = append(r.queue, closers...)
	r.mu.Unlock()

	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *reaper) close() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.closed {
		r.closed = true
		close(r.stop)
	}
}

func (r *reaper) take() []func() {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := r.queue
	r.queue = nil

	return out
}

func (r *reaper) run() {
	for {
		for _, closeOne := range r.take() {
			closeOne()
		}

		select {
		case <-r.stop:
			// what was queued before the stop is closed too
			for _, closeOne := range r.take() {
				closeOne()
			}

			return
		case <-r.wake:
		}
	}
}
