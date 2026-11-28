package manifestreader_test

import (
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

func TestReader_ManifestUnknownFieldPathsAndPreservation(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	for _, o := range threeObjects() {
		store.put(keyOf(t, o), []byte(o.content))
	}

	manifest := manifestOf(t, 1, threeObjects()...)
	raw, err := controlplane.MarshalManifestJSON(manifest)
	require.NoError(t, err)

	var extra strings.Builder

	extra.WriteByte('{')

	for i := range 12 {
		fmt.Fprintf(&extra, `"zExtra%02d":"secret-val-%02d",`, i, i)
	}

	withUnknown := append([]byte(extra.String()), raw[1:]...)
	store.put(manifestKey, withUnknown)

	rdr := startRig(t, store, &manifestreader.Config{})
	require.NotNil(t, rdr.snapshot())

	got := rdr.reader.Status().Manifest
	assert.Equal(t, manifestreader.OutcomeAccepted, got.Outcome)
	assert.Equal(t, clocktestInstant().UTC(), got.At)
	assert.Equal(t, 12, got.UnknownFields)
	require.Len(t, got.UnknownFieldPaths, manifestreader.MaxUnknownFieldPaths)
	assert.Equal(t, "zExtra00", got.UnknownFieldPaths[0])
	assert.Equal(t, "zExtra09", got.UnknownFieldPaths[9])

	// A rejected manifest updates outcome/reason/at while preserving held unknown-field counts and paths.
	store.put(
		manifestKey,
		[]byte("apiVersion: semaphore/v2\nkind: Manifest\nchannel: stable\ngeneration: 2\nobjects: []\n"),
	)

	_ = rdr.round()

	rejected := rdr.reader.Status().Manifest
	assert.Equal(t, manifestreader.OutcomeInvalid, rejected.Outcome)
	assert.Equal(t, string(controlplane.CodeUnsupportedVersion), rejected.Reason)
	assert.Equal(t, clocktestInstant().UTC(), rejected.At)
	assert.Equal(t, uint64(1), rejected.Generation)
	assert.Equal(t, 12, rejected.UnknownFields)
	assert.Equal(t, got.UnknownFieldPaths, rejected.UnknownFieldPaths)

	// An accepted manifest without unknown fields clears the count and paths.
	publish(t, store, 2, threeObjects()...)

	_ = rdr.round()

	cleared := rdr.reader.Status().Manifest
	assert.Equal(t, manifestreader.OutcomeAccepted, cleared.Outcome)
	assert.Empty(t, cleared.Reason)
	assert.Equal(t, 0, cleared.UnknownFields)
	assert.Empty(t, cleared.UnknownFieldPaths)
}

func TestReader_RejectionCountsByReason(t *testing.T) {
	t.Parallel()

	assert.True(t, slices.IsSorted(manifestreader.RejectionReasons()), "RejectionReasons must stay sorted")

	validGen2 := func(t *testing.T) []byte {
		t.Helper()

		return mustMarshal(t, manifestOf(t, 2, threeObjects()...))
	}

	tests := []struct {
		bad        func(t *testing.T) []byte
		name       string
		wantReason string
		cfg        manifestreader.Config
	}{
		{
			name:       "unsupported manifest apiVersion",
			wantReason: string(controlplane.CodeUnsupportedVersion),
			bad: func(t *testing.T) []byte {
				t.Helper()

				return []byte("apiVersion: semaphore/v99\nkind: Manifest\nchannel: stable\ngeneration: 2\n")
			},
		},
		{
			name:       "wrong kind",
			wantReason: "wrong-kind",
			bad: func(t *testing.T) []byte {
				t.Helper()

				return []byte("apiVersion: semaphore/v1\nkind: FlagSet\nflagSets: []\n")
			},
		},
		{
			name:       "decode error",
			wantReason: "decode",
			bad: func(t *testing.T) []byte {
				t.Helper()

				return []byte("not: [valid")
			},
		},
		{
			name:       "channel mismatch",
			cfg:        manifestreader.Config{Channel: testChannel},
			wantReason: manifestreader.OutcomeChannelMismatch,
			bad: func(t *testing.T) []byte {
				t.Helper()

				manifest := manifestOf(t, 2, threeObjects()...)
				manifest.Channel = "other"

				return mustMarshal(t, manifest)
			},
		},
		{
			name:       "lower generation",
			wantReason: manifestreader.OutcomeLowerGeneration,
			bad: func(t *testing.T) []byte {
				t.Helper()

				return mustMarshal(t, manifestOf(t, 0+1, threeObjects()[0]))
			},
		},
		{
			name:       "generation conflict",
			wantReason: manifestreader.OutcomeConflict,
			bad: func(t *testing.T) []byte {
				t.Helper()

				return mustMarshal(t, manifestOf(t, 2, threeObjects()[0]))
			},
		},
		{
			name:       "too many objects",
			cfg:        manifestreader.Config{MaxObjects: 2},
			wantReason: string(controlplane.CodeTooManyObjects),
			bad:        validGen2,
		},
		{
			name:       "too large total",
			cfg:        manifestreader.Config{MaxTotalBytes: 15},
			wantReason: manifestreader.ReasonTotalTooLarge,
			bad: func(t *testing.T) []byte {
				t.Helper()

				return mustMarshal(t, manifestOf(t, 3, threeObjects()...))
			},
		},
		{
			name:       "missing channel",
			wantReason: string(controlplane.CodeMissingValue),
			bad: func(t *testing.T) []byte {
				t.Helper()

				manifest := manifestOf(t, 3, threeObjects()...)
				manifest.Channel = ""

				return mustMarshal(t, manifest)
			},
		},
		{
			name:       "invalid generation",
			wantReason: string(controlplane.CodeInvalidValue),
			bad: func(t *testing.T) []byte {
				t.Helper()

				manifest := manifestOf(t, 0, threeObjects()...)

				return mustMarshal(t, manifest)
			},
		},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			publish(t, store, 2, threeObjects()...)

			for _, item := range threeObjects() {
				store.put(keyOf(t, item), []byte(item.content))
			}

			cfg := tt.cfg
			switch tt.name {
			case "too many objects":
				publish(t, store, 2, threeObjects()[:2]...)
			case "too large total":
				publish(t, store, 2, threeObjects()[0])
			}

			rdr := startRig(t, store, &cfg)
			require.NotNil(t, rdr.snapshot())
			store.takeCalls()

			store.put(manifestKey, tt.bad(t))

			_ = rdr.round()

			status := rdr.reader.Status()
			assert.Equal(t, []manifestreader.RejectionCount{{Reason: tt.wantReason, Count: 1}}, status.Rejections)
		})
	}
}

