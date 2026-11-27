package storetest

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

const (
	// bigLimit is a limit no object of the suite reaches, unless a case says so.
	bigLimit = 1 << 22
	// wait bounds every wait of a case: a missing event fails the case instead of hanging it.
	wait = 10 * time.Second

	// The generated contents of the ETag property: a fixed seed, so every run is the same.
	propertySeed, propertySeed2 = 7, 11
	propertyRounds              = 12
	maxPropertySize             = 200
	baseSeed                    = 100
	largeSize                   = 3<<20 + 123
	bodyHangSize                = 256 << 10
	repeatTen                   = 10
	halves                      = 2
	leakGets                    = 20
	longKeyLen                  = 5000
	bodySeed                    = 2

	keyMain  = "conformance/dir/object.json"
	keyOther = "conformance/dir/other.json"
	keyThird = "conformance/third.json"
)

// manualDeadline is a context whose deadline passes when the test says so: it ends with
// [context.DeadlineExceeded] without a timer and without reading the clock, so "the deadline passes while the
// store is reading" does not depend on the speed of the machine.
type manualDeadline struct {
	context.Context //nolint:containedctx // it wraps a context to change how it ends

	done chan struct{}
	once sync.Once
}

func newManualDeadline(parent context.Context) *manualDeadline {
	return &manualDeadline{Context: parent, done: make(chan struct{})}
}

func (m *manualDeadline) Done() <-chan struct{} { return m.done }

// deadlineInstant is the one deadline the context reports for its whole life, as a context must: a fixed instant,
// far ahead. The test signals that the deadline has passed through Done and Err, as a timer would, and a store that
// copies Deadline() once into an I/O deadline and ignores Done never sees it.
const deadlineInstant = 1 << 40

// Deadline implements [context.Context]: the same result on every call.
func (*manualDeadline) Deadline() (time.Time, bool) { return time.Unix(deadlineInstant, 0), true }

func (m *manualDeadline) Err() error {
	select {
	case <-m.done:
		return context.DeadlineExceeded
	default:
		return m.Context.Err() //nolint:wrapcheck // the error of the wrapped context, as it is
	}
}

// expire makes the deadline pass.
func (m *manualDeadline) expire() { m.once.Do(func() { close(m.done) }) }

// CaseNeeds returns the capabilities that the named case needs, and false when there is no such case.
func CaseNeeds(name string) ([]Capability, bool) {
	for _, current := range allCases() {
		if current.name == name {
			return current.needs, true
		}
	}

	return nil, false
}

func allCases() []contractCase {
	return []contractCase{
		{name: "get-unconditional", run: caseGetUnconditional},
		{name: "conditional-unchanged-is-not-modified", run: caseConditionalUnchanged},
		{name: "conditional-changed-returns-new-body-and-etag", run: caseConditionalChanged},
		{name: "unknown-etag-returns-the-object", run: caseUnknownETag},
		{name: "missing-is-not-found", run: caseMissing},
		{name: "missing-version-is-not-found", run: caseMissingVersion},
		{name: "version-id-reads-an-older-version", run: caseVersionID, needs: []Capability{CapVersions}},
		{name: "denied-is-not-retryable", run: caseDenied, needs: []Capability{CapDeny}},
		{name: "transient-is-retryable", run: caseTransient, needs: []Capability{CapTransient}},
		{name: "throttled-is-retryable-and-throttled", run: caseThrottled, needs: []Capability{CapThrottle}},
		{name: "limit-at-and-one-over", run: caseLimitBoundary},
		{name: "limit-zero-and-negative", run: caseLimitZeroAndNegative},
		{name: "conditional-over-limit-never-returns-a-body", run: caseConditionalOverLimit},
		{name: "empty-object", run: caseEmptyObject},
		{name: "large-object-near-the-limit", run: caseLargeObject},
		{name: "key-characters", run: caseKeyCharacters},
		{
			name:  "key-characters-reserved-by-some-file-systems",
			run:   caseReservedNameChars,
			needs: []Capability{CapReservedNameChars},
		},
		{name: "odd-keys-never-return-content", run: caseOddKeys},
		{name: "unclean-keys-are-refused", run: caseKeyRefusal, needs: []Capability{CapKeyValidation}},
		{name: "object-and-child-keys-coexist", run: caseNestedKeys, needs: []Capability{CapNestedKeys}},
		{name: "etag-is-stable-for-unchanged-content", run: caseETagStable},
		{name: "etag-follows-content", run: caseETagFollowsContent},
		{
			name:  "etag-is-content-addressed",
			run:   caseETagContentAddressed,
			needs: []Capability{CapContentAddressedETag},
		},
		{name: "etag-describes-the-bytes-returned", run: caseETagDescribesBytes},
		{name: "returned-bytes-are-not-aliased", run: caseNotAliased},
		{name: "ended-context-is-an-error", run: caseEndedContext},
		{name: "cancel-while-reading", run: caseCancelWhileReading, needs: []Capability{CapHang}},
		{name: "deadline-while-reading", run: caseDeadlineWhileReading, needs: []Capability{CapHang}},
		{name: "cancel-while-reading-the-body", run: caseCancelWhileReadingBody, needs: []Capability{CapHangBody}},
		{name: "cancel-leaves-nothing-behind", run: caseNoLeakAfterCancel, needs: []Capability{CapHang}},
		{name: "gets-leave-no-goroutine-behind", run: caseGetsLeaveNothing},
		{name: "concurrent-gets", run: caseConcurrentGets},
	}
}

