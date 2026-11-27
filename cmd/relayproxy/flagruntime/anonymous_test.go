package flagruntime_test

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

const (
	anonLive    = "live"
	anonWarning = "the anonymous flag set is not in the generation"
)

// anonContent holds a flag set that serves, another one that serves, and one that is not available.
func anonContent(t *testing.T, generation uint64) *flagruntime.Content {
	t.Helper()

	return content(generation, objs{
		flagsName: flagsObj(1, "f"),
		setsName: setsObj(t,
			fset(anonLive, src(flagsName), keyA),
			fset("other", src(flagsName), "key-other"),
			fset(pendingSet, src("nope"), "key-pending")),
	})
}

// A request without a key routes to the anonymous flag set of the file, with the route a key of that flag set would
// get. A key that is given never does: a wrong key is unknown, an admin key is an admin key.
func TestRoute_Anonymous(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		bootstrap     string
		key           string
		wantSet       string
		wantRoute     flagruntime.Route
		wantStatus    *flagruntime.AnonymousStatus
		wantErrNilSet bool
	}{
		{
			name: "a request without a key and no anonymous flag set is unknown", bootstrap: "auth: {adminKeys: [admin-2]}\n",
			wantRoute: flagruntime.RouteUnknown, wantErrNilSet: true,
		},
		{
			name: "a request without a key is served by the anonymous flag set", key: "",
			bootstrap: "auth: {anonymousFlagSet: live}\n", wantSet: anonLive, wantRoute: flagruntime.RouteFlagSet,
			wantStatus: &flagruntime.AnonymousStatus{FlagSet: anonLive, State: flagruntime.StateServing},
		},
		{
			name: "a wrong key is unknown even when there is an anonymous flag set", key: "wrong-key",
			bootstrap: "auth: {anonymousFlagSet: live}\n", wantRoute: flagruntime.RouteUnknown, wantErrNilSet: true,
			wantStatus: &flagruntime.AnonymousStatus{FlagSet: anonLive, State: flagruntime.StateServing},
		},
		{
			name: "a key of a flag set is served by its own flag set", key: "key-other",
			bootstrap: "auth: {anonymousFlagSet: live}\n", wantSet: "other", wantRoute: flagruntime.RouteFlagSet,
			wantStatus: &flagruntime.AnonymousStatus{FlagSet: anonLive, State: flagruntime.StateServing},
		},
		{
			name: "an admin key stays an admin key", key: adminOne,
			bootstrap: "auth: {anonymousFlagSet: live, adminKeys: [" + adminOne + "]}\n",
			wantRoute: flagruntime.RouteAdmin, wantErrNilSet: true,
			wantStatus: &flagruntime.AnonymousStatus{FlagSet: anonLive, State: flagruntime.StateServing},
		},
		{
			name: "an anonymous flag set that is not available answers as a pending flag set",
			bootstrap: "auth: {anonymousFlagSet: " + pendingSet + "}\n", wantSet: pendingSet,
			wantRoute:  flagruntime.RoutePending,
			wantStatus: &flagruntime.AnonymousStatus{FlagSet: pendingSet, State: flagruntime.StatePending},
		},
		{
			name: "an anonymous flag set that the generation does not hold answers as a pending flag set",
			bootstrap: "auth: {anonymousFlagSet: later}\n", wantSet: "later", wantRoute: flagruntime.RoutePending,
			wantStatus: &flagruntime.AnonymousStatus{FlagSet: "later", State: flagruntime.StateMissing},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			harness := routeRig(t, slog.DiscardHandler, anonContent(t, 1))
			res := harness.reload(bootstrapFromYAML(t, "version: 2\n"+tt.bootstrap))
			require.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)

			run := harness.holder.Load()
			set, route := run.Route(tt.key)

			assert.Equal(t, tt.wantRoute, route)

			if tt.wantErrNilSet {
				assert.Nil(t, set)
			} else {
				require.NotNil(t, set)
				assert.Equal(t, tt.wantSet, set.Name)
			}

			assert.Equal(t, tt.wantStatus, run.Status.Anonymous)
		})
	}
}

// The anonymous flag set applies on the reload of the bootstrap file, without a restart. A flag set that a later
// generation publishes starts serving at that generation, and the warning is logged once per generation.
func TestAnonymousFlagSetFollowsReloadsAndGenerations(t *testing.T) {
	t.Parallel()

	handler := newRecordingHandler()
	harness := routeRig(t, slog.New(handler).Handler(), content(1, objs{
		flagsName: flagsObj(1, "f"), setsName: setsObj(t, fset(anonLive, src(flagsName), keyA)),
	}))

	_, route := harness.holder.Load().Route("")
	assert.Equal(t, flagruntime.RouteUnknown, route, "off by default")

	res := harness.reload(bootstrapFromYAML(t, "version: 2\nauth: {anonymousFlagSet: later}\n"))
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome)

	run := harness.holder.Load()
	assert.Empty(t, run.Status.PendingRestart, "it applies in place")

	_, route = run.Route("")
	assert.Equal(t, flagruntime.RoutePending, route)
	assert.Equal(t, 1, strings.Count(handler.text(), anonWarning), "the warning is logged for the generation")

	// another bootstrap change of the same generation does not log it again
	res = harness.reload(bootstrapFromYAML(t, "version: 2\nauth: {anonymousFlagSet: later, adminKeys: [admin-2]}\n"))
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome)
	assert.Equal(t, 1, strings.Count(handler.text(), anonWarning))

	// the next generation still lacks it: the warning comes again
	res = harness.feed(content(2, objs{
		flagsName: flagsObj(2, "f"), setsName: setsObj(t, fset(anonLive, src(flagsName), keyA)),
	}))
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome)
	assert.Equal(t, 2, strings.Count(handler.text(), anonWarning))

	// the generation that publishes it makes the flag set serve, with no restart
	res = harness.feed(content(3, objs{
		flagsName: flagsObj(3, "f"),
		setsName:  setsObj(t, fset(anonLive, src(flagsName), keyA), fset("later", src(flagsName), "key-later")),
	}))
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome)

	set, route := harness.holder.Load().Route("")
	assert.Equal(t, flagruntime.RouteFlagSet, route)
	require.NotNil(t, set)
	assert.Equal(t, "later", set.Name)
	assert.Contains(t, set.Flags, "f")
	assert.Equal(t, 2, strings.Count(handler.text(), anonWarning), "nothing to warn about any more")

	// switching it off applies in place too
	res = harness.reload(bootstrapFromYAML(t, "version: 2\nauth: {adminKeys: [admin-2]}\n"))
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome)

	set, route = harness.holder.Load().Route("")
	assert.Nil(t, set)
	assert.Equal(t, flagruntime.RouteUnknown, route)
	assert.Nil(t, harness.holder.Load().Status.Anonymous)
}
