package resolver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// SetVersion tells the resolver the current version of a segment. A token that differs from the current one starts a
// new generation: cached answers fetched under an earlier generation become misses (they are not flushed: they age
// out of the LRU), also when the token is one that the segment had before, and the calls in flight for the segment
// are detached, so their answers are not cached and a caller that comes after asks the store again. The same token
// changes nothing. It reports whether the version changed; a segment that is unknown to the resolver or has no cache
// is ignored.
//
// It is the seam of change detection: [Resolver.RefreshVersions] calls it with what a versioner reports, and a
// [segment.ChangeNotifier] consumer calls it with a pushed change.
func (r *Resolver) SetVersion(name, token string) bool {
	return r.install(name, token, nil)
}

// install sets the version; when gen is not nil it does so only if the generation is still *gen.
func (r *Resolver) install(name, token string, gen *uint64) bool {
	sst := (*r.states.Load())[name]
	if sst == nil || sst.cache == nil {
		return false
	}

	sst.mu.Lock()
	defer sst.mu.Unlock()

	if gen != nil && sst.cache.gen.Load() != *gen {
		return false // a newer change arrived while the version was being read: it wins
	}

	if !sst.cache.setVersion(token) {
		return false
	}

	sst.epoch.Add(1)
	sst.flights = make(map[string]*flight)
	sst.notLoadedUntil = time.Time{}

	return true
}

// generationOf returns the generation of the version of a segment, false when it has none.
func (r *Resolver) generationOf(name string) (uint64, bool) {
	sst := (*r.states.Load())[name]
	if sst == nil || sst.cache == nil {
		return 0, false
	}

	return sst.cache.gen.Load(), true
}

// RefreshVersions is one step of the version watcher, driven by the caller off the request path (the resolver has no
// timer): it asks versioner for the version of each segment that has [SegmentOptions.Versioned] set, in name order, and
// applies it. It is admitted like a lookup: after Shutdown it returns [ErrClosed], and Shutdown waits for a step in
// flight. A version that was read while a newer one was set (a push, another refresh) is dropped, the newer one wins.
// A segment whose version cannot be read keeps its current version (the time-to-live is the fallback); the errors of
// all such segments are joined, and the step goes on after one.
func (r *Resolver) RefreshVersions(ctx context.Context, versioner segment.Versioner) error {
	leave, err := r.enter()
	if err != nil {
		return err
	}
	defer leave()

	states := *r.states.Load()
	names := make([]string, 0, len(states))

	for name, sst := range states {
		if sst.opts.Versioned {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	var errs []error

	for _, name := range names {
		ctxErr := ctx.Err()
		if ctxErr != nil {
			return errors.Join(append(errs, fmt.Errorf("segment %q: %w", name, ctxErr))...)
		}

		gen, _ := r.generationOf(name)

		token, readErr := versioner.Version(ctx, name)
		if readErr != nil {
			errs = append(errs, fmt.Errorf("segment %q version: %w", name, readErr))

			continue
		}

		r.install(name, token, &gen)
	}

	return errors.Join(errs...)
}