// get reads and, for every error, also checks that no object came with it: a failed read returns a zero Object.
func get(ctx context.Context, t TB, fixture Fixture, key, etag string, limit int64) (manifestreader.Object, error) {
	t.Helper()

	return getRequest(ctx, t, fixture, manifestreader.GetRequest{Key: key, ETag: etag, Limit: limit})
}

func getRequest(
	ctx context.Context, t TB, fixture Fixture, req manifestreader.GetRequest,
) (manifestreader.Object, error) {
	t.Helper()

	obj, err := fixture.Store.Get(ctx, req)
	if err != nil {
		assert.Equal(t, manifestreader.Object{}, obj, "[failed-read-no-object] a failed read returns no object")

		return obj, fmt.Errorf("storetest: get %q: %w", req.Key, err)
	}

	return obj, nil
}

// mustGet reads key and requires success.
func mustGet(
	t TB, fixture Fixture, key, etag string, limit int64, msgAndArgs ...any,
) manifestreader.Object {
	t.Helper()

	obj, err := get(t.Context(), t, fixture, key, etag, limit)
	require.NoError(t, err, msgAndArgs...)

	return obj
}

func mustPut(t TB, fixture Fixture, key string, content []byte) {
	t.Helper()
	require.NoError(t, fixture.Backend.Put(key, content))
}

// sameBytes compares bodies by content: nil and empty are the same body.
func sameBytes(t TB, want, got []byte, msgAndArgs ...any) {
	t.Helper()

	if !bytes.Equal(want, got) {
		assert.Fail(
			t,
			fmt.Sprintf("the bodies differ: want %d bytes %q, got %d bytes %q", len(want), want, len(got), got),
			msgAndArgs...)
	}
}

// wantFetchError asserts the classification of a failed read.
func wantFetchError(t TB, err error, reason string, retryable, throttled bool) {
	t.Helper()

	fetchErr, ok := errors.AsType[*manifestreader.FetchError](err)
	require.True(t, ok, "want a *FetchError, got %T: %v", err, err)
	assert.Equal(t, reason, fetchErr.Reason)
	assert.Equal(t, retryable, fetchErr.Retryable, "[fault-retryable] Retryable")
	assert.Equal(t, throttled, fetchErr.Throttled, "Throttled")
	require.NotErrorIs(t, err, manifestreader.ErrNotFound)
	require.NotErrorIs(t, err, manifestreader.ErrNotModified)
	require.NotErrorIs(t, err, manifestreader.ErrTooLarge)
}

func caseGetUnconditional(t TB, fixture Fixture) {
	t.Helper()

	content := []byte(`{"flag":true}`)
	mustPut(t, fixture, keyMain, content)

	obj := mustGet(t, fixture, keyMain, "", bigLimit)
	sameBytes(t, content, obj.Bytes, "[unconditional-body] the body of an unconditional read")
	assert.NotEmpty(t, obj.ETag)
}

func caseConditionalUnchanged(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, keyMain, []byte(`{"flag":true}`))
	first := mustGet(t, fixture, keyMain, "", bigLimit)

	obj, err := get(t.Context(), t, fixture, keyMain, first.ETag, bigLimit)
	require.ErrorIs(t, err, manifestreader.ErrNotModified)
	assert.Equal(t, manifestreader.Object{}, obj, "a not-modified answer carries no body and no ETag")
}

func caseConditionalChanged(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, keyMain, []byte(`{"flag":true}`))
	first := mustGet(t, fixture, keyMain, "", bigLimit)

	changed := []byte(`{"flag":false,"more":1}`)
	mustPut(t, fixture, keyMain, changed)

	second := mustGet(t, fixture, keyMain, first.ETag, bigLimit, "[changed-read-after-rewrite]")
	sameBytes(t, changed, second.Bytes, "[changed-new-body] the new body")
	assert.NotEmpty(t, second.ETag)
	assert.NotEqual(t, first.ETag, second.ETag)

	_, err := get(t.Context(), t, fixture, keyMain, second.ETag, bigLimit)
	require.ErrorIs(t, err, manifestreader.ErrNotModified, "the new ETag is current")
}

// staleETags writes the contents in turn to keyMain and returns the ETag of each, then writes content and returns
// its ETag. The stale ETags belong to bytes that were once at the key, so none of them is a made-up token that a
// store could happen to use as an ETag of its own.
func staleETags(t TB, fixture Fixture, earlier [][]byte, content []byte) ([]string, string) {
	t.Helper()

	stale := make([]string, 0, len(earlier))

	for _, old := range earlier {
		mustPut(t, fixture, keyMain, old)
		stale = append(stale, mustGet(t, fixture, keyMain, "", bigLimit).ETag)
	}

	mustPut(t, fixture, keyMain, content)

	current := mustGet(t, fixture, keyMain, "", bigLimit).ETag

	for _, etag := range stale {
		require.NotEqual(t, current, etag, "an ETag of other content at the key is not the current ETag")
	}

	return stale, current
}

