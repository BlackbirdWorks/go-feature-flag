package manifestreader_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// gatedSched is a Scheduler whose waits return at once until the reader has made as many polls as the limit
// allows; it then parks until the test raises the limit. It records every wait, so the test reads the timing
// the reader asked for (the start of the round waits and the spreading delay) without any real timer.
type gatedSched struct {
	polls  func() uint64
	wake   chan struct{}
	parked chan struct{}
	waits  []time.Duration
	limit  uint64
	mu     sync.Mutex
}

func newGatedSched() *gatedSched {
	return &gatedSched{wake: make(chan struct{}), parked: make(chan struct{}, 4096), limit: 1}
}

func (s *gatedSched) Wait(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.waits = append(s.waits, d)
	s.mu.Unlock()

	for {
		s.mu.Lock()
		limit, wake := s.limit, s.wake
		s.mu.Unlock()

		if s.polls() < limit {
			return nil
		}

		s.parked <- struct{}{}

		select {
		case <-wake:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// runTo lets the reader make that many polls in all.
func (s *gatedSched) runTo(polls uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.limit = polls
	close(s.wake)
	s.wake = make(chan struct{})
}

// shorterThan returns the waits below limit, in order: with a poll interval above limit these are the waits that
// are not the wait between two rounds (the spreading delay before the object fetches).
func (s *gatedSched) shorterThan(limit time.Duration) []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []time.Duration

	for _, d := range s.waits {
		if d < limit {
			out = append(out, d)
		}
	}

	return out
}

// replica is one reader of a fleet.
type replica struct {
	reader *manifestreader.Reader
	sched  *gatedSched
}

// fleet is readers on one store, each with its own scheduler.
type fleet struct {
	t        testing.TB
	replicas []*replica
}

// readerSpec is the configuration of one reader of a fleet.
type readerSpec struct {
	opts []manifestreader.Option
	cfg  manifestreader.Config
}

func startFleet(tb testing.TB, store manifestreader.ObjectStore, specs []readerSpec) *fleet {
	tb.Helper()

	flt := &fleet{t: tb}
	ctx, cancel := context.WithCancel(tb.Context())

	var wg sync.WaitGroup

	for i := range specs {
		cfg := specs[i].cfg
		cfg.DisableJitter = true

		if cfg.Connection == "" {
			cfg.Connection = connControl
		}

		if cfg.ManifestKey == "" {
			cfg.ManifestKey = manifestKey
		}

		if cfg.Channel == "" {
			cfg.Channel = testChannel
		}

		sched := newGatedSched()
		opts := append([]manifestreader.Option{manifestreader.WithScheduler(sched)}, specs[i].opts...)

		reader, err := manifestreader.New(store, &cfg, opts...)
		require.NoError(tb, err)

		sched.polls = func() uint64 { return reader.Status().Polls }
		flt.replicas = append(flt.replicas, &replica{reader: reader, sched: sched})

		wg.Go(func() { assert.NoError(tb, reader.Run(ctx)) })
	}

	tb.Cleanup(func() {
		cancel()

		finished := make(chan struct{})

		go func() {
			wg.Wait()
			close(finished)
		}()

		select {
		case <-finished:
		case <-time.After(wait):
			assert.Fail(tb, "the readers did not stop")
		}
	})

	flt.settle()

	return flt
}

// settle waits until every reader is parked at the current limit.
func (f *fleet) settle() {
	f.t.Helper()

	for _, rep := range f.replicas {
		select {
		case <-rep.sched.parked:
		case <-time.After(wait):
			require.FailNow(f.t, "a reader did not end its round")
		}
	}
}

// runTo lets every reader make that many polls in all and waits until all of them are done with them. A reader parks
// once per limit, and every reader has its own channel, so each is drained in turn.
func (f *fleet) runTo(polls uint64) {
	f.t.Helper()

	for _, rep := range f.replicas {
		rep.sched.runTo(polls)
	}

	for _, rep := range f.replicas {
		select {
		case <-rep.sched.parked:
		case <-time.After(wait):
			require.FailNow(f.t, "a reader did not end its round")
		}
	}
}
