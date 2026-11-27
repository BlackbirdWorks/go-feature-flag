package manifestreader_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// A refresh polls at once, with no tick of the scheduler: it reports the generation the reader holds, whether the
// round changed it, and the class of a failure.
func TestReader_Refresh(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		wantPoll       string
		publishGen     uint64
		wantGeneration uint64
		wantChanged    bool
	}{
		{
			name:           "a new generation",
			publishGen:     2,
			wantPoll:       manifestreader.PollUpdated,
			wantGeneration: 2,
			wantChanged:    true,
		},
		{name: "nothing new", wantPoll: manifestreader.PollNotModified, wantGeneration: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			publish(t, store, 1, threeObjects()...)

			rdr := startRig(t, store, &manifestreader.Config{})

			if tt.publishGen != 0 {
				publish(t, store, tt.publishGen, threeObjects()...)
			}

			result, err := rdr.reader.Refresh(t.Context())
			require.NoError(t, err)

			assert.Equal(t, tt.wantPoll, result.Poll.Result)
			assert.Equal(t, tt.wantGeneration, result.Generation)
			assert.Equal(t, tt.wantChanged, result.Changed)
		})
	}
}
