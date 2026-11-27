package storetest_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/storetest"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

// toyStore is a small in-memory store that is correct, with one defect that a test can switch on. It has every
// capability but key validation: the suite runs against it as a store of its own, and the suite has to fail for
// each defect. A suite that passes a broken store proves nothing about a good one.
type toyStore struct {
	versions         map[string][]toyVersion
	faults           map[string]storetest.Fault
	hangs            map[string]*storetest.Hang
	bodies           map[string]*storetest.Hang
	etags            map[string]string
	release          chan struct{}
	admit            chan struct{}
	defect           string
	nextID           int
	mu               sync.Mutex
	worker           bool
	started          bool
	generationETags  bool
	nilEmpty         bool
	ctxWorker        bool
	notModifiedFirst bool
	literalETags     bool
}

type toyVersion struct {
	id      string
	etag    string // the ETag of this version for a toy with generation numbers
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
	defectLeaksAndFails     = "leaks a goroutine and fails the read"
	defectLeaksUnderOwnDo   = "leaks a goroutine started under its own pprof.Do from the request context"
	defectPartialOnCancel   = "a cancelled read returns the bytes it had"
	defectServesStale       = "keeps serving the first content after a later write"
	defectOneShotDeadline   = "sets a one-shot deadline from the first Deadline() and ignores Done"
)

// The bound of the waits of a case for a store that is known to fail by never answering: a bounded wait that is
// certain to run out, and so does not depend on the speed of the machine. The default is the one of Check.
const (
	shortBound   = 300 * time.Millisecond
	defaultBound = 10 * time.Second
)

func cmpOr(value, fallback time.Duration) time.Duration {
	if value != 0 {
		return value
	}

	return fallback
}

