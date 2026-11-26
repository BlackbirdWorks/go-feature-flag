package manifestreader_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/storetest"
)

var errFaultArranged = errors.New("the fault arranged by the suite")

// faultyFake is the in-memory fake with the faults of the conformance suite in front of it.
type faultyFake struct {
	*fakeStore

	faults map[string]storetest.Fault
	hangs  map[string]*storetest.Hang
	mu     sync.Mutex
}

var (
	_ manifestreader.ObjectStore = (*faultyFake)(nil)
	_ storetest.Backend          = (*faultyFake)(nil)
)

func (f *faultyFake) Get(ctx context.Context, req manifestreader.GetRequest) (manifestreader.Object, error) {
	f.mu.Lock()
	fault, hang := f.faults[req.Key], f.hangs[req.Key]
	f.mu.Unlock()

	if hang != nil {
		err := hang.Block(ctx)
		if err != nil {
			return manifestreader.Object{}, err
		}
	}

	switch fault {
	case storetest.FaultDenied:
		return manifestreader.Object{}, &manifestreader.FetchError{
			Err:    errFaultArranged,
			Reason: manifestreader.ReasonDenied,
		}
	case storetest.FaultTransient:
		return manifestreader.Object{}, &manifestreader.FetchError{
			Err: errFaultArranged, Reason: manifestreader.ReasonError, Retryable: true,
		}
	case storetest.FaultThrottled:
		return manifestreader.Object{}, &manifestreader.FetchError{
			Err: errFaultArranged, Reason: manifestreader.ReasonError, Retryable: true, Throttled: true,
		}
	case storetest.FaultNone:
	}

	return f.fakeStore.Get(ctx, req)
}

func (f *faultyFake) Put(key string, content []byte) error {
	f.put(key, content)

	return nil
}

func (f *faultyFake) Overwrite(key string, content []byte) error { return f.Put(key, content) }

func (f *faultyFake) Delete(key string) error {
	f.remove(key)

	return nil
}

func (f *faultyFake) Fail(key string, fault storetest.Fault) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.faults[key] = fault

	return nil
}

func (f *faultyFake) Hang(key string) (*storetest.Hang, error) {
	hang := storetest.NewHang()

	f.mu.Lock()
	f.hangs[key] = hang
	f.mu.Unlock()

	return hang, nil
}

func fakeFixture(*testing.T) storetest.Fixture {
	fake := &faultyFake{
		fakeStore: newFakeStore(),
		faults:    map[string]storetest.Fault{},
		hangs:     map[string]*storetest.Hang{},
	}

	unsupported := make(map[storetest.Capability]string)
	unsupported[storetest.CapVersions] = "the fake keeps no version history and ignores the version id"
	unsupported[storetest.CapKeyValidation] = "the fake keys are opaque strings"

	return storetest.Fixture{
		Store: fake, Backend: fake,
		Unsupported: unsupported,
	}
}

func TestConformance_Fake(t *testing.T) {
	t.Parallel()

	report := storetest.Run(t, "fake", fakeFixture)

	assert.Equal(t, map[string]string{
		"missing-version-is-not-found": "versions: the fake keeps no version history and ignores the version id",
		"unclean-keys-are-refused":     "key-validation: the fake keys are opaque strings",
	}, report.Unsupported)
	assert.ElementsMatch(t, ranCases(report.Unsupported), report.Ran)
	require.NotEmpty(t, report.Ran)
}

// ranCases is every case of the suite except the unsupported ones.
func ranCases(unsupported map[string]string) []string {
	var out []string

	for _, name := range storetest.CaseNames() {
		if _, no := unsupported[name]; !no {
			out = append(out, name)
		}
	}

	return out
}
