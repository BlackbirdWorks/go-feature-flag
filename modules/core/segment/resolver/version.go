package resolver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// SetVersion tells the resolver the current version of a segment. Cached answers fetched under another version become
// misses (they are not flushed: they age out of the LRU), and the calls in flight for the segment are detached, so
// their answers are not cached and a caller that comes after asks the store again. The same token changes nothing. It
// reports whether the version changed; a segment that is unknown to the resolver or has no cache is ignored.
//
// It is the seam of change detection: [Resolver.RefreshVersions] calls it with what a versioner reports, and a
// [segment.ChangeNotifier] consumer calls it with a pushed change.
func (r *Resolver) SetVersion(name, token string) bool {
	sst := (*r.states.Load())[name]
	if sst == nil || sst.cache == nil {
		return false
	}

	sst.mu.Lock()
	defer sst.mu.Unlock()

	if !sst.cache.setVersion(token) {
		return false
	}

	sst.epoch.Add(1)
	sst.flights = make(map[string]*flight)
	sst.notLoadedUntil = time.Time{}

	return true
}

// RefreshVersions is one step of the version watcher, driven by the caller off the request path (the resolver has no
// timer): it asks versioner for the version of each segment that has [SegmentOptions.Versioned] set, in name order, and
// applies it with [Resolver.SetVersion]. A segment whose version cannot be read keeps its current version (the
// time-to-live is the fallback); the errors of all such segments are joined, and the step goes on after one.
func (r *Resolver) RefreshVersions(ctx context.Context, versioner segment.Versioner) error {
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
		err := ctx.Err()
		if err != nil {
			return errors.Join(append(errs, fmt.Errorf("segment %q: %w", name, err))...)
		}

		token, err := versioner.Version(ctx, name)
		if err != nil {
			errs = append(errs, fmt.Errorf("segment %q version: %w", name, err))

			continue
		}

		r.SetVersion(name, token)
	}

	return errors.Join(errs...)
}