// Names of cases that several rows name.
const (
	caseChanged   = "conditional-changed-returns-new-body-and-etag"
	caseFollows   = "etag-follows-content"
	caseKeys      = "unclean-keys-are-refused"
	caseGetUncond = "get-unconditional"
	caseDescribes = "etag-describes-the-bytes-returned"
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
	s.startWorkers(ctx)

	if s.defect == defectLeaksAndFails {
		return manifestreader.Object{}, &manifestreader.FetchError{Reason: manifestreader.ReasonError, Retryable: true}
	}

	release, err := s.admitRead(ctx)
	if err != nil {
		return manifestreader.Object{}, err
	}

	defer release()

	obj, err := s.waitHangs(ctx, req.Key)
	if err != nil {
		return obj, err
	}

	return s.lookup(ctx, req)
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

	if s.defect == defectServesStale && had {
		return id, nil // the write is lost: the first content stays
	}

	if s.defect == defectAliases && had && len(old) == len(content) {
		copy(old, content) // the slice a reader holds changes under it
	} else {
		s.versions[key] = append(versions, toyVersion{
			id: id, etag: s.generation(len(versions)), content: bytes.Clone(content),
		})
	}

	s.updateETag(key, had, old, content)

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

// lookup answers the read from the versions of the key.
func (s *toyStore) lookup(ctx context.Context, req manifestreader.GetRequest) (manifestreader.Object, error) {
	if s.defect != defectIgnoresContext && ctx.Err() != nil {
		return manifestreader.Object{}, ctx.Err()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	failure := s.fault(req.Key)
	if failure != nil {
		return manifestreader.Object{}, failure
	}

	version, ok := s.find(req.Key, req.VersionID)
	if !ok {
		if s.defect == defectMissingIsEmpty {
			return manifestreader.Object{}, nil
		}

		return manifestreader.Object{}, manifestreader.ErrNotFound
	}

	etag := s.etagOf(req.Key, req.VersionID, &version)

	if s.notModifiedFirst && req.ETag != "" && req.ETag == etag {
		return manifestreader.Object{}, manifestreader.ErrNotModified
	}

	if s.tooLarge(version.content, req.Limit) {
		return manifestreader.Object{}, manifestreader.ErrTooLarge
	}

	return s.answer(version.content, etag, req.ETag)
}

// waitHangs holds the read in the hangs of the key. A toy with the partial defect returns the bytes it had.
func (s *toyStore) waitHangs(ctx context.Context, key string) (manifestreader.Object, error) {
	hang, body := s.hangsOf(key)

	for _, held := range []*storetest.Hang{hang, body} {
		if held == nil {
			continue
		}

		err := s.block(ctx, held)
		if err != nil && s.defect == defectPartialOnCancel {
			return manifestreader.Object{Bytes: []byte("part")}, err
		}

		if err != nil {
			return manifestreader.Object{}, err
		}
	}

	return manifestreader.Object{}, nil
}

// admitRead takes the one slot of a toy that admits one read at a time; the queue ends with the context.
func (s *toyStore) admitRead(ctx context.Context) (func(), error) {
	if s.admit == nil {
		return func() {}, nil
	}

	select {
	case s.admit <- struct{}{}:
		return func() { <-s.admit }, nil
	case <-ctx.Done():
		return func() {}, fmt.Errorf("toy queue: %w", ctx.Err())
	}
}

// startWorkers starts the goroutines that some toys leave behind, or that end with the context.
func (s *toyStore) startWorkers(ctx context.Context) {
	switch {
	case s.defect == defectLeaks, s.defect == defectLeaksAndFails:
		go func() { <-s.release }()
	case s.defect == defectLeaksUnderOwnDo:
		pprof.Do(ctx, pprof.Labels("toy", "own"), func(context.Context) {
			go func() { <-s.release }()
		})
	case s.ctxWorker:
		go func() { <-ctx.Done() }()
	}
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

// find returns the current version of the key, or the named one.
func (s *toyStore) find(key, versionID string) (toyVersion, bool) {
	versions := s.versions[key]
	if len(versions) == 0 {
		return toyVersion{}, false
	}

	if versionID == "" || s.defect == defectIgnoresVersion {
		return versions[len(versions)-1], true
	}

	for _, version := range versions {
		if version.id == versionID {
			return version, true
		}
	}

	return toyVersion{}, false
}

func (s *toyStore) etagOf(key, versionID string, version *toyVersion) string {
	if versionID == "" {
		return s.etags[key]
	}

	if s.generationETags || s.literalETags {
		return version.etag
	}

	return toyETag(version.content)
}

// generation is the ETag of the n-th version of a key (from zero) for the toys with generation numbers. The first
// ones of a toy with literal ETags are words that other cases use as made-up tokens.
func (s *toyStore) generation(index int) string {
	words := []string{`"stale"`, `"not-an-etag"`, `"0000000000000000"`}
	if s.literalETags && index < len(words) {
		return words[index]
	}

	return `"gen-` + strconv.Itoa(index+1) + `"`
}

// updateETag sets the ETag of the key after a write, as far as the defect of the toy lets it.
func (s *toyStore) updateETag(key string, had bool, old, content []byte) {
	switch {
	case s.defect == defectConstantETag:
		s.etags[key] = `"constant"`
	case s.defect == defectStaleETag && had:
		// the ETag of the first content stays
	case s.defect == defectETagOfOldBytes && had:
		s.etags[key] = toyETag(old)
	case s.generationETags || s.literalETags:
		s.etags[key] = s.generation(len(s.versions[key]) - 1)
	default:
		s.etags[key] = toyETag(content)
	}
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

	if s.nilEmpty && len(content) == 0 {
		content = nil
	}

	return manifestreader.Object{Bytes: content, ETag: etag}, nil
}

// block holds the read in the hang. The toy with the one-shot defect copies the first Deadline() of the context
// into a deadline that it never looks at again, as a store does that hands the deadline to the I/O it starts, and
// ignores Done: Done is authoritative, so the suite has to reject it.
func (s *toyStore) block(ctx context.Context, hang *storetest.Hang) error {
	if s.defect != defectOneShotDeadline {
		return hang.Block(ctx)
	}

	deadline, hasDeadline := ctx.Deadline() // read once

	inner, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()

	go func() {
		for inner.Err() == nil {
			if hasDeadline && clock.Now().After(deadline) {
				cancel()

				return
			}

			runtime.Gosched()
		}
	}()

	err := hang.Block(inner)
	if err != nil {
		return fmt.Errorf("toy: %w", context.DeadlineExceeded)
	}

	return nil
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
		name   string
		defect string
		// wants names each case that must fail for the defect and the identifier of the assertion it must fail
		// on. The identifier occurs once in the source of the suite (TestFailureIdentifiersAreUnique), so no other
		// failure of the case can satisfy the row, and a failure of the setup of the case does not either.
		wants []caseWant
		// within is the bound of the waits for a read to return after its context ended, for a store that fails
		// by never answering (0: the default)
		within time.Duration
	}{
		{
			name: "the ETag of a rewrite stays the old one", defect: defectStaleETag,
			wants: []caseWant{{caseChanged, idChanged}, {caseFollows, idFollows}},
		},
		{
			name: "the ETag is the same for every content", defect: defectConstantETag,
			wants: []caseWant{
				{caseChanged, idChanged}, {caseFollows, idFollows},
				{"empty-object", "[empty-etag-differs]"}, {caseDescribes, idTwoContents},
			},
		},
		{
			name: "the limit is ignored", defect: defectIgnoresLimit,
			wants: []caseWant{
				{"limit-at-and-one-over", "[limit-boundary-too-large]"},
				{"limit-zero-and-negative", idNonPositive}, {"empty-object", "[empty-limit-zero-too-large]"},
			},
		},
		{
			name: "a negative limit lets everything through", defect: defectNegativeLimit,
			wants: []caseWant{{"limit-zero-and-negative", idNonPositive}},
		},
		{
			name:   "any ETag is answered with not-modified",
			defect: defectNotModifiedAlways,
			wants: []caseWant{
				{"unknown-etag-returns-the-object", "[unknown-etag-returns-object]"},
				{caseChanged, idChanged},
			},
		},
		{
			name: "a body comes with the not-modified answer", defect: defectBodyWithNotMod,
			wants: []caseWant{
				{"conditional-unchanged-is-not-modified", idNoObject},
				{"etag-is-stable-for-unchanged-content", idNoObject},
			},
		},
		{
			name: "the context is ignored", defect: defectIgnoresContext,
			wants: []caseWant{{"ended-context-is-an-error", "[ended-context-error]"}},
		},
		{
			name: "the body is one byte short", defect: defectTruncates,
			wants: []caseWant{{caseGetUncond, "[unconditional-body]"}, {caseChanged, "[changed-new-body]"}},
		},
		{
			name: "every Get leaves a goroutine", defect: defectLeaks,
			wants: []caseWant{{"gets-leave-no-goroutine-behind", idLeak}, {caseGetUncond, idLeak}},
		},
		{
			name: "every Get leaves a goroutine and fails, so that the case ends at once", defect: defectLeaksAndFails,
			wants: []caseWant{{caseGetUncond, idLeak}},
		},
		{
			name:   "a goroutine started under the pprof.Do of the store from the request context",
			defect: defectLeaksUnderOwnDo,
			wants:  []caseWant{{caseGetUncond, idLeak}},
		},
		{
			name: "returned bytes are overwritten by a later Put", defect: defectAliases,
			wants: []caseWant{{"returned-bytes-are-not-aliased", "[bytes-not-aliased]"}},
		},
		{
			name: "a version that does not exist is served", defect: defectIgnoresVersion,
			wants: []caseWant{
				{"missing-version-is-not-found", "[missing-version-not-found]"},
				{"version-id-reads-an-older-version", "[older-version-bytes]"},
			},
		},
		{
			name: "a denied read is retryable", defect: defectDeniedRetryable,
			wants: []caseWant{{"denied-is-not-retryable", "[fault-retryable]"}},
		},
		{
			name: "a missing object is an empty one", defect: defectMissingIsEmpty,
			wants: []caseWant{{"missing-is-not-found", "[missing-never-written]"}},
		},
		{
			name:   "a store copies the first Deadline() into a one-shot deadline and ignores Done",
			defect: defectOneShotDeadline,
			wants: []caseWant{
				{"deadline-while-reading", idNoReturn}, {"cancel-while-reading", idNoReturn},
			},
			within: shortBound,
		},
		{
			name: "a store keeps serving the first content after a later write", defect: defectServesStale,
			wants: []caseWant{{caseDescribes, "[read-after-write]"}, {caseChanged, idChanged}},
		},
		{
			name: "a cancelled read returns the bytes it had", defect: defectPartialOnCancel,
			wants: []caseWant{
				{"cancel-while-reading", idNoObject}, {"cancel-while-reading-the-body", idNoObject},
				{"deadline-while-reading", idNoObject},
			},
		},
		{
			name: "the ETag is the one of the previous bytes", defect: defectETagOfOldBytes,
			wants: []caseWant{{caseDescribes, idTwoContents}, {caseFollows, idFollows}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, want := range tt.wants {
				fixture, toy := toyFixture(tt.defect)

				failures, ran := storetest.CheckWithin(want.name, fixture, cmpOr(tt.within, defaultBound))

				close(toy.release)

				require.True(t, ran, "case %s did not run", want.name)

				text := strings.Join(failures, "\n")
				assert.Contains(t, text, want.text, "the case %s did not fail for %q as it must", want.name, tt.defect)
				assert.NotContains(
					t,
					text,
					idSetup,
					"the case %s failed in its setup, not for %q",
					want.name,
					tt.defect,
				)
			}
		})
	}
}

// caseWant is a case that must fail for a defect, with the identifier of the assertion it must fail on.
type caseWant struct {
	name string
	text string
}

// Identifiers that several rows use.
const (
	idChanged     = "[changed-read-after-rewrite]"
	idFollows     = "[follows-rewrite-seen]"
	idTwoContents = "[etag-two-contents]"
	idNonPositive = "[limit-nonpositive-too-large]"
	idNoObject    = "[failed-read-no-object]"
	idLeak        = "[left-goroutines-behind]"
	idNoReturn    = "[read-did-not-return]"
	// idSetup is the failure of a case whose read never reached the store: no row accepts it.
	idSetup = "[read-did-not-enter]"
)

// TestFailureIdentifiersAreUnique: the identifier a row looks for occurs once in the source of the suite, in the
// assertion it names. A fragment of ordinary text (or of the text of testify) could also come from another
// failure of the case, so no row uses one.
func TestFailureIdentifiersAreUnique(t *testing.T) {
	t.Parallel()

	var builder strings.Builder

	for _, name := range []string{"cases.go", "storetest.go"} {
		data, err := os.ReadFile(name)
		require.NoError(t, err)

		builder.Write(data)
	}

	source := builder.String()

	ids := map[string]bool{idSetup: true}

	table, err := os.ReadFile("toy_test.go")
	require.NoError(t, err)

	for _, match := range regexp.MustCompile(`"(\[[a-z0-9-]+\])"`).FindAllStringSubmatch(string(table), -1) {
		ids[match[1]] = true
	}

	require.NotEmpty(t, ids)

	for id := range ids {
		assert.Equal(t, 1, strings.Count(source, id), "the identifier %s occurs once in the suite", id)
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

// TestSuiteAcceptsConformingToys: stores that the suite must not reject, each with a property that an earlier
// version of a case assumed away: ETags that are per-key generation numbers, an empty object returned as nil
// bytes, a backend that admits one read at a time with a queue the context ends, and a worker per read that ends
// with the context. Every case that the store does not waive runs and passes.
func TestSuiteAcceptsConformingToys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		configure   func(*toyStore)
		name        string
		wantNotRun  []string
		waiveHashed bool
	}{
		{
			name: "ETags are per-key generation numbers", configure: func(s *toyStore) { s.generationETags = true },
			waiveHashed: true, wantNotRun: []string{"etag-is-content-addressed", caseKeys},
		},
		{
			name: "the empty object is nil bytes", configure: func(s *toyStore) { s.nilEmpty = true },
			wantNotRun: []string{caseKeys},
		},
		{
			name: "one read at a time with a queue that the context ends",
			configure: func(s *toyStore) {
				s.admit = make(chan struct{}, 1)
			},
			wantNotRun: []string{caseKeys},
		},
		{
			name: "a worker per read that ends with the context", configure: func(s *toyStore) { s.ctxWorker = true },
			wantNotRun: []string{caseKeys},
		},
		{
			name: "the first ETags of a key are words that other cases use as made-up tokens",
			configure: func(s *toyStore) {
				s.literalETags = true
				s.notModifiedFirst = true
			},
			waiveHashed: true, wantNotRun: []string{"etag-is-content-addressed", caseKeys},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				notRun []string
				failed []string
			)

			for _, name := range storetest.CaseNames() {
				fixture, toy := toyFixture(defectNone)
				tt.configure(toy)

				if tt.waiveHashed {
					fixture.Unsupported = toyWaivers().With(storetest.CapContentAddressedETag, "the ETags are generation numbers")
				}

				failures, ran := storetest.Check(name, fixture)

				close(toy.release)

				if !ran {
					notRun = append(notRun, name)

					continue
				}

				if len(failures) > 0 {
					failed = append(failed, name+": "+strings.Join(failures, "\n"))
				}
			}

			assert.Empty(t, failed, "cases that failed for a conforming store")
			assert.Equal(t, tt.wantNotRun, notRun)
		})
	}
}
