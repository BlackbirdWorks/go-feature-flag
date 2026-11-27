package corehttp_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/ffcontext"
)

// hotPath is what a request does before its handler runs the evaluation: it authenticates the key (the flag set is
// resolved once and kept in the context) and evaluates one flag.
type hotPath struct {
	resolver service.FlagSetResolver
}

func (h hotPath) authenticate(tb testing.TB) {
	tb.Helper()

	_, ok := helper.AuthenticateFlagSet(tb.Context(), h.resolver, tenantKey)
	require.True(tb, ok)
}

// evaluate uses a flag that does not track its events: the evaluation of a tracked flag makes an event for the
// exporters of the core, which the v1 relay without exporters does not.
func (h hotPath) evaluate(tb testing.TB) {
	tb.Helper()

	flagSet, err := h.resolver.FlagSet(tenantKey)
	require.NoError(tb, err)

	evalCtx := ffcontext.NewEvaluationContext("user-1")
	_, err = flagSet.RawVariation("untracked-flag", evalCtx, nil)
	require.NoError(tb, err)
}

func hotPaths(tb testing.TB) (hotPath, hotPath) {
	tb.Helper()

	_, manager := newV1Manager(tb)
	rig := newV2(tb)

	return hotPath{resolver: manager}, hotPath{resolver: corehttp.NewAdapter(rig.core)}
}

// TestHotPath_AllocationsNotAboveV1 pins the allocations of the authentication and of one evaluation on the v2 path
// to the ones of the v1 path. It counts the allocations of both paths in the same process, so the figures hold under
// -race too. It is not parallel on purpose: AllocsPerRun counts the allocations of the parallel tests too.
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestHotPath_AllocationsNotAboveV1(t *testing.T) {
	baseline, current := hotPaths(t)

	tests := []struct {
		run  func(hotPath, testing.TB)
		name string
	}{
		{name: "authentication", run: hotPath.authenticate},
		{name: "single evaluation", run: hotPath.evaluate},
	}
	for _, tt := range tests {
		base := testing.AllocsPerRun(200, func() { tt.run(baseline, t) })
		got := testing.AllocsPerRun(200, func() { tt.run(current, t) })

		assert.LessOrEqual(t, got, base, "%s: allocations per operation: v2 %v, v1 %v", tt.name, got, base)
		t.Logf("%-18s v1 %v allocs, v2 %v allocs", tt.name, base, got)
	}
}

func BenchmarkAuthenticateFlagSet(b *testing.B) {
	baseline, current := hotPaths(b)

	for name, path := range map[string]hotPath{"v1": baseline, "v2": current} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				path.authenticate(b)
			}
		})
	}
}

func BenchmarkSingleEvaluation(b *testing.B) {
	baseline, current := hotPaths(b)

	for name, path := range map[string]hotPath{"v1": baseline, "v2": current} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				path.evaluate(b)
			}
		})
	}
}