func caseUnknownETag(t TB, fixture Fixture) {
	t.Helper()

	content := []byte(`{"flag":true}`)
	stale, _ := staleETags(t, fixture, [][]byte{[]byte("first"), []byte(`{"flag":false}`)}, content)

	for _, etag := range stale {
		obj := mustGet(t, fixture, keyMain, etag, bigLimit, "[unknown-etag-returns-object]")
		sameBytes(t, content, obj.Bytes, etag)
	}
}

func caseMissing(t TB, fixture Fixture) {
	t.Helper()

	obj, err := get(t.Context(), t, fixture, keyMain, "", bigLimit)
	require.ErrorIs(t, err, manifestreader.ErrNotFound, "[missing-never-written]")
	assert.Equal(t, manifestreader.Object{}, obj)

	mustPut(t, fixture, keyMain, []byte("x"))
	etag := mustGet(t, fixture, keyMain, "", bigLimit).ETag
	require.NoError(t, fixture.Backend.Delete(keyMain))

	_, err = get(t.Context(), t, fixture, keyMain, "", bigLimit)
	require.ErrorIs(t, err, manifestreader.ErrNotFound, "deleted")

	_, err = get(t.Context(), t, fixture, keyMain, etag, bigLimit)
	require.ErrorIs(t, err, manifestreader.ErrNotFound, "a deleted object is not a not-modified one")

	_, err = get(t.Context(), t, fixture, "conformance/no/such/dir/object.json", "", bigLimit)
	require.ErrorIs(t, err, manifestreader.ErrNotFound, "a missing directory on the path")
}

func caseMissingVersion(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, keyMain, []byte("x"))

	_, err := getRequest(t.Context(), t, fixture, manifestreader.GetRequest{
		Key: keyMain, VersionID: "no-such-version", Limit: bigLimit,
	})
	require.ErrorIs(t, err, manifestreader.ErrNotFound, "[missing-version-not-found]")
}

func caseVersionID(t TB, fixture Fixture) {
	t.Helper()

	first, second := []byte(`{"version":1}`), []byte(`{"version":"two","longer":true}`)

	id1, err := fixture.Backend.PutVersion(keyMain, first)
	require.NoError(t, err)

	id2, err := fixture.Backend.PutVersion(keyMain, second)
	require.NoError(t, err)

	require.NotEmpty(t, id1)
	require.NotEqual(t, id1, id2)

	byVersion := func(id, etag string) (manifestreader.Object, error) {
		return getRequest(t.Context(), t, fixture, manifestreader.GetRequest{
			Key: keyMain, VersionID: id, ETag: etag, Limit: bigLimit,
		})
	}

	old, err := byVersion(id1, "")
	require.NoError(t, err)
	sameBytes(t, first, old.Bytes, "[older-version-bytes] the older version is read after the overwrite")

	current, err := byVersion(id2, "")
	require.NoError(t, err)
	sameBytes(t, second, current.Bytes)
	assert.NotEqual(t, old.ETag, current.ETag)

	sameBytes(t, second, mustGet(t, fixture, keyMain, "", bigLimit).Bytes, "no version id: the current one")

	_, err = byVersion(id1, old.ETag)
	require.ErrorIs(t, err, manifestreader.ErrNotModified, "an older version is unchanged for its own ETag")

	again, err := byVersion(id1, current.ETag)
	require.NoError(t, err)
	sameBytes(t, first, again.Bytes, "the ETag of another version does not make the version unchanged")
}

// faultCase arranges fault for keyMain, checks the classification of an unconditional and of a conditional read
// and that the store recovers when the fault is cleared.
func faultCase(t TB, fixture Fixture, fault Fault, reason string, retryable, throttled bool) {
	t.Helper()

	content := []byte(`{"flag":true}`)
	mustPut(t, fixture, keyMain, content)
	etag := mustGet(t, fixture, keyMain, "", bigLimit).ETag

	require.NoError(t, fixture.Backend.Fail(keyMain, fault))

	_, err := get(t.Context(), t, fixture, keyMain, "", bigLimit)
	require.Error(t, err)
	wantFetchError(t, err, reason, retryable, throttled)

	_, err = get(t.Context(), t, fixture, keyMain, etag, bigLimit)
	require.Error(t, err)
	wantFetchError(t, err, reason, retryable, throttled)

	// another key is not affected
	mustPut(t, fixture, keyOther, []byte("other"))
	sameBytes(t, []byte("other"), mustGet(t, fixture, keyOther, "", bigLimit).Bytes)

	require.NoError(t, fixture.Backend.Fail(keyMain, FaultNone))

	sameBytes(t, content, mustGet(t, fixture, keyMain, "", bigLimit).Bytes, "recovered")

	_, err = get(t.Context(), t, fixture, keyMain, etag, bigLimit)
	require.ErrorIs(t, err, manifestreader.ErrNotModified, "recovered, and the ETag is still the one of the content")
}

func caseDenied(t TB, fixture Fixture) {
	t.Helper()

	faultCase(t, fixture, FaultDenied, manifestreader.ReasonDenied, false, false)
}

