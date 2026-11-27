package valkeysegment_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/segment/segmenttest"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

// faultBackend arranges the faults of the conformance suite in front of the fake store.
type faultBackend struct {
	store   *fakeStore
	gate    chan struct{}
	entered chan struct{}
	failSeg map[string]bool
	mu      sync.Mutex
	failed  bool
}

func newFaultBackend(store *fakeStore) *faultBackend {
	backend := &faultBackend{store: store, failSeg: map[string]bool{}}

	store.exec = backend.exec
	store.reply = backend.reply

	return backend
}

func (b *faultBackend) Fail() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failed = true
}

func (b *faultBackend) FailSegment(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failSeg[name] = true
}

func (b *faultBackend) Hang() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.gate, b.entered = make(chan struct{}), make(chan struct{}, 64)

	return b.entered
}

func (b *faultBackend) Release() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.gate != nil {
		close(b.gate)

		b.gate = nil
	}
}

func (b *faultBackend) Heal() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failed = false
	b.failSeg = map[string]bool{}
}

func TestProviderConformsOnTheFakeStore(t *testing.T) {
	t.Parallel()

	layouts := []struct {
		name   string
		layout valkeysegment.Layout
	}{
		{name: "set layout", layout: valkeysegment.LayoutSet},
		{name: nameKeysLayout, layout: valkeysegment.LayoutKeys},
	}

	for _, tt := range layouts {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			segmenttest.Run(t, func(data segmenttest.Data) (segmenttest.Fixture, error) {
				store := &fakeStore{sets: map[string]map[string]bool{}, keys: map[string]bool{}}
				segs := map[string]valkeysegment.SegmentOptions{}

				for name, ids := range data {
					if tt.layout == valkeysegment.LayoutSet {
						segs[name] = valkeysegment.SegmentOptions{Key: "seg:{" + name + "}"}
						store.sets["seg:{"+name+"}"] = map[string]bool{}

						for _, id := range ids {
							store.sets["seg:{"+name+"}"][id] = true
						}

						continue
					}

					segs[name] = valkeysegment.SegmentOptions{
						Layout: valkeysegment.LayoutKeys,
						Key:    "seg:" + name + ":",
					}

					for _, id := range ids {
						store.keys["seg:"+name+":"+id] = true
					}
				}

				prov, err := valkeysegment.New(store, &valkeysegment.Options{Segments: segs, Atomic: true})
				if err != nil {
					return segmenttest.Fixture{}, err
				}

				backend := newFaultBackend(store)

				return segmenttest.Fixture{Provider: prov, Backend: backend, Close: backend.Release}, nil
			}, nil)
		})
	}
}

// keyOf is the key a command works on: the second argument, the fourth for a script.
func keyOf(args []any) string {
	pos := 1
	if name, _ := args[0].(string); strings.HasPrefix(name, "eval") {
		pos = 3
	}

	if len(args) <= pos {
		return "" // a command without a key, such as the probe of Init
	}

	key, _ := args[pos].(string)

	return key
}

func (b *faultBackend) exec(ctx context.Context) error {
	b.mu.Lock()
	gate, entered, failed := b.gate, b.entered, b.failed
	b.mu.Unlock()

	if entered != nil {
		entered <- struct{}{}
	}

	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if failed {
		return errDial
	}

	return nil
}

func (b *faultBackend) reply(args []any) *fakeReply {
	key := keyOf(args)

	b.mu.Lock()
	defer b.mu.Unlock()

	for seg := range b.failSeg {
		if strings.Contains(key, seg) {
			return &fakeReply{err: serverError{msg: "WRONGTYPE"}}
		}
	}

	return nil
}
