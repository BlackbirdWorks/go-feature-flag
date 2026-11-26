package storetest

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
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
	// shortDeadline is the deadline of the context of a read that hangs. The read is blocked until it ends.
	shortDeadline = 20 * time.Millisecond

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

func get(ctx context.Context, fixture Fixture, key, etag string, limit int64) (manifestreader.Object, error) {
	obj, err := fixture.Store.Get(ctx, manifestreader.GetRequest{Key: key, ETag: etag, Limit: limit})
	if err != nil {
		return obj, fmt.Errorf("storetest: get %q: %w", key, err)
	}

	return obj, nil
}

// mustGet reads key and requires success.
func mustGet(t TB, fixture Fixture, key, etag string, limit int64) manifestreader.Object {
	t.Helper()

	obj, err := get(t.Context(), fixture, key, etag, limit)
	require.NoError(t, err)

	return obj
}

func mustPut(t TB, fixture Fixture, key string, content []byte) {
	t.Helper()
	require.NoError(t, fixture.Backend.Put(key, content))
}

// wantFetchError asserts the classification of a failed read.
func wantFetchError(t TB, err error, reason string, retryable, throttled bool) {
	t.Helper()

	fetchErr, ok := errors.AsType[*manifestreader.FetchError](err)
	require.True(t, ok, "want a *FetchError, got %T: %v", err, err)
	assert.Equal(t, reason, fetchErr.Reason)
	assert.Equal(t, retryable, fetchErr.Retryable, "Retryable")
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
	assert.Equal(t, content, obj.Bytes)
	assert.NotEmpty(t, obj.ETag)
}

func caseConditionalUnchanged(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, keyMain, []byte(`{"flag":true}`))
	first := mustGet(t, fixture, keyMain, "", bigLimit)

	obj, err := get(t.Context(), fixture, keyMain, first.ETag, bigLimit)
	require.ErrorIs(t, err, manifestreader.ErrNotModified)
	assert.Equal(t, manifestreader.Object{}, obj, "a not-modified answer carries no body and no ETag")
}

func caseConditionalChanged(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, keyMain, []byte(`{"flag":true}`))
	first := mustGet(t, fixture, keyMain, "", bigLimit)

	changed := []byte(`{"flag":false,"more":1}`)
	mustPut(t, fixture, keyMain, changed)

	second := mustGet(t, fixture, keyMain, first.ETag, bigLimit)
	assert.Equal(t, changed, second.Bytes)
	assert.NotEmpty(t, second.ETag)
	assert.NotEqual(t, first.ETag, second.ETag)

	_, err := get(t.Context(), fixture, keyMain, second.ETag, bigLimit)
	require.ErrorIs(t, err, manifestreader.ErrNotModified, "the new ETag is current")
}

func caseUnknownETag(t TB, fixture Fixture) {
	t.Helper()

	content := []byte(`{"flag":true}`)
	mustPut(t, fixture, keyMain, content)

	for _, etag := range []string{`"not-an-etag"`, `"0000000000000000"`, `W/"weak"`} {
		obj := mustGet(t, fixture, keyMain, etag, bigLimit)
		assert.Equal(t, content, obj.Bytes, etag)
	}
}

func caseMissing(t TB, fixture Fixture) {
	t.Helper()

	obj, err := get(t.Context(), fixture, keyMain, "", bigLimit)
	require.ErrorIs(t, err, manifestreader.ErrNotFound, "never written")
	assert.Equal(t, manifestreader.Object{}, obj)

	mustPut(t, fixture, keyMain, []byte("x"))
	etag := mustGet(t, fixture, keyMain, "", bigLimit).ETag
	require.NoError(t, fixture.Backend.Delete(keyMain))

	_, err = get(t.Context(), fixture, keyMain, "", bigLimit)
	require.ErrorIs(t, err, manifestreader.ErrNotFound, "deleted")

	_, err = get(t.Context(), fixture, keyMain, etag, bigLimit)
	require.ErrorIs(t, err, manifestreader.ErrNotFound, "a deleted object is not a not-modified one")

	_, err = get(t.Context(), fixture, "conformance/no/such/dir/object.json", "", bigLimit)
	require.ErrorIs(t, err, manifestreader.ErrNotFound, "a missing directory on the path")
}

