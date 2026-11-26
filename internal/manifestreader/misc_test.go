package manifestreader_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

func TestSnapshot_NamesAreSorted(t *testing.T) {
	t.Parallel()

	snap := &manifestreader.Snapshot{Objects: map[string]manifestreader.ObjectState{"b": {}, "c": {}, "a": {}}}

	assert.Equal(t, []string{"a", "b", "c"}, snap.Names())
	assert.Empty(t, (&manifestreader.Snapshot{}).Names())
}

func TestFetchError_Message(t *testing.T) {
	t.Parallel()

	cause := errCause

	tests := []struct {
		name string
		err  *manifestreader.FetchError
		want string
	}{
		{
			name: "with a cause",
			err:  &manifestreader.FetchError{Err: cause, Reason: manifestreader.ReasonError},
			want: "manifestreader: fetch failed: error: the cause",
		},
		{
			name: "without a cause",
			err:  &manifestreader.FetchError{Reason: manifestreader.ReasonDenied},
			want: "manifestreader: fetch failed: denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Helper()

			t.Parallel()

			assert.Equal(t, tt.want, tt.err.Error())

			if tt.err.Err != nil {
				assert.ErrorIs(t, tt.err, cause)
			}
		})
	}
}

func TestReader_WrongKindIsRejectedWhole(t *testing.T) {
	t.Parallel()

	runSteps(t, []step{{
		name: "a flag-set document where the manifest is expected",
		act: func(t *testing.T, store *fakeStore) {
			t.Helper()

			data := mustMarshal(t, manifestOf(t, 2, threeObjects()...))
			store.put(manifestKey, replaceOnce(data, "kind: Manifest", "kind: FlagSet"))
		},
		wantCalls: condGet,
		check: func(t *testing.T, status manifestreader.Status) {
			t.Helper()

			assert.Equal(t, manifestreader.OutcomeInvalid, status.Manifest.Outcome)
			assert.Equal(t, "wrong-kind", status.Manifest.Reason)
			assert.Equal(t, uint64(1), status.Manifest.Generation)
		},
	}})
}
