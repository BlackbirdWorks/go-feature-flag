package flagruntime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

const secretCanary = "canary-7f3a9c1e-secret-key"

// recordingHandler keeps every record that is logged, to scan it for a key.
type recordingHandler struct {
	records *[]string
	mu      *sync.Mutex
}

func newRecordingHandler() recordingHandler {
	return recordingHandler{records: &[]string{}, mu: &sync.Mutex{}}
}

func (recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h recordingHandler) Handle(
	_ context.Context,
	rec slog.Record, //nolint:gocritic // the slog.Handler signature
) error {
	var line bytes.Buffer

	line.WriteString(rec.Message)
	rec.Attrs(func(attr slog.Attr) bool {
		line.WriteString(" " + attr.Key + "=" + attr.Value.String())

		return true
	})

	h.mu.Lock()
	*h.records = append(*h.records, line.String())
	h.mu.Unlock()

	return nil
}

func (h recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h recordingHandler) WithGroup(string) slog.Handler      { return h }

func (h recordingHandler) text() string {
	h.mu.Lock()
	defer h.mu.Unlock()

	var all bytes.Buffer
	for _, rec := range *h.records {
		all.WriteString(rec + "\n")
	}

	return all.String()
}

// routeRig starts an engine on a content and returns it with the running runtime.
func routeRig(t *testing.T, handler slog.Handler, first *flagruntime.Content) *rig {
	t.Helper()

	harness := newRigLogger(t, slog.New(handler), flagruntime.Hooks{})
	harness.start()

	res := harness.feed(first)
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)

	return harness
}

// Every kind of key resolves to the route the HTTP layer maps to a status: a known key to its flag set, an unknown
// key and the empty key to 401, an admin key to no flag set, a key of a pending flag set (a placeholder) to the
// upstream style empty answers. The error of a route is a sentinel and never holds the key.
func TestRoute_Kinds(t *testing.T) {
	t.Parallel()

	harness := routeRig(t, slog.DiscardHandler, content(1, objs{
		flagsName: flagsObj(1, "f"),
		setsName: setsObj(t,
			fset("live", src(flagsName), keyA, keyB),
			fset(pendingSet, src("nope"), "key-pending")),
	}))
	harness.eng.Reload(bootstrapFromYAML(t, "auth:\n  adminKeys:\n    - "+adminOne+"\n"))
	harness.nextRound()

	run := harness.holder.Load()

	tests := []struct {
		wantErr      error
		name         string
		key          string
		wantFlagSet  string
		wantRouteStr string
		wantRoute    flagruntime.Route
	}{
		{
			name:         "known key",
			key:          keyA,
			wantRoute:    flagruntime.RouteFlagSet,
			wantFlagSet:  "live",
			wantRouteStr: "flagSet",
		},
		{
			name:         "second key of the same set",
			key:          keyB,
			wantRoute:    flagruntime.RouteFlagSet,
			wantFlagSet:  "live",
			wantRouteStr: "flagSet",
		},
		{
			name: "pending flag set", key: "key-pending", wantRoute: flagruntime.RoutePending, wantFlagSet: pendingSet,
			wantErr: flagruntime.ErrFlagSetUnavailable, wantRouteStr: pendingSet,
		},
		{name: "admin key", key: adminOne, wantRoute: flagruntime.RouteAdmin, wantRouteStr: "admin"},
		{
			name: "unknown key", key: "nobody", wantRoute: flagruntime.RouteUnknown, wantErr: flagruntime.ErrUnknownKey,
			wantRouteStr: unknownStr,
		},
		{
			name: "empty key", key: "", wantRoute: flagruntime.RouteUnknown, wantErr: flagruntime.ErrUnknownKey,
			wantRouteStr: unknownStr,
		},
		{
			name:         "prefix of a key",
			key:          keyA[:3],
			wantRoute:    flagruntime.RouteUnknown,
			wantErr:      flagruntime.ErrUnknownKey,
			wantRouteStr: unknownStr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			set, route := run.Route(tt.key)
			assert.Equal(t, tt.wantRoute, route)
			assert.Equal(t, tt.wantRouteStr, route.String())
			assert.Equal(t, tt.wantErr, route.Err())

			if tt.wantFlagSet == "" {
				assert.Nil(t, set)

				return
			}

			require.NotNil(t, set)
			assert.Equal(t, tt.wantFlagSet, set.Name)
		})
	}
}