func caseTransient(t TB, fixture Fixture) {
	t.Helper()

	faultCase(t, fixture, FaultTransient, manifestreader.ReasonError, true, false)
}

func caseThrottled(t TB, fixture Fixture) {
	t.Helper()

	faultCase(t, fixture, FaultThrottled, manifestreader.ReasonError, true, true)
}

func caseLimitBoundary(t TB, fixture Fixture) {
	t.Helper()

	content := bytes.Repeat([]byte("0123456789"), repeatTen)
	mustPut(t, fixture, keyMain, content)

	size := int64(len(content))
	tests := []struct {
		name  string
		limit int64
		want  bool
	}{
		{name: "exactly at the size", limit: size, want: true},
		{name: "one under", limit: size - 1},
		{name: "one over", limit: size + 1, want: true},
		{name: "half", limit: size / halves},
	}

	for _, tt := range tests {
		obj, err := get(t.Context(), t, fixture, keyMain, "", tt.limit)
		if tt.want {
			require.NoError(t, err, tt.name)
			sameBytes(t, content, obj.Bytes, tt.name)

			continue
		}

		require.ErrorIs(t, err, manifestreader.ErrTooLarge, "[limit-boundary-too-large] "+tt.name)
	}
}

// caseLimitZeroAndNegative defines the limits that are not positive: zero and a negative limit both admit the
// empty object and nothing else.
func caseLimitZeroAndNegative(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, keyMain, []byte("x"))
	mustPut(t, fixture, keyOther, []byte{})

	limits := []int64{0, -1, -1000, math.MinInt64}

	for _, limit := range limits {
		_, err := get(t.Context(), t, fixture, keyMain, "", limit)
		require.ErrorIs(t, err, manifestreader.ErrTooLarge, "[limit-nonpositive-too-large] limit %d, one byte", limit)

		empty := mustGet(t, fixture, keyOther, "", limit)
		assert.Empty(t, empty.Bytes, "limit %d, empty object", limit)
		assert.NotEmpty(t, empty.ETag, "limit %d, empty object", limit)

		_, err = get(t.Context(), t, fixture, keyOther, empty.ETag, limit)
		require.ErrorIs(t, err, manifestreader.ErrNotModified, "limit %d, empty object, current ETag", limit)
	}
}

// caseConditionalOverLimit: with a limit below the size and the current ETag, a store may answer "not modified"
// or "too large" (it depends on whether it looks at the size first), never with a body.
func caseConditionalOverLimit(t TB, fixture Fixture) {
	t.Helper()

	content := bytes.Repeat([]byte("0123456789"), repeatTen)
	stale, etag := staleETags(t, fixture, [][]byte{[]byte("other content")}, content)

	obj, err := get(t.Context(), t, fixture, keyMain, etag, int64(len(content))-1)
	require.Error(t, err)
	assert.True(t,
		errors.Is(err, manifestreader.ErrNotModified) || errors.Is(err, manifestreader.ErrTooLarge),
		"want not-modified or too-large, got %v", err)
	assert.Equal(t, manifestreader.Object{}, obj, "never a body")

	_, err = get(t.Context(), t, fixture, keyMain, stale[0], int64(len(content))-1)
	require.ErrorIs(t, err, manifestreader.ErrTooLarge, "another ETag: the object is read and it is too large")
}

func caseEmptyObject(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, keyMain, []byte{})
	mustPut(t, fixture, keyOther, []byte("x"))

	for _, limit := range []int64{0, 1, bigLimit} {
		obj := mustGet(t, fixture, keyMain, "", limit)
		assert.Empty(t, obj.Bytes, "limit %d", limit)
		assert.NotEmpty(t, obj.ETag, "limit %d", limit)
	}

	empty := mustGet(t, fixture, keyMain, "", bigLimit)

	_, err := get(t.Context(), t, fixture, keyMain, empty.ETag, 0)
	require.ErrorIs(t, err, manifestreader.ErrNotModified)

	_, err = get(t.Context(), t, fixture, keyOther, "", 0)
	require.ErrorIs(t, err, manifestreader.ErrTooLarge, "[empty-limit-zero-too-large] one byte over a limit of zero")

	// the empty body and another body at the same key have different ETags
	mustPut(t, fixture, keyMain, []byte("x"))

	other := mustGet(t, fixture, keyMain, "", bigLimit)
	assert.NotEqual(t, empty.ETag, other.ETag, "[empty-etag-differs] the empty object has an ETag of its own")
}

func pseudoRandom(seed uint64, size int) []byte {
	var key [32]byte

	binary.LittleEndian.PutUint64(key[:], seed)

	out := make([]byte, size)
	_, _ = rand.NewChaCha8(key).Read(out) // the same bytes for the same seed, on every run

	return out
}

func caseLargeObject(t TB, fixture Fixture) {
	t.Helper()

	const size = largeSize

	content := pseudoRandom(1, size)
	mustPut(t, fixture, keyMain, content)

	obj := mustGet(t, fixture, keyMain, "", size)
	sameBytes(t, content, obj.Bytes, "the 3 MiB body is returned whole at a limit equal to its size")

	_, err := get(t.Context(), t, fixture, keyMain, "", size-1)
	require.ErrorIs(t, err, manifestreader.ErrTooLarge)

	_, err = get(t.Context(), t, fixture, keyMain, obj.ETag, size)
	require.ErrorIs(t, err, manifestreader.ErrNotModified)
}

