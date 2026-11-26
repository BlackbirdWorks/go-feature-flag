package storetest_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/storetest"
)

// toyStore is a small in-memory store that is correct, with one defect that a test can switch on. It has every
// capability but key validation: the suite runs against it as a store of its own, and the suite has to fail for
// each defect. A suite that passes a broken store proves nothing about a good one.
type toyStore struct {
	versions map[string][]toyVersion
	faults   map[string]storetest.Fault
	hangs    map[string]*storetest.Hang
	bodies   map[string]*storetest.Hang
	etags    map[string]string
	release  chan struct{}
	defect   string
	mu       sync.Mutex
	nextID   int
	worker   bool
	started  bool
}

type toyVersion struct {
	id      string
	content []byte
}

var (
	_ manifestreader.ObjectStore = (*toyStore)(nil)
	_ storetest.Backend          = (*toyStore)(nil)
)

// The defects.
const (
	defectNone              = "none"
	defectStaleETag         = "stale ETag with new bytes"
	defectConstantETag      = "constant ETag"
	defectIgnoresLimit      = "ignores Limit"
	defectNotModifiedAlways = "not modified for an unknown ETag"
	defectBodyWithNotMod    = "body together with ErrNotModified"
	defectIgnoresContext    = "ignores the context"
	defectTruncates         = "truncates the body by one byte"
	defectLeaks             = "leaks a goroutine per Get"
	defectAliases           = "returns an internal slice that a later Put overwrites in place"
	defectIgnoresVersion    = "ignores VersionID"
	defectNegativeLimit     = "a negative Limit is no limit"
	defectDeniedRetryable   = "denied is retryable"
	defectMissingIsEmpty    = "a missing object is an empty one"
	defectETagOfOldBytes    = "ETag of the previous content with the new bytes"
	defectPartialOnCancel   = "a cancelled read returns the bytes it had"
)

// Names of cases that several rows name.
const (
	caseChanged = "conditional-changed-returns-new-body-and-etag"
	caseFollows = "etag-follows-content"
	caseKeys    = "unclean-keys-are-refused"
)

func newToy(defect string) *toyStore {
	return &toyStore{
		versions: map[string][]toyVersion{}, faults: map[string]storetest.Fault{},
		hangs: map[string]*storetest.Hang{}, bodies: map[string]*storetest.Hang{}, etags: map[string]string{},
		release: make(chan struct{}), defect: defect,
	}
}

func toyETag(content []byte) string {
	sum := sha256.Sum256(content)

	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func toyWaivers() storetest.Waivers {
	return storetest.NewWaivers().With(storetest.CapKeyValidation, "the toy keys are opaque")
}

func toyFixture(defect string) (storetest.Fixture, *toyStore) {
	toy := newToy(defect)

	return storetest.Fixture{Store: toy, Backend: toy, Unsupported: toyWaivers()}, toy
}

func (s *toyStore) Get(ctx context.Context, req manifestreader.GetRequest) (manifestreader.Object, error) {
	if s.defect == defectLeaks {
		go func() { <-s.release }()
	}

	hang, body := s.hangsOf(req.Key)

	for _, held := range []*storetest.Hang{hang, body} {
		if held == nil {
			continue
		}

		err := held.Block(ctx)
		if err != nil && s.defect == defectPartialOnCancel {
			return manifestreader.Object{Bytes: []byte("part")}, err
		}

		if err != nil {
			return manifestreader.Object{}, err
		}
	}

	if s.defect != defectIgnoresContext && ctx.Err() != nil {
		return manifestreader.Object{}, ctx.Err()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	failure := s.fault(req.Key)
	if failure != nil {
		return manifestreader.Object{}, failure
	}

	content, ok := s.find(req.Key, req.VersionID)
	if !ok {
		if s.defect == defectMissingIsEmpty {
			return manifestreader.Object{}, nil
		}

		return manifestreader.Object{}, manifestreader.ErrNotFound
	}

	if s.tooLarge(content, req.Limit) {
		return manifestreader.Object{}, manifestreader.ErrTooLarge
	}

	return s.answer(content, s.etagOf(req.Key, req.VersionID, content), req.ETag)
}

func (s *toyStore) Put(key string, content []byte) error {
	_, err := s.PutVersion(key, content)

	return err
}

func (s *toyStore) PutVersion(key string, content []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	versions := s.versions[key]
	had := len(versions) > 0

	var old []byte
	if had {
		old = versions[len(versions)-1].content
	}

	s.nextID++
	id := strconv.Itoa(s.nextID)

	if s.worker && !s.started {
		// a worker of the store, started by the first write and stopped by the cleanup of the fixture
		s.started = true

		go func() { <-s.release }()
	}

	if s.defect == defectAliases && had && len(old) == len(content) {
		copy(old, content) // the slice a reader holds changes under it
	} else {
		s.versions[key] = append(versions, toyVersion{id: id, content: bytes.Clone(content)})
	}

	switch {
	case s.defect == defectConstantETag:
		s.etags[key] = `"constant"`
	case s.defect == defectStaleETag && had:
		// the ETag of the first content stays
	case s.defect == defectETagOfOldBytes && had:
		s.etags[key] = toyETag(old)
	default:
		s.etags[key] = toyETag(content)
	}

	return id, nil
}

func (s *toyStore) Overwrite(key string, content []byte) error { return s.Put(key, content) }

func (s *toyStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.versions, key)
	delete(s.etags, key)

	return nil
}

func (s *toyStore) Fail(key string, fault storetest.Fault) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.faults[key] = fault

	return nil
}