func TestReader_AccumulatesMultipleRejectionReasonsSorted(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	publish(t, store, 3, threeObjects()...)

	for _, item := range threeObjects() {
		store.put(keyOf(t, item), []byte(item.content))
	}

	rdr := startRig(t, store, &manifestreader.Config{Channel: testChannel})
	require.NotNil(t, rdr.snapshot())
	store.takeCalls()

	// Trigger lower-generation first, then wrong-kind, then channel-mismatch, then lower-generation again.
	store.put(manifestKey, mustMarshal(t, manifestOf(t, 2, threeObjects()...)))

	_ = rdr.round()

	store.put(manifestKey, []byte("apiVersion: semaphore/v1\nkind: FlagSet\nflagSets: []\n"))

	_ = rdr.round()

	wrongChan := manifestOf(t, 4, threeObjects()...)
	wrongChan.Channel = "other"
	store.put(manifestKey, mustMarshal(t, wrongChan))

	_ = rdr.round()

	store.put(manifestKey, mustMarshal(t, manifestOf(t, 1, threeObjects()...)))

	_ = rdr.round()

	got := rdr.reader.Status().Rejections
	want := []manifestreader.RejectionCount{
		{Reason: manifestreader.OutcomeChannelMismatch, Count: 1},
		{Reason: manifestreader.OutcomeLowerGeneration, Count: 2},
		{Reason: "wrong-kind", Count: 1},
	}
	assert.Equal(t, want, got)
}