// An out-of-range route is unknown: the HTTP layer never maps a route to a success by accident.
func TestRoute_UnknownValue(t *testing.T) {
	t.Parallel()

	assert.Equal(t, unknownStr, flagruntime.Route(200).String())
	assert.Equal(t, flagruntime.ErrUnknownKey, flagruntime.Route(200).Err())
}

// A valid key of an unavailable flag set follows upstream: a single evaluation says the flag is not found, a bulk
// evaluation is valid and empty, the listings are empty and the change hash is the one of the empty set.
func TestRoute_PendingFlagSetAnswers(t *testing.T) {
	t.Parallel()

	harness := routeRig(t, slog.DiscardHandler, content(1, objs{
		setsName: setsObj(t, fset(pendingSet, src("not-listed"), "key-pending")),
	}))

	set, route := harness.holder.Load().Route("key-pending")
	require.Equal(t, flagruntime.RoutePending, route)
	require.NotNil(t, set)
	assert.Equal(t, flagruntime.StatePending, set.State)

	eval := flagruntime.NewEvaluator(newService(t), nil)
	ctx := ffcontext.NewEvaluationContext("u")

	single, err := eval.RawVariation(set, "any", ctx, nil)
	require.ErrorIs(t, err, flagruntime.ErrFlagNotPresent)
	assert.Equal(t, flagdef.ErrorCodeFlagNotFound, single.ErrorCode)
	assert.Equal(t, flagdef.ReasonError, single.Reason)

	all := eval.AllFlagsState(set, ctx)
	assert.True(t, all.IsValid())
	assert.Equal(t, `{"valid":true}`, mustJSON(t, all))
	assert.Empty(t, set.Configuration(nil))

	empty, err := (*flagruntime.FlagSet)(nil).Change()
	require.NoError(t, err)

	change, err := set.Change()
	require.NoError(t, err)
	assert.Equal(t, empty, change)
}

// A flag set removed by the publisher leaves the routing table in that generation: its keys are unknown at once and
// another flag set can hold them in the same generation. A key rotation (both keys listed) never fails the
// surviving key.
func TestRoute_RemovalReuseAndRotation(t *testing.T) {
	t.Parallel()

	flags := flagsObj(1, "f")
	harness := routeRig(t, slog.DiscardHandler, content(1, objs{
		flagsName: flags,
		setsName:  setsObj(t, fset("old", src(flagsName), keyA), fset("other", src(flagsName), keyB)),
	}))

	routeOf := func(key string) (string, flagruntime.Route) {
		set, route := harness.holder.Load().Route(key)
		if set == nil {
			return "", route
		}

		return set.Name, route
	}

	name, route := routeOf(keyA)
	assert.Equal(t, "old", name)
	assert.Equal(t, flagruntime.RouteFlagSet, route)

	// generation 2: rotation overlap, both keys of the flag set "other" are listed; "old" is removed and its key is
	// taken over by "new" in the same generation
	harness.feed(content(2, objs{
		flagsName: flags,
		setsName:  setsObj(t, fset("other", src(flagsName), keyB, "key-b2"), fset("new", src(flagsName), keyA)),
	}))

	for _, key := range []string{keyB, "key-b2"} {
		name, route = routeOf(key)
		assert.Equal(t, "other", name, key)
		assert.Equal(t, flagruntime.RouteFlagSet, route, key)
	}

	name, route = routeOf(keyA)
	assert.Equal(t, "new", name, "the key of the removed flag set is reusable at once")
	assert.Equal(t, flagruntime.RouteFlagSet, route)

	// generation 3: the first key of the rotation is dropped; the surviving one never stops working, and the key
	// of a set that is removed is unknown in that generation
	harness.feed(content(3, objs{
		flagsName: flags,
		setsName:  setsObj(t, fset("other", src(flagsName), "key-b2")),
	}))

	name, route = routeOf("key-b2")
	assert.Equal(t, "other", name)
	assert.Equal(t, flagruntime.RouteFlagSet, route)

	for _, key := range []string{keyA, keyB} {
		name, route = routeOf(key)
		assert.Empty(t, name, key)
		assert.Equal(t, flagruntime.RouteUnknown, route, key)
	}

	assert.Equal(t, []flagruntime.WithdrawnSet{{Name: "old", Generation: 2}, {Name: "new", Generation: 3}},
		harness.holder.Load().Status.Withdrawn)
}