func (s *toyStore) Hang(key string) (*storetest.Hang, error) {
	hang := storetest.NewHang()

	s.mu.Lock()
	s.hangs[key] = hang
	s.mu.Unlock()

	return hang, nil
}

func (s *toyStore) HangBody(key string) (*storetest.Hang, error) {
	hang := storetest.NewHang()

	s.mu.Lock()
	s.bodies[key] = hang
	s.mu.Unlock()

	return hang, nil
}

func (s *toyStore) hangsOf(key string) (*storetest.Hang, *storetest.Hang) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.hangs[key], s.bodies[key]
}

func (s *toyStore) fault(key string) error {
	switch s.faults[key] {
	case storetest.FaultDenied:
		return &manifestreader.FetchError{
			Reason: manifestreader.ReasonDenied, Retryable: s.defect == defectDeniedRetryable,
		}
	case storetest.FaultTransient:
		return &manifestreader.FetchError{Reason: manifestreader.ReasonError, Retryable: true}
	case storetest.FaultThrottled:
		return &manifestreader.FetchError{Reason: manifestreader.ReasonError, Retryable: true, Throttled: true}
	case storetest.FaultNone:
	}

	return nil
}

// find returns the current content of the key, or of the named version.
func (s *toyStore) find(key, versionID string) ([]byte, bool) {
	versions := s.versions[key]
	if len(versions) == 0 {
		return nil, false
	}

	if versionID == "" || s.defect == defectIgnoresVersion {
		return versions[len(versions)-1].content, true
	}

	for _, version := range versions {
		if version.id == versionID {
			return version.content, true
		}
	}

	return nil, false
}

func (s *toyStore) etagOf(key, versionID string, content []byte) string {
	if versionID == "" {
		return s.etags[key]
	}

	return toyETag(content)
}

// tooLarge applies the limit, as far as the defect of the toy lets it.
func (s *toyStore) tooLarge(content []byte, limit int64) bool {
	switch {
	case s.defect == defectIgnoresLimit:
		return false
	case s.defect == defectNegativeLimit && limit < 0:
		return false
	}

	return int64(len(content)) > max(limit, 0)
}

// answer is the conditional logic of the toy.
func (s *toyStore) answer(content []byte, etag, held string) (manifestreader.Object, error) {
	switch {
	case s.defect == defectNotModifiedAlways && held != "":
		return manifestreader.Object{}, manifestreader.ErrNotModified
	case held != "" && held == etag && s.defect == defectBodyWithNotMod:
		return manifestreader.Object{Bytes: content, ETag: etag}, manifestreader.ErrNotModified
	case held != "" && held == etag:
		return manifestreader.Object{}, manifestreader.ErrNotModified
	}

	if s.defect == defectTruncates && len(content) > 0 {
		content = content[:len(content)-1]
	}

	return manifestreader.Object{Bytes: content, ETag: etag}, nil
}