func TestRejectionReason_MapsClosedVocabulary(t *testing.T) {
	t.Parallel()

	const ignoredReason = "ignored"

	tests := []struct {
		name    string
		outcome string
		reason  string
		want    string
	}{
		{name: "empty outcome", outcome: "", reason: ignoredReason, want: ""},
		{name: "none outcome", outcome: manifestreader.OutcomeNone, reason: ignoredReason, want: ""},
		{name: "accepted outcome", outcome: manifestreader.OutcomeAccepted, reason: ignoredReason, want: ""},
		{
			name:    "channel mismatch",
			outcome: manifestreader.OutcomeChannelMismatch,
			reason:  "channel",
			want:    manifestreader.OutcomeChannelMismatch,
		},
		{
			name:    "lower generation",
			outcome: manifestreader.OutcomeLowerGeneration,
			reason:  "generation",
			want:    manifestreader.OutcomeLowerGeneration,
		},
		{
			name:    "generation conflict",
			outcome: manifestreader.OutcomeConflict,
			reason:  "generation",
			want:    manifestreader.OutcomeConflict,
		},
		{
			name:    "invalid preserves closed code",
			outcome: manifestreader.OutcomeInvalid,
			reason:  string(controlplane.CodeUnsupportedVersion),
			want:    string(controlplane.CodeUnsupportedVersion),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, manifestreader.RejectionReason(tt.outcome, tt.reason))
		})
	}
}

func TestRejectionReason_AllControlplaneCodesInClosedSet(t *testing.T) {
	t.Parallel()

	closed := manifestreader.RejectionReasons()
	allCodes := []controlplane.Code{
		controlplane.CodeUnresolvedSource,
		controlplane.CodeDuplicateKey,
		controlplane.CodeDuplicateName,
		controlplane.CodeInvalidName,
		controlplane.CodeUnsupportedVersion,
		controlplane.CodeInvalidChecksum,
		controlplane.CodeInvalidValue,
		controlplane.CodeMissingValue,
		controlplane.CodeTooManyObjects,
		controlplane.CodeUndeclaredVariation,
	}

	for _, code := range allCodes {
		got := manifestreader.RejectionReason(manifestreader.OutcomeInvalid, string(code))
		assert.Contains(t, closed, got, "code %s must map inside closed set", code)
	}

	const wantInvalid = manifestreader.OutcomeInvalid

	assert.Equal(t, wantInvalid, manifestreader.RejectionReason(
		manifestreader.OutcomeInvalid, string(controlplane.CodeUnresolvedSource),
	))
	assert.Equal(t, wantInvalid, manifestreader.RejectionReason(
		manifestreader.OutcomeInvalid, string(controlplane.CodeDuplicateKey),
	))
	assert.Equal(t, wantInvalid, manifestreader.RejectionReason(
		manifestreader.OutcomeInvalid, string(controlplane.CodeUndeclaredVariation),
	))
	assert.Equal(t, wantInvalid, manifestreader.RejectionReason(manifestreader.OutcomeInvalid, "future-code"))
}