// caseKeyCharacters puts one object per key, with content of its own, and reads each back: the characters that
// URLs, shells and file systems treat specially must name distinct objects.
func caseKeyCharacters(t TB, fixture Fixture) {
	t.Helper()

	keyCharacters(t, fixture, []string{
		"conformance/with space.json", "conformance/\u00fcn\u00ef/\u00e9.json", "conformance/a+b=c&d.json",
		"conformance/deep/er/est/x.json", "conformance/.hidden", "conformance/dots..in..name", "top-level.json",
		"conformance/hash#tag.json", "conformance/percent%.json", "conformance/p%2Fx.json", "conformance/p/x.json",
		"conformance/a b", "conformance/a+b", ".leading-dot", "conformance/%41", "conformance/A",
	})
}

// caseReservedNameChars: the characters that some file systems reserve are valid in the keys of others.
func caseReservedNameChars(t TB, fixture Fixture) {
	t.Helper()

	keyCharacters(t, fixture, []string{"conformance/what?.json", "conformance/colon:name", "conformance/what.json"})
}

func keyCharacters(t TB, fixture Fixture, keys []string) {
	t.Helper()

	for i, key := range keys {
		mustPut(t, fixture, key, []byte("content-"+strconv.Itoa(i)))
	}

	for i, key := range keys {
		obj, err := get(t.Context(), t, fixture, key, "", bigLimit)
		require.NoError(t, err, key)
		sameBytes(t, []byte("content-"+strconv.Itoa(i)), obj.Bytes, key)
	}
}

// oddKeys are keys that must never be answered with the content of another object.
func oddKeys() []string {
	return []string{
		"",
		"/conformance/real.json",
		"../outside",
		"conformance/../../outside2",
		"conformance//double",
		".",
	}
}

func caseOddKeys(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, "conformance/real.json", []byte("real"))

	for _, key := range oddKeys() {
		_, err := get(t.Context(), t, fixture, key, "", bigLimit)
		require.Error(t, err, "key %q", key)

		_, isFetch := errors.AsType[*manifestreader.FetchError](err)
		assert.True(t, isFetch || errors.Is(err, manifestreader.ErrNotFound), "key %q: %T %v", key, err, err)
	}
}

func caseKeyRefusal(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, "conformance/real.json", []byte("real"))
	sameBytes(t, []byte("real"), mustGet(t, fixture, "conformance/real.json", "", bigLimit).Bytes)

	refused := []string{
		"", ".", "..", "/conformance/real.json", "conformance/../conformance/real.json", "./conformance/real.json",
		"conformance//real.json", "conformance/real.json/", "conformance\\real.json", "conformance/real.json\x00x",
		"../conformance/real.json", "conformance/./real.json",
		"conformance/" + string(bytes.Repeat([]byte("n"), longKeyLen)),
	}

	for _, key := range refused {
		_, err := get(t.Context(), t, fixture, key, "", bigLimit)
		require.Error(t, err, "key %q", key)
		wantFetchError(t, err, manifestreader.ReasonDenied, false, false)
	}
}

func caseNestedKeys(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, "conformance/node", []byte("the object"))
	mustPut(t, fixture, "conformance/node/child", []byte("the child"))

	sameBytes(t, []byte("the object"), mustGet(t, fixture, "conformance/node", "", bigLimit).Bytes)
	sameBytes(t, []byte("the child"), mustGet(t, fixture, "conformance/node/child", "", bigLimit).Bytes)

	require.NoError(t, fixture.Backend.Delete("conformance/node/child"))
	sameBytes(t, []byte("the object"), mustGet(t, fixture, "conformance/node", "", bigLimit).Bytes)
}

func caseETagStable(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, keyMain, []byte(`{"flag":true}`))
	first := mustGet(t, fixture, keyMain, "", bigLimit)

	for range 3 {
		again := mustGet(t, fixture, keyMain, "", bigLimit)
		assert.Equal(t, first.ETag, again.ETag, "the ETag of unchanged content is stable")
		sameBytes(t, first.Bytes, again.Bytes)

		_, err := get(t.Context(), t, fixture, keyMain, first.ETag, bigLimit)
		require.ErrorIs(t, err, manifestreader.ErrNotModified)
	}
}

// bitsPerByte is the number of single-bit masks of a byte.
const bitsPerByte = 8

// randomBit is a byte with one bit set.
func randomBit(rng *rand.Rand) byte { return byte(1) << rng.IntN(bitsPerByte) }

// mutations returns contents that differ from content in one way each, and are never equal to it.
func mutations(rng *rand.Rand, content []byte) [][]byte {
	out := [][]byte{append(bytes.Clone(content), randomBit(rng))}

	if len(content) > 0 {
		flipped := bytes.Clone(content)
		flipped[rng.IntN(len(flipped))] ^= randomBit(rng)

		other := bytes.Clone(content)
		for i := range other {
			other[i] = ^other[i] // every byte differs, the length is the same
		}

		out = append(out, flipped, other, bytes.Clone(content[:len(content)-1]), []byte{})
	}

	return out
}