// TestSuiteRunsAgainstTheToy runs the whole suite on a store of the package itself: every case but the one that
// needs key validation runs and passes.
func TestSuiteRunsAgainstTheToy(t *testing.T) {
	t.Parallel()

	// The store of the fixture starts a worker, and the cleanup of the fixture stops it: the check for leaks
	// runs after that cleanup, or every case would fail.
	report := storetest.Run(t, "toy", toyWaivers(), func(t *testing.T) storetest.Fixture {
		t.Helper()

		fixture, toy := toyFixture(defectNone)
		toy.worker = true

		t.Cleanup(func() { close(toy.release) })

		return fixture
	})

	assert.Equal(t, map[string]string{caseKeys: "key-validation: the toy keys are opaque"}, report.Unsupported)
	assert.Len(t, report.Ran, len(storetest.CaseNames())-1)
}

// TestSuiteCatchesBrokenStores is the proof that the suite has teeth: each row is a store with one defect, and the
// named cases must fail for it. The table is the list of what the suite is known to catch.
func TestSuiteCatchesBrokenStores(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		defect    string
		wantText  string
		wantCases []string
	}{
		{
			name:      "the ETag of a rewrite stays the old one",
			defect:    defectStaleETag,
			wantCases: []string{caseChanged, caseFollows},
		},
		{
			name: "the ETag is the same for every content", defect: defectConstantETag,
			wantCases: []string{caseChanged, caseFollows, "empty-object", "etag-describes-the-bytes-returned"},
		},
		{
			name: "the limit is ignored", defect: defectIgnoresLimit,
			wantCases: []string{"limit-at-and-one-over", "limit-zero-and-negative", "empty-object"},
		},
		{
			name: "a negative limit lets everything through", defect: defectNegativeLimit,
			wantCases: []string{"limit-zero-and-negative"},
		},
		{
			name: "any ETag is answered with not-modified", defect: defectNotModifiedAlways,
			wantCases: []string{"unknown-etag-returns-the-object", caseChanged},
		},
		{
			name: "a body comes with the not-modified answer", defect: defectBodyWithNotMod,
			wantCases: []string{"conditional-unchanged-is-not-modified", "etag-is-stable-for-unchanged-content"},
		},
		{
			name: "the context is ignored", defect: defectIgnoresContext,
			wantCases: []string{"ended-context-is-an-error"},
		},
		{
			name: "the body is one byte short", defect: defectTruncates,
			wantCases: []string{"get-unconditional", caseChanged},
		},
		{
			name: "every Get leaves a goroutine", defect: defectLeaks, wantText: "left goroutines behind",
			wantCases: []string{"gets-leave-no-goroutine-behind", "get-unconditional"},
		},
		{
			name: "returned bytes are overwritten by a later Put", defect: defectAliases,
			wantCases: []string{"returned-bytes-are-not-aliased"},
		},
		{
			name: "a version that does not exist is served", defect: defectIgnoresVersion,
			wantCases: []string{"missing-version-is-not-found", "version-id-reads-an-older-version"},
		},
		{
			name: "a denied read is retryable", defect: defectDeniedRetryable,
			wantCases: []string{"denied-is-not-retryable"},
		},
		{
			name: "a missing object is an empty one", defect: defectMissingIsEmpty,
			wantCases: []string{"missing-is-not-found"},
		},
		{
			name: "a cancelled read returns the bytes it had", defect: defectPartialOnCancel,
			wantCases: []string{"cancel-while-reading", "cancel-while-reading-the-body", "deadline-while-reading"},
		},
		{
			name: "the ETag is the one of the previous bytes", defect: defectETagOfOldBytes,
			wantCases: []string{"etag-describes-the-bytes-returned", caseFollows},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, name := range tt.wantCases {
				fixture, toy := toyFixture(tt.defect)

				failures, ran := storetest.Check(name, fixture)

				close(toy.release)

				require.True(t, ran, "case %s did not run", name)
				assert.NotEmpty(t, failures, "the case %s did not catch %q", name, tt.defect)
				assert.Contains(t, strings.Join(failures, "\n"), tt.wantText)
			}
		})
	}
}

// TestSuiteAcceptsAGoodToy guards the table above: the toy without a defect passes every case, so the failures
// of the other rows come from their defects.
func TestSuiteAcceptsAGoodToy(t *testing.T) {
	t.Parallel()

	var notRun []string

	for _, name := range storetest.CaseNames() {
		fixture, toy := toyFixture(defectNone)

		failures, ran := storetest.Check(name, fixture)

		close(toy.release)

		if !ran {
			notRun = append(notRun, name)

			continue
		}

		assert.Empty(t, failures, "case %s failed for a store without a defect", name)
	}

	assert.Equal(t, []string{caseKeys}, notRun, "the one case that needs key validation")
}
