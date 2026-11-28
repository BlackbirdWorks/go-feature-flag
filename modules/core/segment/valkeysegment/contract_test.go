package valkeysegment_test

import (
	"context"
	"testing"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmenttest"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

const (
	nameKeysLayout = "keys layout"
	shutdownBound  = 5 * time.Second
)

func TestProviderConformsOnTheStubServer(t *testing.T) {
	t.Parallel()

	layouts := []struct {
		name   string
		layout valkeysegment.Layout
	}{
		{name: "set layout", layout: valkeysegment.LayoutSet},
		{name: nameKeysLayout, layout: valkeysegment.LayoutKeys},
	}

	modes := []valkeysegment.Pipelining{
		valkeysegment.PipeliningAlways,
		valkeysegment.PipeliningAuto,
	}

	for _, tt := range layouts {
		for _, mode := range modes {
			t.Run(tt.name+"/"+string(mode), func(t *testing.T) {
				t.Parallel()

				segmenttest.Run(t, func(data segmenttest.Data) (segmenttest.Fixture, error) {
					srv := newStubServer(t)
					segs := map[string]valkeysegment.SegmentOptions{}

					for name, ids := range data {
						if tt.layout == valkeysegment.LayoutSet {
							segs[name] = valkeysegment.SegmentOptions{Key: "seg:{" + name + "}"}
							srv.setMembers("seg:{"+name+"}", ids...)

							continue
						}

						segs[name] = valkeysegment.SegmentOptions{
							Layout: valkeysegment.LayoutKeys,
							Key:    "seg:" + name + ":",
						}

						for _, id := range ids {
							srv.setKey("seg:" + name + ":" + id)
						}
					}

					prov, err := valkeysegment.Dial(
						srv.clientOption(),
						&valkeysegment.Options{
							MissingKey: valkeysegment.MissingUnknown,
							Segments:   segs,
							Pipelining: mode,
						},
					)
					if err != nil {
						return segmenttest.Fixture{}, err
					}

					closeFixture := func() {
						srv.Release() // a held command must end before Shutdown waits for it

						ctx, cancel := context.WithTimeout(context.Background(), shutdownBound)
						defer cancel()

						_ = prov.Shutdown(ctx)
					}

					return segmenttest.Fixture{
						Provider: prov,
						Missing:  segment.MissingUnknown,
						Backend:  srv,
						Close:    closeFixture,
					}, nil
				}, nil)
			})
		}
	}
}
