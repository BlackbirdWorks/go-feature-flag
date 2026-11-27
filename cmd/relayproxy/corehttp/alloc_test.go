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

func (h hotPath) authenticate(t testing.TB) {
	t.Helper()

	_, ok := helper.AuthenticateFlagSet(t.Context(), h.resolver, tenantKey)
	require.True(t, ok)
}

// evaluate uses a flag that does not track its events: the evaluation of a tracked flag makes an event for the
// exporters of the core, which the v1 relay without exporters does not.
func (h hotPath) evaluate(t testing.TB) {
	t.Helper()

	flagSet, err := h.resolver.FlagSet(tenantKey)
	require.NoError(t, err)

	evalCtx := ffcontext.NewEvaluationContext("user-1")
	_, err = flagSet.RawVariation("untracked-flag", evalCtx, nil)
	require.NoError(t, err)
}

func hotPaths(t testing.TB) (v1, v2 hotPath) {
	t.Helper()

	_, manager := newV1Manager(t)
	rig := newV2(t)

	return hotPath{resolver: manager}, hotPath{resolver: corehttp.NewAdapter(rig.core)}
}

// TestHotPath_AllocationsNotAboveV1 pins the allocations of the authentication and of one evaluation on the v2 path
// to the ones of the v1 path. It counts the allocations of both paths in the same process, so the figures hold under
// -race too. It is not parallel on purpose: AllocsPerRun counts the allocations of the parallel tests too.
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestHotPath_AllocationsNotAboveV1(t *testing.T) {
	v1, v2 := hotPaths(t)

	tests := []struct {
		run  func(hotPath, testing.TB)
		name string
	}{
		{name: "authentication", run: func(h hotPath, tb testing.TB) { h.authenticate(tb) }},
		{name: "single evaluation", run: func(h hotPath, tb testing.TB) { h.evaluate(tb) }},
	}
	for _, tt := range tests {
		base := testing.AllocsPerRun(200, func() { tt.run(v1, t) })
		got := testing.AllocsPerRun(200, func() { tt.run(v2, t) })

		assert.LessOrEqual(t, got, base, "%s: allocations per operation: v2 %v, v1 %v", tt.name, got, base)
		t.Logf("%-18s v1 %v allocs, v2 %v allocs", tt.name, base, got)
	}
}

func BenchmarkAuthenticateFlagSet(b *testing.B) {
	v1, v2 := hotPaths(b)

	for name, path := range map[string]hotPath{"v1": v1, "v2": v2} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				path.authenticate(b)
			}
		})
	}
}

func BenchmarkSingleEvaluation(b *testing.B) {
	v1, v2 := hotPaths(b)

	for name, path := range map[string]hotPath{"v1": v1, "v2": v2} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				path.evaluate(b)
			}
		})
	}
}