func caseMissingVersion(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, keyMain, []byte("x"))

	obj, err := fixture.Store.Get(t.Context(), manifestreader.GetRequest{
		Key: keyMain, VersionID: "no-such-version", Limit: bigLimit,
	})
	require.ErrorIs(t, err, manifestreader.ErrNotFound)
	assert.Equal(t, manifestreader.Object{}, obj)
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
		return fixture.Store.Get(t.Context(), manifestreader.GetRequest{
			Key: keyMain, VersionID: id, ETag: etag, Limit: bigLimit,
		})
	}

	old, err := byVersion(id1, "")
	require.NoError(t, err)
	assert.Equal(t, first, old.Bytes, "the older version is read after the overwrite")

	current, err := byVersion(id2, "")
	require.NoError(t, err)
	assert.Equal(t, second, current.Bytes)
	assert.NotEqual(t, old.ETag, current.ETag)

	assert.Equal(t, second, mustGet(t, fixture, keyMain, "", bigLimit).Bytes, "no version id: the current one")

	_, err = byVersion(id1, old.ETag)
	require.ErrorIs(t, err, manifestreader.ErrNotModified, "an older version is unchanged for its own ETag")

	again, err := byVersion(id1, current.ETag)
	require.NoError(t, err)
	assert.Equal(t, first, again.Bytes, "the ETag of another version does not make the version unchanged")
}

// faultCase arranges fault for keyMain, checks the classification of an unconditional and of a conditional read
// and that the store recovers when the fault is cleared.
func faultCase(t TB, fixture Fixture, fault Fault, reason string, retryable, throttled bool) {
	t.Helper()

	content := []byte(`{"flag":true}`)
	mustPut(t, fixture, keyMain, content)
	etag := mustGet(t, fixture, keyMain, "", bigLimit).ETag

	require.NoError(t, fixture.Backend.Fail(keyMain, fault))

	obj, err := get(t.Context(), fixture, keyMain, "", bigLimit)
	require.Error(t, err)
	wantFetchError(t, err, reason, retryable, throttled)
	assert.Equal(t, manifestreader.Object{}, obj)

	_, err = get(t.Context(), fixture, keyMain, etag, bigLimit)
	require.Error(t, err)
	wantFetchError(t, err, reason, retryable, throttled)

	// another key is not affected
	mustPut(t, fixture, keyOther, []byte("other"))
	assert.Equal(t, []byte("other"), mustGet(t, fixture, keyOther, "", bigLimit).Bytes)

	require.NoError(t, fixture.Backend.Fail(keyMain, FaultNone))

	assert.Equal(t, content, mustGet(t, fixture, keyMain, "", bigLimit).Bytes, "recovered")

	_, err = get(t.Context(), fixture, keyMain, etag, bigLimit)
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
		obj, err := get(t.Context(), fixture, keyMain, "", tt.limit)
		if tt.want {
			require.NoError(t, err, tt.name)
			assert.Equal(t, content, obj.Bytes, tt.name)

			continue
		}

		require.ErrorIs(t, err, manifestreader.ErrTooLarge, tt.name)
		assert.Equal(t, manifestreader.Object{}, obj, tt.name)
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
		obj, err := get(t.Context(), fixture, keyMain, "", limit)
		require.ErrorIs(t, err, manifestreader.ErrTooLarge, "limit %d, one byte", limit)
		assert.Equal(t, manifestreader.Object{}, obj, "limit %d", limit)

		empty := mustGet(t, fixture, keyOther, "", limit)
		assert.Empty(t, empty.Bytes, "limit %d, empty object", limit)
		assert.NotEmpty(t, empty.ETag, "limit %d, empty object", limit)

		_, err = get(t.Context(), fixture, keyOther, empty.ETag, limit)
		require.ErrorIs(t, err, manifestreader.ErrNotModified, "limit %d, empty object, current ETag", limit)
	}
}