// generated returns the contents of the ETag property: each with the contents that differ from it.
func generated() [][2][]byte {
	source := rand.NewPCG(propertySeed, propertySeed2)
	rng := rand.New(source) //nolint:gosec // a fixed seed: the same cases on every run

	var out [][2][]byte

	for iteration := range propertyRounds {
		original := pseudoRandom(uint64(iteration)+baseSeed, rng.IntN(maxPropertySize))
		for _, changed := range mutations(rng, original) {
			out = append(out, [2][]byte{original, changed})
		}
	}

	return out
}

// caseETagFollowsContent is the property: for generated contents, a rewrite that keeps the size (and, where the
// backend has them, the modification time and the identity of the object) is seen by a conditional read, and the
// ETag that comes with the new bytes is current for them.
func caseETagFollowsContent(t TB, fixture Fixture) {
	t.Helper()

	for _, pair := range generated() {
		original, changed := pair[0], pair[1]

		require.NoError(t, fixture.Backend.Overwrite(keyMain, original))

		first := mustGet(t, fixture, keyMain, "", bigLimit)

		require.NoError(t, fixture.Backend.Overwrite(keyMain, changed))

		second, err := get(t.Context(), t, fixture, keyMain, first.ETag, bigLimit)
		require.NoError(t, err, "[follows-rewrite-seen] %d bytes became %d bytes", len(original), len(changed))
		sameBytes(t, changed, second.Bytes)
		assert.NotEqual(t, first.ETag, second.ETag, "%d -> %d bytes", len(original), len(changed))

		_, err = get(t.Context(), t, fixture, keyMain, second.ETag, bigLimit)
		require.ErrorIs(t, err, manifestreader.ErrNotModified)

		// and back: whatever the ETag of the original is now, it describes the original bytes
		require.NoError(t, fixture.Backend.Overwrite(keyMain, original))

		back := mustGet(t, fixture, keyMain, second.ETag, bigLimit)
		sameBytes(t, original, back.Bytes)
		assert.NotEqual(t, second.ETag, back.ETag)

		_, err = get(t.Context(), t, fixture, keyMain, back.ETag, bigLimit)
		require.ErrorIs(t, err, manifestreader.ErrNotModified)
	}
}

// caseETagContentAddressed: for a store whose ETag is a function of the bytes, the same bytes always give the
// same ETag, whatever happened in between.
func caseETagContentAddressed(t TB, fixture Fixture) {
	t.Helper()

	for _, pair := range generated() {
		original, changed := pair[0], pair[1]

		mustPut(t, fixture, keyMain, original)
		first := mustGet(t, fixture, keyMain, "", bigLimit)

		mustPut(t, fixture, keyMain, changed)
		mustPut(t, fixture, keyMain, original)

		again := mustGet(t, fixture, keyMain, "", bigLimit)
		assert.Equal(t, first.ETag, again.ETag, "%d -> %d bytes", len(original), len(changed))
	}
}

// caseETagDescribesBytes reads while the object is replaced. Whatever the ETags are (a hash, a row version, a
// generation number), an ETag identifies the bytes it came with: the same ETag never comes with two different
// contents, and the two contents never share one ETag.
func caseETagDescribesBytes(t TB, fixture Fixture) {
	t.Helper()

	contentA, contentB := []byte(`{"version":"A"}`), []byte(`{"version":"B-and-longer"}`)
	// in turn, with no writer at work: the reads of a fixed sequence of contents
	var seen []manifestreader.Object

	// A read that starts after a write has returned sees that write: the contract of the store (S3 is strongly
	// consistent since 2020, a file system and a database are too).
	for _, content := range [][]byte{contentA, contentB, contentA} {
		mustPut(t, fixture, keyMain, content)

		obj := mustGet(t, fixture, keyMain, "", bigLimit)
		sameBytes(t, content, obj.Bytes, "[read-after-write] the read that follows a write sees the write")

		seen = append(seen, obj)
	}

	const readers, reads, writes = 4, 25, 40

	var (
		group sync.WaitGroup
		mutex sync.Mutex
	)

	group.Go(func() {
		for i := range writes {
			content := contentA
			if i%2 == 0 {
				content = contentB
			}

			assert.NoError(t, fixture.Backend.Put(keyMain, content))
		}
	})

	for range readers {
		group.Go(func() {
			for range reads {
				obj, err := get(t.Context(), t, fixture, keyMain, "", bigLimit)
				if !assert.NoError(t, err) { //nolint:testifylint // require is not allowed off the test goroutine
					return
				}

				mutex.Lock()

				seen = append(seen, obj)

				mutex.Unlock()
			}
		})
	}

	group.Wait()

	contentOf := map[string][]byte{}

	for _, obj := range seen {
		assert.True(t, bytes.Equal(obj.Bytes, contentA) || bytes.Equal(obj.Bytes, contentB), "bytes %q", obj.Bytes)

		known, ok := contentOf[obj.ETag]
		if !ok {
			contentOf[obj.ETag] = obj.Bytes

			continue
		}

		sameBytes(t, known, obj.Bytes, "[etag-two-contents] the ETag %s came with two different contents", obj.ETag)
	}
}

