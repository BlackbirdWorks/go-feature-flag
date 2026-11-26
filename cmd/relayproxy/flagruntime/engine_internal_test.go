package flagruntime

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// Flag sets that come and go, with or without a consumer that reads, leave no state behind: the tracking of
// the flag sets the consumer knows is bounded by the live ones.
func TestEngineTrackingStaysBoundedUnderCreateAndWithdrawCycles(t *testing.T) {
	t.Parallel()

	const cycles = 10000

	tests := []struct {
		name     string
		consumer bool
	}{
		{name: "a consumer that reads every event", consumer: true},
		{name: "a consumer that reads nothing", consumer: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := flagdef.NewService()
			t.Cleanup(svc.Close)

			eng := NewEngine(nil, svc, NewHolder())
			flags := Object{Kind: KindFlags, Format: controlplane.FormatYAML, Bytes: []byte(internalFlags)}
			stay := controlplane.FlagSet{Name: stayName, Sources: []string{"f1"}, APIKeys: []string{keyStay}}
			generation := uint64(0)

			round := func(sets ...controlplane.FlagSet) {
				generation++

				eng.round(context.Background(), &Content{
					Generation: generation, Objects: map[string]Object{"f1": flags, setsKey: internalDoc(t, sets...)},
				}, nil)

				for tt.consumer && eng.queue.front() != nil {
					eng.delivered(eng.queue.front())
				}
			}

			round(stay)

			for cycle := range cycles {
				name := fmt.Sprintf("tmp-%d", cycle)

				round(stay, controlplane.FlagSet{Name: name, Sources: []string{"f1"}, APIKeys: []string{"key-" + name}})
				round(stay)
			}

			require.Equal(t, uint64(2*cycles+1), generation)

			// the flag set stayName was announced at the start; with no consumer its event still waits
			assert.LessOrEqual(t, len(eng.announced), 2)
			assert.LessOrEqual(t, len(eng.queue.byName), 2)
			assert.Contains(t, eng.announced, stayName)
			assert.NotContains(t, eng.announced, fmt.Sprintf("tmp-%d", cycles-1))
		})
	}
}