// caseConditionalOverLimit: with a limit below the size and the current ETag, a store may answer "not modified"
// or "too large" (it depends on whether it looks at the size first), never with a body.
func caseConditionalOverLimit(t TB, fixture Fixture) {
	t.Helper()

	content := bytes.Repeat([]byte("0123456789"), repeatTen)
	mustPut(t, fixture, keyMain, content)

	etag := mustGet(t, fixture, keyMain, "", bigLimit).ETag

	obj, err := get(t.Context(), fixture, keyMain, etag, int64(len(content))-1)
	require.Error(t, err)
	assert.True(t,
		errors.Is(err, manifestreader.ErrNotModified) || errors.Is(err, manifestreader.ErrTooLarge),
		"want not-modified or too-large, got %v", err)
	assert.Equal(t, manifestreader.Object{}, obj, "never a body")

	obj, err = get(t.Context(), fixture, keyMain, `"stale"`, int64(len(content))-1)
	require.ErrorIs(t, err, manifestreader.ErrTooLarge, "another ETag: the object is read and it is too large")
	assert.Equal(t, manifestreader.Object{}, obj)
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
	other := mustGet(t, fixture, keyOther, "", bigLimit)
	assert.NotEqual(t, empty.ETag, other.ETag, "the empty object has an ETag of its own")

	_, err := get(t.Context(), fixture, keyMain, empty.ETag, 0)
	require.ErrorIs(t, err, manifestreader.ErrNotModified)

	_, err = get(t.Context(), fixture, keyOther, "", 0)
	require.ErrorIs(t, err, manifestreader.ErrTooLarge, "one byte over a limit of zero")
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
	assert.True(t, bytes.Equal(content, obj.Bytes), "the 3 MiB body is returned whole at a limit equal to its size")

	_, err := get(t.Context(), fixture, keyMain, "", size-1)
	require.ErrorIs(t, err, manifestreader.ErrTooLarge)

	_, err = get(t.Context(), fixture, keyMain, obj.ETag, size)
	require.ErrorIs(t, err, manifestreader.ErrNotModified)
}

// caseKeyCharacters puts one object per key, with content of its own, and reads each back: the characters that
// URLs, shells and file systems treat specially must name distinct objects.
func caseKeyCharacters(t TB, fixture Fixture) {
	t.Helper()

	keys := []string{
		"conformance/with space.json", "conformance/ünï/é.json", "conformance/a+b=c&d.json",
		"conformance/deep/er/est/x.json", "conformance/.hidden", "conformance/dots..in..name", "top-level.json",
		"conformance/what?.json", "conformance/hash#tag.json", "conformance/percent%.json",
		"conformance/p%2Fx.json", "conformance/p/x.json", "conformance/a b", "conformance/a+b",
		".leading-dot", "conformance/%41", "conformance/A",
	}

	for i, key := range keys {
		mustPut(t, fixture, key, []byte("content-"+strconv.Itoa(i)))
	}

	for i, key := range keys {
		obj, err := get(t.Context(), fixture, key, "", bigLimit)
		require.NoError(t, err, key)
		assert.Equal(t, []byte("content-"+strconv.Itoa(i)), obj.Bytes, key)
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
		obj, err := get(t.Context(), fixture, key, "", bigLimit)
		require.Error(t, err, "key %q", key)
		assert.Equal(t, manifestreader.Object{}, obj, "key %q", key)

		_, isFetch := errors.AsType[*manifestreader.FetchError](err)
		assert.True(t, isFetch || errors.Is(err, manifestreader.ErrNotFound), "key %q: %T %v", key, err, err)
	}
}

func caseKeyRefusal(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, "conformance/real.json", []byte("real"))
	assert.Equal(t, []byte("real"), mustGet(t, fixture, "conformance/real.json", "", bigLimit).Bytes)

	refused := []string{
		"", ".", "..", "/conformance/real.json", "conformance/../conformance/real.json", "./conformance/real.json",
		"conformance//real.json", "conformance/real.json/", "conformance\\real.json", "conformance/real.json\x00x",
		"../conformance/real.json", "conformance/./real.json",
		"conformance/" + string(bytes.Repeat([]byte("n"), longKeyLen)),
	}

	for _, key := range refused {
		obj, err := get(t.Context(), fixture, key, "", bigLimit)
		require.Error(t, err, "key %q", key)
		wantFetchError(t, err, manifestreader.ReasonDenied, false, false)
		assert.Equal(t, manifestreader.Object{}, obj, "key %q", key)
	}
}