func TestCapUnknownFieldPaths_SanitizesAndBounds(t *testing.T) {
	t.Parallel()

	const threeByteRune = "\u20ac"

	longASCII := strings.Repeat("a", 250)
	longUTF8 := strings.Repeat(threeByteRune, 50) // 150 bytes (3 bytes/rune) -> truncates cleanly at 126 bytes.

	tests := []struct {
		name      string
		in        []string
		want      []string
		maxBytes  int
		checkUTF8 bool
	}{
		{name: "nil input returns nil", in: nil, want: nil},
		{name: "empty slice returns nil", in: []string{}, want: nil},
		{
			name:     "very long ascii key truncates to 128 bytes",
			in:       []string{longASCII},
			want:     []string{strings.Repeat("a", manifestreader.MaxUnknownFieldPathBytes)},
			maxBytes: manifestreader.MaxUnknownFieldPathBytes,
		},
		{
			name:      "very long utf8 key truncates on valid rune boundary",
			in:        []string{longUTF8},
			want:      []string{strings.Repeat(threeByteRune, manifestreader.MaxUnknownFieldPathBytes/3)},
			maxBytes:  manifestreader.MaxUnknownFieldPathBytes,
			checkUTF8: true,
		},
		{
			name:      "invalid utf8 bytes are replaced cleanly",
			in:        []string{"bad\xff\xfekey"},
			want:      []string{"bad\uFFFD\uFFFDkey"},
			maxBytes:  manifestreader.MaxUnknownFieldPathBytes,
			checkUTF8: true,
		},
		{
			name: "newlines and control characters are replaced with spaces",
			in:   []string{"line1\nline2\r\nline3\tend"},
			want: []string{"line1 line2  line3 end"},
		},
		{
			name: "more than ten paths capped to ten",
			in:   []string{"p00", "p01", "p02", "p03", "p04", "p05", "p06", "p07", "p08", "p09", "p10", "p11"},
			want: []string{"p00", "p01", "p02", "p03", "p04", "p05", "p06", "p07", "p08", "p09"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := manifestreader.CapUnknownFieldPaths(tt.in)
			assert.Equal(t, tt.want, got)

			for _, item := range got {
				assert.LessOrEqual(t, len(item), manifestreader.MaxUnknownFieldPathBytes)
				assert.NotContains(t, item, "\n")
				assert.NotContains(t, item, "\r")

				if tt.checkUTF8 {
					assert.True(t, utf8.ValidString(item))
				}
			}
		})
	}
}

func TestReader_RestoresManifestUnknownFieldsFromSavedState(t *testing.T) {
	t.Parallel()

	items := threeObjects()
	manifest := manifestOf(t, 4, items...)
	raw, err := controlplane.MarshalManifestJSON(manifest)
	require.NoError(t, err)

	withUnknown := append([]byte(`{"restoredExtra":"secret-val",`), raw[1:]...)

	restoredObjs := make([][]byte, 0, len(items))
	for _, item := range items {
		restoredObjs = append(restoredObjs, []byte(item.content))
	}

	hooks := manifestreader.Hooks{
		Restore: func() (*manifestreader.Restored, error) {
			return &manifestreader.Restored{
				ETag:     "saved-etag",
				Manifest: withUnknown,
				Objects:  restoredObjs,
			}, nil
		},
	}

	// Empty store answers 404 on poll; status must keep restored unknown fields without a successful poll.
	store := newFakeStore()
	rdr := startRig(t, store, &manifestreader.Config{}, manifestreader.WithHooks(hooks))

	snap := rdr.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, uint64(4), snap.Generation)

	got := rdr.reader.Status().Manifest
	assert.Equal(t, manifestreader.OutcomeAccepted, got.Outcome)
	assert.Equal(t, 1, got.UnknownFields)
	assert.Equal(t, []string{"restoredExtra"}, got.UnknownFieldPaths)
}

// The status of the manifest is visible before the snapshot reaches the engine: the round of the engine reads it (for
// the unknown fields of the manifest) and must not meet the status of the previous manifest.
func TestReader_ManifestStatusIsVisibleWhenTheSnapshotIsPublished(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	for _, o := range threeObjects() {
		store.put(keyOf(t, o), []byte(o.content))
	}

	publish(t, store, 1, threeObjects()...)

	var (
		current atomic.Pointer[manifestreader.Reader]
		seen    = make(chan manifestreader.ManifestStatus, 4)
	)

	hooks := manifestreader.Hooks{Save: func(manifestreader.Persist) {
		if rdr := current.Load(); rdr != nil {
			seen <- rdr.Status().Manifest
		}
	}}
	rdr := startRig(t, store, &manifestreader.Config{}, manifestreader.WithHooks(hooks))
	current.Store(rdr.reader)

	raw, err := controlplane.MarshalManifestJSON(manifestOf(t, 2, threeObjects()...))
	require.NoError(t, err)

	store.put(manifestKey, append([]byte(`{"futureField":1,`), raw[1:]...))

	_ = rdr.round()

	require.Len(t, seen, 1)

	got := <-seen
	assert.Equal(t, uint64(2), got.Generation)
	assert.Equal(t, 1, got.UnknownFields)
}
