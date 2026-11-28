package manifestreader

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrNotPolling is returned by [Reader.Refresh] when the reader is not running: it was not started, or it stopped.
var ErrNotPolling = errors.New("manifestreader: the reader is not polling")

// RefreshResult is the outcome of the round a [Reader.Refresh] waited for. It is never serialised: the endpoint of the
// relay answers with a type of its own.
type RefreshResult struct {
	// Poll is the result of the poll of the manifest: a Poll* result and its reason.
	Poll PollStatus
	// Generation is the generation of the held manifest after the round, zero before the first accepted one.
	Generation uint64
	// Changed says that the round accepted a manifest of another generation than the one held before it.
	Changed bool
	// Revision is the number of the last snapshot the reader emitted, zero before the first: the snapshot the round
	// produced, or the one that was already out when it produced none.
	Revision uint64
}

// refreshCall is the waiters of one round: they all get its result.
type refreshCall struct {
	done    chan struct{}
	result  RefreshResult
	waiters int
}

// refresher is the trigger of the polling loop. The zero value is not usable: use [newRefresher].
type refresher struct {
	current *refreshCall
	// wake asks the loop to poll now; it has room for one request, a second one is the same request.
	wake chan struct{}
	// stopped is closed when the loop ended.
	stopped chan struct{}
	mu      sync.Mutex
}

func newRefresher() *refresher {
	return &refresher{wake: make(chan struct{}, 1), stopped: make(chan struct{})}
}

// Refresh makes the reader poll the manifest now, through the loop that polls on schedule: there is no second poll
// path. Calls that arrive while a round is waiting to run, or is running, share that round and its result. It
// returns when the round ended or ctx is done; the round itself is not cancelled by ctx, it is bounded by the request
// timeout of the reader.
func (r *Reader) Refresh(ctx context.Context) (RefreshResult, error) {
	trigger := r.refresh

	select {
	case <-trigger.stopped:
		return RefreshResult{}, ErrNotPolling
	default:
	}

	if !r.started.Load() {
		return RefreshResult{}, ErrNotPolling
	}

	trigger.mu.Lock()

	call := trigger.current
	if call == nil {
		call = &refreshCall{done: make(chan struct{})}
		trigger.current = call
	}

	call.waiters++

	trigger.mu.Unlock()

	select {
	case trigger.wake <- struct{}{}:
	default:
	}

	select {
	case <-call.done:
		return call.result, nil
	case <-trigger.stopped:
		return RefreshResult{}, ErrNotPolling
	case <-ctx.Done():
		return RefreshResult{}, fmt.Errorf("manifestreader: waiting for the refresh: %w", ctx.Err())
	}
}

// complete gives the result of a round to the waiters, if any.
func (t *refresher) complete(result *RefreshResult) {
	t.mu.Lock()
	call := t.current
	t.current = nil
	t.mu.Unlock()

	if call != nil {
		call.result = *result

		// the request that was waiting for this round is answered: it needs no round of its own
		select {
		case <-t.wake:
		default:
		}

		close(call.done)
	}
}

// roundResult is what a round left in the status, compared with the generation held before it.
func (r *Reader) roundResult(before uint64) RefreshResult {
	status := r.Status()

	return RefreshResult{
		Poll: status.Poll, Generation: status.Manifest.Generation, Changed: status.Manifest.Generation != before,
		Revision: r.revision,
	}
}

// sleepOrRefresh is sleep that a refresh request cuts short: it reports true when the loop must poll now. The virtual
// time advances only for a wait that completed.
func (r *Reader) sleepOrRefresh(ctx context.Context, delay time.Duration) (bool, error) {
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	done := make(chan error, 1)
	started := r.now()

	go func() { done <- r.sched.Wait(waitCtx, delay) }()

	select {
	case err := <-done:
		if err != nil {
			return false, fmt.Errorf("manifestreader: %w", err)
		}

		r.vnow += delay

		return false, nil
	case <-r.refresh.wake:
		cancel()

		waitErr := <-done
		_ = waitErr // the wait was cancelled on purpose

		// the time that passed counts: the retries of the failed objects are instants of this virtual clock
		r.vnow += min(max(r.now().Sub(started), 0), delay)

		return true, nil
	}
}