func caseNestedKeys(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, "conformance/node", []byte("the object"))
	mustPut(t, fixture, "conformance/node/child", []byte("the child"))

	assert.Equal(t, []byte("the object"), mustGet(t, fixture, "conformance/node", "", bigLimit).Bytes)
	assert.Equal(t, []byte("the child"), mustGet(t, fixture, "conformance/node/child", "", bigLimit).Bytes)

	require.NoError(t, fixture.Backend.Delete("conformance/node/child"))
	assert.Equal(t, []byte("the object"), mustGet(t, fixture, "conformance/node", "", bigLimit).Bytes)
}

func caseETagStable(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, keyMain, []byte(`{"flag":true}`))
	first := mustGet(t, fixture, keyMain, "", bigLimit)

	for range 3 {
		again := mustGet(t, fixture, keyMain, "", bigLimit)
		assert.Equal(t, first, again)

		obj, err := get(t.Context(), fixture, keyMain, first.ETag, bigLimit)
		require.ErrorIs(t, err, manifestreader.ErrNotModified)
		assert.Equal(t, manifestreader.Object{}, obj)
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

		second, err := get(t.Context(), fixture, keyMain, first.ETag, bigLimit)
		require.NoError(t, err, "%d bytes became %d bytes", len(original), len(changed))
		assert.Equal(t, changed, second.Bytes)
		assert.NotEqual(t, first.ETag, second.ETag, "%d -> %d bytes", len(original), len(changed))

		_, err = get(t.Context(), fixture, keyMain, second.ETag, bigLimit)
		require.ErrorIs(t, err, manifestreader.ErrNotModified)

		// and back: whatever the ETag of the original is now, it describes the original bytes
		require.NoError(t, fixture.Backend.Overwrite(keyMain, original))

		back := mustGet(t, fixture, keyMain, second.ETag, bigLimit)
		assert.Equal(t, original, back.Bytes)
		assert.NotEqual(t, second.ETag, back.ETag)

		_, err = get(t.Context(), fixture, keyMain, back.ETag, bigLimit)
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

		assert.Equal(
			t,
			first,
			mustGet(t, fixture, keyMain, "", bigLimit),
			"%d -> %d bytes",
			len(original),
			len(changed),
		)
	}
}

// caseETagDescribesBytes reads while the object is replaced: every answer pairs bytes with the ETag of exactly
// those bytes.
func caseETagDescribesBytes(t TB, fixture Fixture) {
	t.Helper()

	contentA, contentB := []byte(`{"version":"A"}`), []byte(`{"version":"B-and-longer"}`)
	mustPut(t, fixture, "conformance/probe-a", contentA)
	mustPut(t, fixture, "conformance/probe-b", contentB)
	etagA := mustGet(t, fixture, "conformance/probe-a", "", bigLimit).ETag
	etagB := mustGet(t, fixture, "conformance/probe-b", "", bigLimit).ETag

	mustPut(t, fixture, keyMain, contentA)

	const readers, reads, writes = 4, 25, 40

	var group sync.WaitGroup

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
				obj, err := get(t.Context(), fixture, keyMain, "", bigLimit)
				if !assert.NoError(t, err) { //nolint:testifylint // require is not allowed off the test goroutine
					return
				}

				// the ETag of A is not the ETag of B, and each goes with its own bytes
				assert.True(t,
					(bytes.Equal(obj.Bytes, contentA) && obj.ETag == etagA) ||
						(bytes.Equal(obj.Bytes, contentB) && obj.ETag == etagB),
					"bytes %q came with ETag %s", obj.Bytes, obj.ETag)
			}
		})
	}

	group.Wait()
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

	assert.Equal(t, kept, got.Bytes, "bytes already returned were changed by a later write")
	assert.Equal(t, []byte("kl"), mustGet(t, fixture, keyMain, "", bigLimit).Bytes)
	assert.Equal(t, kept, got.Bytes)
}

func caseEndedContext(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, keyMain, []byte("x"))
	etag := mustGet(t, fixture, keyMain, "", bigLimit).ETag

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	expired, stop := context.WithTimeout(t.Context(), -time.Second)
	defer stop()

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

		obj, err := get(tt.ctx, fixture, key, tt.etag, bigLimit)
		require.ErrorIs(t, err, tt.want, tt.name)
		assert.Equal(t, manifestreader.Object{}, obj, tt.name)
		require.NotErrorIs(t, err, manifestreader.ErrNotFound, tt.name)
		require.NotErrorIs(t, err, manifestreader.ErrNotModified, tt.name)
	}
}

