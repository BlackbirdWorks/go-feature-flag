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
	if req.VersionID != "" {
		// the fake keeps one version of an object: any named version is a missing one
		return manifestreader.Object{}, manifestreader.ErrNotFound
	}

	// the contract: a negative limit is a limit of zero (the raw fake compares the length with it)
	req.Limit = max(req.Limit, 0)

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

func (*faultyFake) HangBody(string) (*storetest.Hang, error) { return nil, storetest.ErrUnsupported }

func (*faultyFake) PutVersion(string, []byte) (string, error) { return "", storetest.ErrUnsupported }

func (f *faultyFake) Hang(key string) (*storetest.Hang, error) {
	hang := storetest.NewHang()

	f.mu.Lock()
	f.hangs[key] = hang
	f.mu.Unlock()

	return hang, nil
}

// fakeWaivers is what the in-memory fake does not offer. Its test and its fixture both state it.
func fakeWaivers() storetest.Waivers {
	return storetest.NewWaivers().
		With(storetest.CapVersions, "the fake keeps one version of an object").
		With(storetest.CapKeyValidation, "the fake keys are opaque strings").
		With(storetest.CapHangBody, "the fake answers in one step: there is no body to stall in")
}

func fakeFixture(*testing.T) storetest.Fixture {
	fake := &faultyFake{
		fakeStore: newFakeStore(),
		faults:    map[string]storetest.Fault{},
		hangs:     map[string]*storetest.Hang{},
	}

	return storetest.Fixture{Store: fake, Backend: fake, Unsupported: fakeWaivers()}
}

func TestConformance_Fake(t *testing.T) {
	t.Parallel()

	report := storetest.Run(t, "fake", fakeWaivers(), fakeFixture)

	assert.Equal(t, map[string]string{
		"version-id-reads-an-older-version": "versions: the fake keeps one version of an object",
		"unclean-keys-are-refused":          "key-validation: the fake keys are opaque strings",
		"cancel-while-reading-the-body":     "hang-body: the fake answers in one step: there is no body to stall in",
	}, report.Unsupported)
	assert.ElementsMatch(t, ranCases(report.Unsupported), report.Ran)
	require.NotEmpty(t, report.Ran)
}

func ranCases(unsupported map[string]string) []string {
	var out []string

	for _, name := range storetest.CaseNames() {
		if _, no := unsupported[name]; !no {
			out = append(out, name)
		}
	}

	return out
}
