package manifestreader_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/storetest"
)

// toyStore is a small correct store with one defect that a test switches on. The conformance suite has to fail
// for every defect: a suite that passes a broken store proves nothing about a good one.
type toyStore struct {
	objs    map[string][]byte
	etags   map[string]string
	faults  map[string]storetest.Fault
	release chan struct{}
	defect  string
	mu      sync.Mutex
}

var (
	_ manifestreader.ObjectStore = (*toyStore)(nil)
	_ storetest.Backend          = (*toyStore)(nil)
)

// toyReason is why the toy waives what it waives.
const toyReason = "the toy cannot arrange this"

// caseChanged is a case that most defects of the ETag are caught by.
const caseFollows = "etag-follows-content"

const caseChanged = "conditional-changed-returns-new-body-and-etag"

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
)

func newToy(defect string) *toyStore {
	return &toyStore{
		objs: map[string][]byte{}, etags: map[string]string{}, faults: map[string]storetest.Fault{},
		release: make(chan struct{}), defect: defect,
	}
}

func toyETag(content []byte) string {
	sum := sha256.Sum256(content)

	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func (s *toyStore) Get(ctx context.Context, req manifestreader.GetRequest) (manifestreader.Object, error) {
	if s.defect == defectLeaks {
		go func() { <-s.release }()
	}

	if s.defect != defectIgnoresContext && ctx.Err() != nil {
		return manifestreader.Object{}, ctx.Err()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.faults[req.Key] == storetest.FaultDenied {
		return manifestreader.Object{}, &manifestreader.FetchError{
			Reason: manifestreader.ReasonDenied, Retryable: s.defect == defectDeniedRetryable,
		}
	}

	content, ok := s.objs[req.Key]
	if !ok || (req.VersionID != "" && s.defect != defectIgnoresVersion) {
		if s.defect == defectMissingIsEmpty {
			return manifestreader.Object{}, nil
		}

		return manifestreader.Object{}, manifestreader.ErrNotFound
	}

	if s.tooLarge(content, req.Limit) {
		return manifestreader.Object{}, manifestreader.ErrTooLarge
	}

	return s.answer(content, s.etags[req.Key], req.ETag)
}

func (s *toyStore) Put(key string, content []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	old, had := s.objs[key]

	switch {
	case s.defect == defectAliases && had && len(old) == len(content):
		copy(old, content) // the slice a reader holds changes under it
	default:
		s.objs[key] = bytes.Clone(content)
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

	return nil
}

func (s *toyStore) Overwrite(key string, content []byte) error { return s.Put(key, content) }

func (s *toyStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.objs, key)
	delete(s.etags, key)

	return nil
}

func (s *toyStore) Fail(key string, fault storetest.Fault) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.faults[key] = fault

	return nil
}

func (*toyStore) Hang(string) (*storetest.Hang, error)     { return nil, storetest.ErrUnsupported }
func (*toyStore) HangBody(string) (*storetest.Hang, error) { return nil, storetest.ErrUnsupported }

func (*toyStore) PutVersion(string, []byte) (string, error) { return "", storetest.ErrUnsupported }

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

// toyFixture is the toy with the capabilities it has: it can be denied, and all else the toy waives.
func toyFixture(defect string) (storetest.Fixture, *toyStore) {
	toy := newToy(defect)

	return storetest.Fixture{
		Store: toy, Backend: toy,
		Unsupported: storetest.NewWaivers().
			With(storetest.CapTransient, toyReason).With(storetest.CapThrottle, toyReason).
			With(storetest.CapHang, toyReason).With(storetest.CapHangBody, toyReason).
			With(storetest.CapVersions, toyReason).With(storetest.CapKeyValidation, toyReason),
	}, toy
}

// TestConformanceCatchesBrokenStores is the proof that the suite has teeth: each row is a store with one defect,
// and the named cases must fail for it. The table is the list of what the suite is known to catch.
func TestConformanceCatchesBrokenStores(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		defect    string
		wantText  string
		wantCases []string
	}{
		{
			name: "the ETag of a rewrite stays the old one", defect: defectStaleETag,
			wantCases: []string{
				caseChanged, caseFollows,
			},
		},
		{
			name: "the ETag is the same for every content", defect: defectConstantETag,
			wantCases: []string{
				caseChanged, caseFollows, "empty-object",
			},
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
			wantCases: []string{"missing-version-is-not-found"},
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
				require.True(t, ran, "case %s did not run: it needs a capability the toy waives", name)
				assert.NotEmpty(t, failures, "the case %s did not catch %q", name, tt.defect)
				assert.Contains(t, strings.Join(failures, "\n"), tt.wantText)

				close(toy.release)
			}
		})
	}
}

// TestConformanceAcceptsAGoodToy guards the table above: the toy without a defect passes every case it can run,
// so the failures of the other rows come from their defects.
func TestConformanceAcceptsAGoodToy(t *testing.T) {
	t.Parallel()

	ranAny := 0

	for _, name := range storetest.CaseNames() {
		fixture, toy := toyFixture(defectNone)

		failures, ran := storetest.Check(name, fixture)
		if !ran {
			continue
		}

		ranAny++

		assert.Empty(t, failures, "case %s failed for a store without a defect", name)

		close(toy.release)
	}

	// the toy waives the capabilities it cannot arrange: eight cases need one of them
	assert.Equal(t, len(storetest.CaseNames())-8, ranAny, "all cases but the eight that need a waived capability ran")
}