// caseNotAliased: a write after a read does not change the bytes the read returned.
func caseNotAliased(t TB, fixture Fixture) {
	t.Helper()

	first := []byte("0123456789")
	mustPut(t, fixture, keyMain, first)

	got := mustGet(t, fixture, keyMain, "", bigLimit)
	kept := bytes.Clone(got.Bytes)

	require.NoError(t, fixture.Backend.Overwrite(keyMain, []byte("abcdefghij"))) // the same length
	require.NoError(t, fixture.Backend.Put(keyMain, []byte("ABCDEFGHIJ")))
	require.NoError(t, fixture.Backend.Overwrite(keyMain, []byte("kl")))

	sameBytes(t, kept, got.Bytes, "[bytes-not-aliased] bytes already returned were changed by a later write")
	sameBytes(t, []byte("kl"), mustGet(t, fixture, keyMain, "", bigLimit).Bytes)
	sameBytes(t, kept, got.Bytes)
}

func caseEndedContext(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, keyMain, []byte("x"))
	etag := mustGet(t, fixture, keyMain, "", bigLimit).ETag

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	expired := newManualDeadline(t.Context())
	expired.expire()

	tests := []struct {
		ctx     context.Context //nolint:containedctx // the table is the list of contexts
		want    error
		name    string
		etag    string
		missing bool
	}{
		{name: "canceled", ctx: canceled, want: context.Canceled},
		{name: "canceled, conditional", ctx: canceled, etag: etag, want: context.Canceled},
		{name: "expired", ctx: expired, want: context.DeadlineExceeded},
		{name: "canceled, object missing", ctx: canceled, want: context.Canceled, missing: true},
	}

	for _, tt := range tests {
		key := keyMain
		if tt.missing {
			key = keyThird
		}

		_, err := get(tt.ctx, t, fixture, key, tt.etag, bigLimit)
		require.ErrorIs(t, err, tt.want, "[ended-context-error] "+tt.name)
		require.NotErrorIs(t, err, manifestreader.ErrNotFound, tt.name)
		require.NotErrorIs(t, err, manifestreader.ErrNotModified, tt.name)
	}
}

// startRead starts a read of keyMain on a goroutine and returns at once.
func startRead(ctx context.Context, t TB, fixture Fixture) <-chan readResult {
	t.Helper()

	done := make(chan readResult, 1)

	go func() {
		obj, err := get(ctx, t, fixture, keyMain, "", bigLimit)
		done <- readResult{obj: obj, err: err}
	}()

	return done
}

// waitEntered waits until a read has reached the backend. It keeps the normal bound whatever the bound of the
// waits after a cancellation: a read that starts late is not a store that ignores its context.
func waitEntered(t TB, hang *Hang) {
	t.Helper()

	select {
	case <-hang.Entered():
	case <-time.After(wait):
		require.FailNow(t, "[read-did-not-enter] the read did not reach the backend")
	}
}

// hungRead starts a read of keyMain that hangs and waits until it does.
func hungRead(ctx context.Context, t TB, fixture Fixture, hang *Hang) <-chan readResult {
	t.Helper()

	done := startRead(ctx, t, fixture)
	waitEntered(t, hang)

	return done
}

// readResult is what a read returned.
type readResult struct {
	err error
	obj manifestreader.Object
}

// waitBound is how long a case waits for a read to return after its context ended: [wait], unless the test is a
// recording one that was given a shorter bound (see [CheckWithin]), which a store that is known to ignore its
// context does not need to exhaust.
func waitBound(t TB) time.Duration {
	if bounded, ok := t.(interface{ waitBound() time.Duration }); ok {
		return bounded.waitBound()
	}

	return wait
}

// awaitRead waits for a read whose context ended and returns its error. That no object came with it is checked
// by the read itself.
func awaitRead(t TB, done <-chan readResult) error {
	t.Helper()

	select {
	case result := <-done:
		return result.err
	case <-time.After(waitBound(t)):
		require.FailNow(t, "[read-did-not-return] the read did not return after its context ended")

		return nil
	}
}

// awaitIdle checks that no request of the hang is left. A store may return as soon as its context ends and let
// the request it abandoned finish by itself, so the count is given the bounded, clock-free settling of the leak
// check before it must be zero.
func awaitIdle(t TB, hang *Hang, msg string) {
	t.Helper()

	for range settleRounds {
		if hang.Active() == 0 {
			return
		}

		for range yieldsPerRound {
			runtime.Gosched()
		}
	}

	assert.Equal(t, 0, hang.Active(), msg)
}

func caseCancelWhileReading(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, keyMain, []byte("x"))

	hang, err := fixture.Backend.Hang(keyMain)
	require.NoError(t, err)

	defer hang.Release() // a store that ignores its context must not leave the read hanging

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := hungRead(ctx, t, fixture, hang)
	assert.GreaterOrEqual(t, hang.Active(), 1, "the read is waiting in the backend")

	cancel()

	err = awaitRead(t, done)
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, manifestreader.ErrNotFound)
	awaitIdle(t, hang, "the request is gone once the read has returned")
}

