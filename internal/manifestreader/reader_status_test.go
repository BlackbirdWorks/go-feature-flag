package manifestreader_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

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
