package resolver_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmenttest"
)

// TestResolverConforms runs the conformance suite on each decorator of the resolver, stacked on a fake store.
func TestResolverConforms(t *testing.T) {
	t.Parallel()

	stacks := []struct {
		opts *resolver.Options
		name string
	}{
		{name: "defaults", opts: &resolver.Options{}},
		{name: "cache only", opts: &resolver.Options{Default: resolver.SegmentOptions{NoBreaker: true}}},
		{name: "breaker only", opts: &resolver.Options{Default: resolver.SegmentOptions{NoCache: true}}},
		{
			name: "coalescing only",
			opts: &resolver.Options{Default: resolver.SegmentOptions{NoCache: true, NoBreaker: true}},
		},
		{
			name: "explicit normalization none",
			opts: &resolver.Options{Default: resolver.SegmentOptions{Normalize: resolver.NormalizeNone}},
		},
	}

	for _, stack := range stacks {
		t.Run(stack.name, func(t *testing.T) {
			t.Parallel()

			segmenttest.Run(t, func(data segmenttest.Data) (segmenttest.Fixture, error) {
				store := &fakeStore{data: members(data)}

				res, err := resolver.New(store, stack.opts)
				if err != nil {
					return segmenttest.Fixture{}, err
				}

				return segmenttest.Fixture{Provider: res, Backend: store, Close: store.Release}, nil
			}, nil)
		})
	}
}

var _ segment.Provider = (*resolver.Resolver)(nil)