// caseDeadlineWhileReading: the deadline of the context passes while the store waits, and the store ends the read
// with an error that wraps [context.DeadlineExceeded] and no object. The context of the case signals the deadline
// the way a timer would: Done closes and Err is DeadlineExceeded. Its Deadline() is one fixed instant for its whole
// life, as a context's must be, so a store that copies the first Deadline() into a one-shot I/O deadline and does
// not watch Done never ends the read, and fails the case: the contract is that Done is authoritative, and
// Deadline() alone is an optimisation.
func caseDeadlineWhileReading(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, keyMain, []byte("x"))

	hang, err := fixture.Backend.Hang(keyMain)
	require.NoError(t, err)

	defer hang.Release() // a store that ignores its context must not leave the read hanging

	ctx := newManualDeadline(t.Context())
	done := hungRead(ctx, t, fixture, hang)

	ctx.expire()

	err = awaitRead(t, done)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	awaitIdle(t, hang, "the request is gone once the read has returned")
}

// caseCancelWhileReadingBody cancels after the answer started: the store has the headers and is reading the body.
func caseCancelWhileReadingBody(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, keyMain, pseudoRandom(bodySeed, bodyHangSize))

	hang, err := fixture.Backend.HangBody(keyMain)
	require.NoError(t, err)

	defer hang.Release() // a store that ignores its context must not leave the read hanging

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := hungRead(ctx, t, fixture, hang)

	cancel()

	err = awaitRead(t, done)
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, manifestreader.ErrNotFound)
	require.NotErrorIs(t, err, manifestreader.ErrTooLarge)
	awaitIdle(t, hang, "the request is gone once the read has returned")

	hang.Release()

	assert.Len(
		t,
		mustGet(t, fixture, keyMain, "", bigLimit).Bytes,
		bodyHangSize,
		"the store reads on after a cancelled read",
	)
}

// caseNoLeakAfterCancel starts several reads of the key and cancels them. It does not require that the backend
// admits them together: a store with a queue that takes one read at a time keeps the others waiting, and their
// context ends them too. It waits for one read to reach the backend, then cancels.
func caseNoLeakAfterCancel(t TB, fixture Fixture) {
	t.Helper()

	content := []byte("survives")
	mustPut(t, fixture, keyMain, content)
	etag := mustGet(t, fixture, keyMain, "", bigLimit).ETag

	hang, err := fixture.Backend.Hang(keyMain)
	require.NoError(t, err)

	defer hang.Release() // a store that ignores its context must not leave the read hanging

	const reads = 8

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	dones := make([]<-chan readResult, 0, reads)
	for range reads {
		dones = append(dones, startRead(ctx, t, fixture))
	}

	waitEntered(t, hang)
	cancel()

	for _, done := range dones {
		require.ErrorIs(t, awaitRead(t, done), context.Canceled)
	}

	awaitIdle(t, hang, "no request is left behind")

	// the store is not poisoned: the same key answers as before once the hang ends
	hang.Release()

	obj := mustGet(t, fixture, keyMain, "", bigLimit)
	assert.Equal(t, etag, obj.ETag)
	sameBytes(t, content, obj.Bytes)

	_, err = get(t.Context(), t, fixture, keyMain, etag, bigLimit)
	require.ErrorIs(t, err, manifestreader.ErrNotModified)
}

// caseGetsLeaveNothing makes reads of every outcome. That nothing is left running is checked, for this case as for
// every case, when it ends (see the package documentation).
func caseGetsLeaveNothing(t TB, fixture Fixture) {
	t.Helper()

	content := []byte("some content")
	mustPut(t, fixture, keyMain, content)

	etag := mustGet(t, fixture, keyMain, "", bigLimit).ETag

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	for range leakGets {
		_, _ = get(t.Context(), t, fixture, keyMain, "", bigLimit)
		_, _ = get(t.Context(), t, fixture, keyMain, etag, bigLimit)
		_, _ = get(t.Context(), t, fixture, keyOther, "", bigLimit)
		_, _ = get(t.Context(), t, fixture, keyMain, "", 1)
		_, _ = get(canceled, t, fixture, keyMain, "", bigLimit)
	}
}

func caseConcurrentGets(t TB, fixture Fixture) {
	t.Helper()

	keys := []string{keyMain, keyOther, keyThird}
	want := map[string]manifestreader.Object{}

	for i, key := range keys {
		mustPut(t, fixture, key, []byte("content of "+strconv.Itoa(i)))
		want[key] = mustGet(t, fixture, key, "", bigLimit)
	}

	const workers, rounds = 16, 10

	unchanged := manifestreader.ErrNotModified

	var group sync.WaitGroup

	for worker := range workers {
		group.Go(func() {
			for round := range rounds {
				key := keys[(worker+round)%len(keys)]

				if round%2 == 0 {
					obj, err := get(t.Context(), t, fixture, key, "", bigLimit)
					assert.NoError(t, err) //nolint:testifylint // require is not allowed off the test goroutine
					assert.Equal(t, want[key].ETag, obj.ETag)
					sameBytes(t, want[key].Bytes, obj.Bytes)

					continue
				}

				_, err := get(t.Context(), t, fixture, key, want[key].ETag, bigLimit)
				assert.ErrorIs(t, err, unchanged)
			}
		})
	}

	group.Wait()
}
