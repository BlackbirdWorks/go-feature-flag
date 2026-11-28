package corehttp_test

import (
	"context"
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
// exporters of the core, which would add the cost of the event to the measure.
func (h hotPath) evaluate(tb testing.TB) {
	tb.Helper()

	flagSet, err := h.resolver.FlagSet(tenantKey)
	require.NoError(tb, err)

	evalCtx := ffcontext.NewEvaluationContext("user-1")
	_, err = flagSet.RawVariation(context.Background(), "untracked-flag", evalCtx, nil)
	require.NoError(tb, err)
}

func newHotPath(tb testing.TB) hotPath {
	tb.Helper()

	return hotPath{resolver: corehttp.NewAdapter(newV2(tb).core)}
}

// TestHotPath_AllocationsBudget pins the allocations of the authentication and of one evaluation on the v2 path to the
// budget they had when they were measured against the first version of the relay: the authentication allocates once,
// one evaluation twice. The figures hold under -race too. It is not parallel on purpose: AllocsPerRun counts the
// allocations of the parallel tests too.
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestHotPath_AllocationsBudget(t *testing.T) {
	path := newHotPath(t)

	tests := []struct {
		run        func(hotPath, testing.TB)
		name       string
		wantAllocs float64
	}{
		{name: "authentication", run: hotPath.authenticate, wantAllocs: 1},
		{name: "single evaluation", run: hotPath.evaluate, wantAllocs: 2},
	}
	for _, tt := range tests {
		got := testing.AllocsPerRun(200, func() { tt.run(path, t) })

		assert.Positive(t, got, "%s: the measure sees the allocations", tt.name)
		assert.LessOrEqual(t, got, tt.wantAllocs, "%s: allocations per operation", tt.name)
	}
}

func BenchmarkAuthenticateFlagSet(b *testing.B) {
	path := newHotPath(b)

	b.ReportAllocs()

	for b.Loop() {
		path.authenticate(b)
	}
}

func BenchmarkSingleEvaluation(b *testing.B) {
	path := newHotPath(b)

	b.ReportAllocs()

	for b.Loop() {
		path.evaluate(b)
	}
}