// hungRead starts a read of key that hangs and waits until it does.
func hungRead(ctx context.Context, t TB, fixture Fixture, hang *Hang) <-chan error {
	t.Helper()

	done := make(chan error, 1)

	go func() {
		_, err := get(ctx, fixture, keyMain, "", bigLimit)
		done <- err
	}()

	select {
	case <-hang.Entered():
	case <-time.After(wait):
		require.FailNow(t, "the read did not reach the backend")
	}

	return done
}

func awaitRead(t TB, done <-chan error) error {
	t.Helper()

	select {
	case err := <-done:
		return err
	case <-time.After(wait):
		require.FailNow(t, "the read did not return after its context ended")

		return nil
	}
}

func caseCancelWhileReading(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, keyMain, []byte("x"))

	hang, err := fixture.Backend.Hang(keyMain)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := hungRead(ctx, t, fixture, hang)
	assert.Equal(t, 1, hang.Active())

	cancel()

	err = awaitRead(t, done)
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, manifestreader.ErrNotFound)
	assert.Equal(t, 0, hang.Active(), "the request is gone when the read has returned")
}

func caseDeadlineWhileReading(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, keyMain, []byte("x"))

	hang, err := fixture.Backend.Hang(keyMain)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), shortDeadline)
	defer cancel()

	done := hungRead(ctx, t, fixture, hang)

	err = awaitRead(t, done)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 0, hang.Active())
}

// caseCancelWhileReadingBody cancels after the answer started: the store has the headers and is reading the body.
func caseCancelWhileReadingBody(t TB, fixture Fixture) {
	t.Helper()

	mustPut(t, fixture, keyMain, pseudoRandom(bodySeed, bodyHangSize))

	hang, err := fixture.Backend.HangBody(keyMain)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := hungRead(ctx, t, fixture, hang)

	cancel()

	err = awaitRead(t, done)
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, manifestreader.ErrNotFound)
	require.NotErrorIs(t, err, manifestreader.ErrTooLarge)
	assert.Equal(t, 0, hang.Active())

	hang.Release()

	assert.Len(
		t,
		mustGet(t, fixture, keyMain, "", bigLimit).Bytes,
		bodyHangSize,
		"the store reads on after a cancelled read",
	)
}

func caseNoLeakAfterCancel(t TB, fixture Fixture) {
	t.Helper()

	content := []byte("survives")
	mustPut(t, fixture, keyMain, content)
	etag := mustGet(t, fixture, keyMain, "", bigLimit).ETag

	hang, err := fixture.Backend.Hang(keyMain)
	require.NoError(t, err)

	const reads = 8

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	dones := make([]<-chan error, 0, reads)
	for range reads {
		dones = append(dones, hungRead(ctx, t, fixture, hang))
	}

	assert.Equal(t, reads, hang.Active())
	cancel()

	for _, done := range dones {
		require.ErrorIs(t, awaitRead(t, done), context.Canceled)
	}

	assert.Equal(t, 0, hang.Active(), "no request is left behind")

	// the store is not poisoned: the same key answers as before once the hang ends
	hang.Release()

	obj := mustGet(t, fixture, keyMain, "", bigLimit)
	assert.Equal(t, manifestreader.Object{Bytes: content, ETag: etag}, obj)

	_, err = get(t.Context(), fixture, keyMain, etag, bigLimit)
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
		_, _ = get(t.Context(), fixture, keyMain, "", bigLimit)
		_, _ = get(t.Context(), fixture, keyMain, etag, bigLimit)
		_, _ = get(t.Context(), fixture, keyOther, "", bigLimit)
		_, _ = get(t.Context(), fixture, keyMain, "", 1)
		_, _ = get(canceled, fixture, keyMain, "", bigLimit)
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
					obj, err := get(t.Context(), fixture, key, "", bigLimit)
					assert.NoError(t, err) //nolint:testifylint // require is not allowed off the test goroutine
					assert.Equal(t, want[key], obj)

					continue
				}

				obj, err := get(t.Context(), fixture, key, want[key].ETag, bigLimit)
				assert.ErrorIs(t, err, unchanged) //nolint:testifylint // require is not allowed off the test goroutine
				assert.Equal(t, manifestreader.Object{}, obj)
			}
		})
	}

	group.Wait()
}
