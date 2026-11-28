package flagruntime_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

const (
	anonLive    = "live"
	anonOther   = "other"
	anonOn      = "auth: {anonymousFlagSet: live}\n"
	anonWarning = "the anonymous flag set is not in the generation"
)

// anonContent holds a flag set that serves, another one that serves, and one that is not available.
func anonContent(t *testing.T, generation uint64) *flagruntime.Content {
	t.Helper()

	return content(generation, objs{
		flagsName: flagsObj(1, "f"),
		setsName: setsObj(t,
			fset(anonLive, src(flagsName), keyA),
			fset(anonOther, src(flagsName), "key-other"),
			fset(pendingSet, src("nope"), "key-pending")),
	})
}

// A request without a key routes to the anonymous flag set of the file, with the route a key of that flag set would
// get. A key that is given never does: a wrong key is unknown, an admin key is an admin key.
func TestRoute_Anonymous(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantStatus    *flagruntime.AnonymousStatus
		name          string
		bootstrap     string
		key           string
		wantSet       string
		wantRoute     flagruntime.Route
		wantErrNilSet bool
	}{
		{
			name:          "a request without a key and no anonymous flag set is unknown",
			bootstrap:     "auth: {adminKeys: [admin-2]}\n",
			wantRoute:     flagruntime.RouteUnknown,
			wantErrNilSet: true,
		},
		{
			name: "a request without a key is served by the anonymous flag set", key: "",
			bootstrap: anonOn, wantSet: anonLive, wantRoute: flagruntime.RouteFlagSet,
			wantStatus: &flagruntime.AnonymousStatus{FlagSet: anonLive, State: flagruntime.StateServing},
		},
		{
			name: "a wrong key is unknown even when there is an anonymous flag set", key: "wrong-key",
			bootstrap: anonOn, wantRoute: flagruntime.RouteUnknown, wantErrNilSet: true,
			wantStatus: &flagruntime.AnonymousStatus{FlagSet: anonLive, State: flagruntime.StateServing},
		},
		{
			name: "a key of a flag set is served by its own flag set", key: "key-other",
			bootstrap: anonOn, wantSet: anonOther, wantRoute: flagruntime.RouteFlagSet,
			wantStatus: &flagruntime.AnonymousStatus{FlagSet: anonLive, State: flagruntime.StateServing},
		},
		{
			name: "an admin key stays an admin key", key: adminOne,
			bootstrap: "auth: {anonymousFlagSet: live, adminKeys: [" + adminOne + "]}\n",
			wantRoute: flagruntime.RouteAdmin, wantErrNilSet: true,
			wantStatus: &flagruntime.AnonymousStatus{FlagSet: anonLive, State: flagruntime.StateServing},
		},
		{
			name:      "an anonymous flag set that is not available answers as a pending flag set",
			bootstrap: "auth: {anonymousFlagSet: " + pendingSet + "}\n", wantSet: pendingSet,
			wantRoute:  flagruntime.RoutePending,
			wantStatus: &flagruntime.AnonymousStatus{FlagSet: pendingSet, State: flagruntime.StatePending},
		},
		{
			name:      "an anonymous flag set that the generation does not hold answers as a pending flag set",
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

func revision(c *flagruntime.Content, n uint64) *flagruntime.Content {
	c.Revision = n

	return c
}

// await runs Await in a goroutine and returns the channel of its answer.
func await(t *testing.T, eng *flagruntime.Engine, holder *flagruntime.Holder, rev uint64) chan flagruntime.Reload {
	t.Helper()

	out := make(chan flagruntime.Reload, 1)

	go func() {
		got, err := eng.Await(t.Context(), rev)
		assert.NoError(t, err)

		// what the call answers is what the runtime serves: the recovered flags are there when it returns
		flagSet, ok := holder.Load().FlagSets["a"]
		assert.True(t, ok && flagSet.Flags != nil)

		if ok {
			assert.Contains(t, flagSet.Flags, "x", "the flags served when the call returned")
		}

		out <- got
	}()

	return out
}

// Await waits for the revision of a snapshot, not for a generation: an object that recovers at the generation the
// runtime already serves is a revision of its own, decided only when the engine applied it, and the engine that
// keeps the latest content only answers the waiters of the revisions it dropped.
func TestEngineAwaitsTheRevision(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	entered := make(chan struct{}, 8)

	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})

	harness := newRig(t, flagruntime.Hooks{BeforeSwap: func(candidate *flagruntime.Runtime) error {
		if candidate.Snapshot >= 2 {
			entered <- struct{}{}

			<-gate
		}

		return nil
	}})
	harness.start()

	stale := flagruntime.Object{Kind: flagruntime.KindFlags, State: flagruntime.ObjectStale, Reason: "gone"}
	res := harness.feed(revision(content(1, simpleObjs(t, 1).with(objs{"f1": stale})), 1))
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome)

	applied, err := harness.eng.Await(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, flagruntime.OutcomeApplied, applied.Outcome)

	// the object recovers at the same generation: the runtime serves generation 1 all along, the revision 2 is not
	// decided before the engine applied it
	harness.eng.Submit(revision(content(1, simpleObjs(t, 1)), 2))

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the engine did not reach the swap")
	}

	// a call that is cancelled while the engine is paused is not answered with a decision: there is none yet
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = harness.eng.Await(cancelled, 2)
	require.ErrorIs(t, err, context.Canceled, "the recovery is not decided before the engine applied it")

	waited := await(t, harness.eng, harness.holder, 2)

	assert.Empty(t, harness.holder.Load().FlagSets["a"].Flags, "the flag set serves no flag before the apply")

	// the revisions 3 and 4 wait while the engine is paused: it keeps the latest, and both waiters are answered
	harness.eng.Submit(revision(content(2, simpleObjs(t, 2)), 3))
	harness.eng.Submit(revision(content(3, simpleObjs(t, 3)), 4))

	third, fourth := await(t, harness.eng, harness.holder, 3), await(t, harness.eng, harness.holder, 4)

	close(gate)

	for name, ch := range map[string]chan flagruntime.Reload{"2": waited, "3": third, "4": fourth} {
		select {
		case got := <-ch:
			assert.Equal(t, flagruntime.OutcomeApplied, got.Outcome, "revision "+name)
		case <-time.After(5 * time.Second):
			require.FailNow(t, "a call did not return", "revision %s", name)
		}
	}

	assert.Contains(t, harness.holder.Load().FlagSets["a"].Flags, "x", "the flag set serves the recovered flags")
}

// A content the engine rejects is a decision too, with its code; a cancelled context ends the wait.
func TestEngineAwaitRejectedAndCancelled(t *testing.T) {
	t.Parallel()

	harness := newRig(t, flagruntime.Hooks{})
	harness.start()

	res := harness.feed(revision(content(1, simpleObjs(t, 1)), 1))
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome)

	bad := revision(content(2, simpleObjs(t, 2).with(objs{"f1": {Kind: flagruntime.KindFlags, State: "bogus"}})), 2)
	res = harness.feed(bad)
	require.Equal(t, flagruntime.OutcomeRejected, res.Outcome)

	rejected, err := harness.eng.Await(t.Context(), 2)
	require.NoError(t, err)
	assert.Equal(t, flagruntime.OutcomeRejected, rejected.Outcome)
	assert.Equal(t, flagruntime.CodeInvalidContent, rejected.Code)

	none, err := harness.eng.Await(t.Context(), 0)
	require.NoError(t, err, "revision 0 has nothing to wait for")
	assert.Equal(t, flagruntime.OutcomeApplied, none.Outcome)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = harness.eng.Await(ctx, 9)
	require.ErrorIs(t, err, context.Canceled)
}
