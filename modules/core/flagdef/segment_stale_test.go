package flagdef_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// staleProvider answers from a memory set and reports whether its data is older than it should be.
type staleProvider struct {
	inner segment.Provider
	stale bool
}

func (p staleProvider) Contains(ctx context.Context, lookups []segment.Lookup) error {
	return p.inner.Contains(ctx, lookups)
}

func (p staleProvider) Stale() bool { return p.stale }

// A provider that serves a last known good set reports STALE: the answers are used, the status says they are old.
func TestSegmentStatusStale(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		key         string
		wantVariant string
		wantStatus  segment.Status
		stale       bool
	}{
		{name: "fresh member", key: keyAlice, wantVariant: variantOn, wantStatus: segment.StatusHealthy},
		{
			name: "stale member is still a member", key: keyAlice, stale: true,
			wantVariant: variantOn, wantStatus: segment.StatusStale,
		},
		{
			name: "stale non member", key: keyBob, stale: true,
			wantVariant: variantOff, wantStatus: segment.StatusStale,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			provider := staleProvider{
				inner: segment.NewMemoryProvider(map[string][]string{segBeta: {keyAlice}}),
				stale: tt.stale,
			}
			svc := flagdef.NewService()
			flag := onOffFlag(queryRule("r", nikunjyInclude))
			require.NoError(t, flag.IsValid(svc))

			_, details := flag.Value(
				"f", ffcontext.NewEvaluationContext(tt.key), flagdef.Context{Service: svc, Segments: provider},
			)

			assert.Equal(t, tt.wantVariant, details.Variant)
			assert.Equal(t, tt.wantStatus, details.SegmentStatus)
		})
	}
}
