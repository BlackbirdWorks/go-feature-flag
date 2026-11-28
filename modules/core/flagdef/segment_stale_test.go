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

// panicsOnStale answers and then panics when it is asked whether it is stale.
type panicsOnStale struct{ inner segment.Provider }

func (p panicsOnStale) Contains(ctx context.Context, lookups []segment.Lookup) error {
	return p.inner.Contains(ctx, lookups)
}

func (panicsOnStale) Stale() bool { panic("stale") }

// A provider that panics in Stale is not trusted: the answers are unanswered, the failure policy applies and the
// status is STORE_ERROR; the rule does not become false as a whole.
func TestSegmentStalePanicIsAProviderPanic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		query       string
		wantVariant string
	}{
		{name: "member is not a member", query: nikunjyInclude, wantVariant: variantOff},
		{name: "negated: matches", query: nikunjyExclude, wantVariant: variantOn},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			provider := panicsOnStale{inner: segment.NewMemoryProvider(map[string][]string{segBeta: {keyAlice}})}
			svc := flagdef.NewService()
			flag := onOffFlag(queryRule("r", tt.query))
			require.NoError(t, flag.IsValid(svc))

			_, details := flag.Value(
				"f", ffcontext.NewEvaluationContext(keyAlice), flagdef.Context{Service: svc, Segments: provider},
			)

			assert.Equal(t, tt.wantVariant, details.Variant)
			assert.Equal(t, segment.StatusStoreError, details.SegmentStatus)
		})
	}
}