// No API key ever shows in an error, in the status or in what the engine logs: the canary key is published in a
// valid flag set, in a rejected entry, and in a duplicate, and is searched in the JSON of the status, the result of
// the round, the errors of the routes and every log record.
func TestRoute_KeyIsNeverExposed(t *testing.T) {
	t.Parallel()

	handler := newRecordingHandler()
	canaryDup := secretCanary + "-dup"
	harness := routeRig(t, handler, content(1, objs{
		flagsName: flagsObj(1, "f"),
		setsName: setsObj(t,
			fset("good", src(flagsName), secretCanary, canaryDup),
			fset("dup", src(flagsName), secretCanary),
			fset("bad", src("missing-source"), secretCanary+"-bad"),
			fset("", src(flagsName), secretCanary+"-noname")),
	}))

	run := harness.holder.Load()
	status, err := json.Marshal(run.Status)
	require.NoError(t, err)

	result, err := json.Marshal(harness.eng.Result())
	require.NoError(t, err)

	var routeErrors bytes.Buffer

	for _, key := range []string{secretCanary, canaryDup, "unknown-" + secretCanary} {
		_, route := run.Route(key)
		routeErrors.WriteString(errText(route.Err()))
	}

	_, err = run.FlagSets["good"].Change()
	require.NoError(t, err)

	eval := flagruntime.NewEvaluator(newService(t), nil)
	_, evalErr := eval.RawVariation(
		run.FlagSets["good"],
		"no-such-flag",
		ffcontext.NewEvaluationContext(secretCanary),
		nil,
	)

	for name, text := range map[string]string{
		"status": string(status), "round result": string(result), "route errors": routeErrors.String(),
		"logs": handler.text(), "evaluation error": errText(evalErr),
	} {
		assert.NotContains(t, text, secretCanary, name)
	}

	assert.NotEmpty(t, status)
	assert.Contains(t, string(status), `"apiKeys":`, "the status carries counts, not keys")
	assert.Equal(t, 2, run.FlagSets["good"].APIKeys)
	assert.NotErrorIs(t, flagruntime.ErrUnknownKey, flagruntime.ErrFlagSetUnavailable)
}

// The lookup does not allocate and takes no lock: a request resolves its key with one map read. It is not parallel
// on purpose: AllocsPerRun counts the allocations of the parallel tests too.
//
//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestRoute_DoesNotAllocate(t *testing.T) {
	run := runtimeWith(t, objs{
		flagsName: flagsObj(1, "f"),
		setsName:  setsObj(t, fset("live", src(flagsName), keyA)),
	})

	tests := []struct {
		wantErr   error
		key       string
		wantRoute flagruntime.Route
	}{
		{key: keyA, wantRoute: flagruntime.RouteFlagSet},
		{key: unknownStr, wantRoute: flagruntime.RouteUnknown, wantErr: flagruntime.ErrUnknownKey},
	}

	for _, tt := range tests {
		var (
			gotSet   *flagruntime.FlagSet
			gotRoute flagruntime.Route
			gotErr   error
		)

		allocs := testing.AllocsPerRun(100, func() {
			gotSet, gotRoute = run.Route(tt.key)
			gotErr = gotRoute.Err()
		})

		assert.Zero(t, allocs, tt.key)
		assert.Equal(t, tt.wantRoute, gotRoute, tt.key)
		assert.Equal(t, tt.wantErr, gotErr, tt.key)
		assert.Equal(t, tt.wantRoute == flagruntime.RouteFlagSet, gotSet != nil, tt.key)
	}

	holder := flagruntime.NewHolder()

	var loaded flagruntime.Route

	assert.Zero(t, testing.AllocsPerRun(100, func() { _, loaded = holder.Load().Route(keyA) }))
	assert.Equal(t, flagruntime.RouteUnknown, loaded, "the empty runtime knows no key")
}
