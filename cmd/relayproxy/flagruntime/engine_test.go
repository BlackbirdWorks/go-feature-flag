package flagruntime_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

// errRefused is what the veto hook of the tests returns.
var errRefused = errors.New("refused by the hook")

func simpleObjs(t *testing.T, value int) objs {
	t.Helper()

	return objs{
		"f1":     flagsObj(value, "x", "y"),
		setsName: setsObj(t, fset("a", src("f1"), keyA)),
	}
}

func TestEngineAppliesContentWithOneStore(t *testing.T) {
	t.Parallel()

	r := newRig(t, flagruntime.Hooks{})

	empty := r.holder.Load()

	require.NotNil(t, empty, "the holder starts with a valid empty runtime")
	assert.Empty(t, empty.FlagSets)
	assert.Equal(t, uint64(0), empty.Snapshot)

	r.start()

	res := r.feed(content(7, simpleObjs(t, 1)))

	// the new flag set announces its flags: one event
	want := flagruntime.RoundResult{
		Outcome: flagruntime.OutcomeApplied, Code: flagruntime.CodeApplied, Generation: 7, Snapshot: 1, Events: 1,
	}

	assert.Equal(t, want, res)

	current := r.holder.Load()

	assert.Equal(t, uint64(7), current.Generation)
	assert.Equal(t, uint64(1), current.Snapshot)
	assert.Equal(t, uint64(1), current.Status.Snapshot)
	assert.Equal(t, uint64(7), current.Status.Generation)
	assert.Equal(t, res.Reload, current.Status.LastReload)
	assert.Equal(t, res, r.eng.Result())

	fs, ok := routedSet(current, keyA)
	require.True(t, ok)
	assert.Equal(t, "a", fs.Name)
	assert.EqualValues(t, 1, value(fs, "x"))

	res = r.feed(content(8, simpleObjs(t, 2)))

	assert.Equal(t, uint64(2), res.Snapshot, "the snapshot counts the swaps")
	assert.Equal(t, uint64(8), r.holder.Load().Generation)
}

func TestEngineEmptyContentIsAValidRuntime(t *testing.T) {
	t.Parallel()

	r := newRig(t, flagruntime.Hooks{})
	r.start()

	res := r.feed(content(1, objs{}))

	assert.Equal(t, flagruntime.OutcomeApplied, res.Outcome)

	current := r.holder.Load()

	assert.Equal(t, uint64(1), current.Generation)
	assert.Empty(t, current.FlagSets)
	assert.Empty(t, current.ByKey)

	_, ok := routedSet(current, "anything")
	assert.False(t, ok)
	assert.Empty(t, current.Status.FlagSets)
}

func TestEngineGenerationRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		second         *flagruntime.Content
		wantOutcome    flagruntime.Outcome
		wantCode       string
		wantGeneration uint64
		wantSnapshot   uint64
		wantStored     bool
	}{
		{
			name:           "a lower generation is ignored",
			second:         content(4, simpleObjs(t, 9)),
			wantOutcome:    flagruntime.OutcomeIgnored,
			wantCode:       flagruntime.CodeStaleGeneration,
			wantGeneration: 5,
			wantSnapshot:   1,
		},
		{
			name:           "the same generation with the same content is ignored",
			second:         content(5, simpleObjs(t, 1)),
			wantOutcome:    flagruntime.OutcomeIgnored,
			wantCode:       flagruntime.CodeDuplicate,
			wantGeneration: 5,
			wantSnapshot:   1,
		},
		{
			name:           "the same generation with other content is a conflict and is rejected",
			second:         content(5, simpleObjs(t, 9)),
			wantOutcome:    flagruntime.OutcomeRejected,
			wantCode:       flagruntime.CodeGenerationConflict,
			wantGeneration: 5,
			wantSnapshot:   1,
		},
		{
			name:           "a higher generation applies",
			second:         content(6, simpleObjs(t, 9)),
			wantOutcome:    flagruntime.OutcomeApplied,
			wantCode:       flagruntime.CodeApplied,
			wantGeneration: 6,
			wantSnapshot:   2,
			wantStored:     true,
		},
		{
			name:           "a higher generation with the same content still applies (no-op content)",
			second:         content(6, simpleObjs(t, 1)),
			wantOutcome:    flagruntime.OutcomeApplied,
			wantCode:       flagruntime.CodeApplied,
			wantGeneration: 6,
			wantSnapshot:   2,
			wantStored:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, flagruntime.Hooks{})
			r.start()

			r.feed(content(5, simpleObjs(t, 1)))

			before := r.holder.Load()
			res := r.feed(tt.second)
			after := r.holder.Load()

			assert.Equal(t, tt.wantOutcome, res.Outcome)
			assert.Equal(t, tt.wantCode, res.Code)
			assert.Equal(t, tt.wantGeneration, after.Generation)
			assert.Equal(t, tt.wantSnapshot, after.Snapshot)

			if tt.wantStored {
				assert.NotSame(t, before, after)
			} else {
				assert.Same(t, before, after, "nothing is stored")
			}
		})
	}
}

func TestEngineRejectedCandidateChangesNothing(t *testing.T) {
	t.Parallel()

	badFormat := flagsObj(1, "x")
	badFormat.Format = "xml"

	tests := []struct {
		bad      *flagruntime.Content
		name     string
		wantCode string
		veto     bool
	}{
		{
			name:     "generation zero",
			bad:      content(0, simpleObjs(t, 2)),
			wantCode: flagruntime.CodeInvalidContent,
		},
		{
			name:     "unknown object state",
			bad:      content(3, simpleObjs(t, 2).with(objs{"f1": {Kind: flagruntime.KindFlags, State: "bogus"}})),
			wantCode: flagruntime.CodeInvalidContent,
		},
		{
			name: "the invalid state is not an input",
			bad: content(
				3,
				simpleObjs(t, 2).with(objs{"f1": {Kind: flagruntime.KindFlags, State: flagruntime.ObjectInvalid}}),
			),
			wantCode: flagruntime.CodeInvalidContent,
		},
		{
			name:     "unsupported format of a flags object",
			bad:      content(3, simpleObjs(t, 2).with(objs{"f1": badFormat})),
			wantCode: flagruntime.CodeInvalidContent,
		},
		{
			name:     "object with an empty name",
			bad:      content(3, simpleObjs(t, 2).with(objs{"": flagsObj(1, "x")})),
			wantCode: flagruntime.CodeInvalidContent,
		},
		{
			name:     "the candidate is vetoed before the store",
			bad:      content(3, simpleObjs(t, 2)),
			wantCode: flagruntime.CodeVetoed,
			veto:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var veto atomic.Bool

			r := newRig(t, flagruntime.Hooks{BeforeSwap: func(*flagruntime.Runtime) error {
				if veto.Load() {
					return errRefused
				}

				return nil
			}})
			r.start()

			r.feed(content(2, simpleObjs(t, 1)))

			before := r.holder.Load()

			veto.Store(tt.veto)

			res := r.feed(tt.bad)

			assert.Equal(t, flagruntime.OutcomeRejected, res.Outcome)
			assert.Equal(t, tt.wantCode, res.Code)
			assert.NotEmpty(t, res.Detail)
			assert.Same(t, before, r.holder.Load(), "a rejected candidate stores nothing")
			assert.Equal(t, res, r.eng.Result(), "the reason is recorded")
			assert.Nil(t, before.Status.LastRejected)

			veto.Store(false)

			res = r.feed(content(4, simpleObjs(t, 3)))

			require.Equal(t, flagruntime.OutcomeApplied, res.Outcome)

			next := r.holder.Load()

			require.NotNil(t, next.Status.LastRejected, "the next status records the last rejection")
			assert.Equal(t, tt.wantCode, next.Status.LastRejected.Code)
			assert.Equal(t, flagruntime.OutcomeRejected, next.Status.LastRejected.Outcome)
			assert.Greater(t, next.Snapshot, before.Snapshot)
		})
	}
}

func TestEngineBuildFailureStoresNothing(t *testing.T) {
	t.Parallel()

	var (
		cancelOnParse atomic.Bool
		r             *rig
	)

	r = newRig(t, flagruntime.Hooks{OnParse: func(flagruntime.ParseEvent) {
		if cancelOnParse.Load() {
			// the context of the build is the context of Run: a build that cannot finish stores nothing
			r.cancel()
		}
	}})
	r.start()

	r.feed(content(1, simpleObjs(t, 1)))

	before := r.holder.Load()

	cancelOnParse.Store(true)

	res := r.feed(content(2, simpleObjs(t, 2)))

	assert.Equal(t, flagruntime.OutcomeRejected, res.Outcome)
	assert.Equal(t, flagruntime.CodeBuildFailed, res.Code)
	assert.Contains(t, res.Detail, "canceled")
	assert.Same(t, before, r.holder.Load(), "a failed build stores nothing")
}

func TestEngineLatestWinsCoalescing(t *testing.T) {
	t.Parallel()

	r := newRig(t, flagruntime.Hooks{})

	for gen := 1; gen <= 5; gen++ {
		r.eng.Submit(content(uint64(gen), simpleObjs(t, gen)))
	}

	r.start()

	res := r.nextRound()

	assert.Equal(t, uint64(5), res.Generation, "only the last content is built")
	assert.Equal(t, uint64(1), res.Snapshot, "one round, one swap")
	assert.Len(t, r.parsed, 2, "the last content has two objects and nothing else was parsed")

	for _, e := range r.parsed {
		assert.Contains(t, []string{"f1", setsName}, e.Name)
	}

	fs, _ := r.holder.Load().FlagSetByName("a")
	assert.EqualValues(t, 5, value(fs, "x"))

	r.stop()

	assert.Empty(t, r.rounds, "no other round ran")
}

func TestEngineCoalescesBootstrapReloads(t *testing.T) {
	t.Parallel()

	r := newRig(t, flagruntime.Hooks{})

	for _, key := range []string{"one", "two", "three"} {
		r.eng.Reload(bootstrapFromYAML(t, "version: 2\nauth:\n  adminKeys: ["+key+"]\n"))
	}

	r.start()

	res := r.nextRound()

	assert.Equal(t, flagruntime.OutcomeApplied, res.Outcome)
	assert.Equal(t, uint64(1), res.Snapshot)
	assert.Equal(t, map[string]struct{}{"three": {}}, r.holder.Load().AdminKeys)
}

func TestEngineContentAndBootstrapInOneRound(t *testing.T) {
	t.Parallel()

	r := newRig(t, flagruntime.Hooks{})

	r.eng.Submit(content(1, simpleObjs(t, 1)))
	r.eng.Reload(bootstrapFromYAML(t, "version: 2\nauth:\n  adminKeys: [adm]\n"))
	r.start()

	res := r.nextRound()

	current := r.holder.Load()

	assert.Equal(t, uint64(1), res.Snapshot, "both inputs land in one swap")
	assert.True(t, isAdmin(current, "adm"))
	assert.False(t, isAdmin(current, "other"))
	assert.Len(t, current.FlagSets, 1)
}

// bootstrapBase is the configuration the process started with in the pendingRestart tests.
const bootstrapBase = `
version: 2
auth:
  adminKeys: [admin-1]
server:
  unixSocketPath: /run/a.sock
  listeners:
    - {name: api, address: ":8080"}
connections:
  main: {type: s3, bucket: b}
controlPlane:
  connection: main
  stateDir: /var/a
  manifest: {key: m.yaml}
`

func TestEngineBootstrapAdminKeysAreLiveAndOthersPendRestart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		edits       [][2]string
		extra       string
		wantAdmin   []string
		wantPending []string
	}{
		{
			name:      "an added admin key applies live and nothing needs a restart",
			edits:     [][2]string{{"[admin-1]", "[admin-1, admin-2]"}},
			wantAdmin: []string{adminOne, "admin-2"},
		},
		{
			name:        "a changed listener is reported and the admin change still applies",
			edits:       [][2]string{{"[admin-1]", "[admin-2]"}, {":8080", ":9090"}},
			wantAdmin:   []string{"admin-2"},
			wantPending: []string{flagruntime.SettingServerListeners},
		},
		{
			name: "a changed unix socket, state dir and control plane connection are reported",
			edits: [][2]string{
				{"/run/a.sock", "/run/b.sock"}, {"/var/a", "/var/b"}, {"connection: main", "connection: other"},
			},
			wantAdmin: []string{adminOne},
			wantPending: []string{
				flagruntime.SettingControlPlaneConn,
				flagruntime.SettingControlPlaneState,
				flagruntime.SettingServerUnixSocket,
			},
		},
		{
			name:        "a changed connection and manifest are reported",
			edits:       [][2]string{{"bucket: b", "bucket: other"}, {"m.yaml", "n.yaml"}},
			wantAdmin:   []string{adminOne},
			wantPending: []string{flagruntime.SettingConnections, flagruntime.SettingControlPlaneMan},
		},
		{
			name: "a removed control plane is reported",
			edits: [][2]string{
				{"controlPlane:\n  connection: main\n  stateDir: /var/a\n  manifest: {key: m.yaml}\n", ""},
			},
			wantAdmin:   []string{adminOne},
			wantPending: []string{flagruntime.SettingControlPlane},
		},
		{
			name:      "exporters, notifiers and observability are reported",
			extra:     "exporters: {e: {type: file}}\nnotifiers: {n: {type: slack}}\nobservability: {logging: {level: debug}}\n",
			wantAdmin: []string{adminOne},
			wantPending: []string{
				flagruntime.SettingExporters, flagruntime.SettingNotifiers, flagruntime.SettingObservability,
			},
		},
		{
			name:        "a changed version is reported",
			edits:       [][2]string{{"version: 2", "version: 3"}},
			wantAdmin:   []string{adminOne},
			wantPending: []string{flagruntime.SettingVersion},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			next := bootstrapBase

			for _, edit := range tt.edits {
				require.Contains(t, next, edit[0], "the edit applies to the base")

				next = strings.Replace(next, edit[0], edit[1], 1)
			}

			next += tt.extra

			rig := newRig(t, flagruntime.Hooks{}, flagruntime.WithInitialBootstrap(bootstrapFromYAML(t, bootstrapBase)))
			rig.start()

			rig.feed(content(1, simpleObjs(t, 1)))

			before := rig.holder.Load()
			res := rig.reload(bootstrapFromYAML(t, next))

			after := rig.holder.Load()

			assert.Equal(t, flagruntime.OutcomeApplied, res.Outcome)
			assert.Equal(t, before.Generation, after.Generation, "a bootstrap change keeps the content generation")
			assert.Equal(t, before.Snapshot+1, after.Snapshot)
			assert.Len(t, after.AdminKeys, len(tt.wantAdmin))

			for _, key := range tt.wantAdmin {
				assert.True(t, isAdmin(after, key), "admin key %s", key)
			}

			assert.Equal(t, tt.wantPending, after.Status.PendingRestart)
			assert.Same(t, before.FlagSets["a"], after.FlagSets["a"], "the flag sets are untouched")
			assert.Equal(t, uint64(1), after.Status.Generation)
		})
	}
}

func TestEngineBootstrapNoChangeStoresNothing(t *testing.T) {
	t.Parallel()

	const cfg = "version: 2\nauth:\n  adminKeys: [admin-1]\n"

	r := newRig(t, flagruntime.Hooks{})
	r.start()

	first := r.reload(bootstrapFromYAML(t, cfg))

	assert.Equal(t, flagruntime.OutcomeApplied, first.Outcome, "the first configuration sets the admin keys")

	before := r.holder.Load()
	res := r.reload(bootstrapFromYAML(t, cfg))

	assert.Equal(t, flagruntime.OutcomeIgnored, res.Outcome)
	assert.Equal(t, "unchanged", res.Code)
	assert.Same(t, before, r.holder.Load())
}

func TestEngineBootstrapAdminKeyThatDoesNotResolveIsRejected(t *testing.T) {
	t.Parallel()

	const canary = "CANARY-ENV-NAME-VALUE"

	r := newRig(t, flagruntime.Hooks{}, flagruntime.WithSecretOptions(bootstrap.LoadOptions{
		LookupEnv: func(name string) (string, bool) { return canary, name == "KNOWN" },
	}))
	r.start()

	r.reload(bootstrapFromYAML(t, "version: 2\nauth:\n  adminKeys: [{fromEnv: KNOWN}]\n"))

	before := r.holder.Load()

	assert.True(t, isAdmin(before, canary), "a reference resolves through the injected environment")

	res := r.reload(bootstrapFromYAML(t, "version: 2\nauth:\n  adminKeys: [{fromEnv: UNKNOWN}]\n"))

	assert.Equal(t, flagruntime.OutcomeRejected, res.Outcome)
	assert.Equal(t, flagruntime.CodeInvalidBootstrap, res.Code)
	assert.Same(t, before, r.holder.Load(), "an unresolved admin key changes nothing")
	assert.NotContains(t, res.Detail, canary)
	assert.Contains(t, res.Detail, "adminKeys[0]")
}

// The number of events is a property of the round, not of the stored runtime: the status has no such member.
func TestStoredStatusHasNoEventCount(t *testing.T) {
	t.Parallel()

	harness := newRig(t, flagruntime.Hooks{})
	harness.start()

	round := harness.feed(content(1, simpleObjs(t, 1)))

	stored, err := json.Marshal(harness.holder.Load().Status)
	require.NoError(t, err)

	var status map[string]json.RawMessage

	require.NoError(t, json.Unmarshal(stored, &status))

	assert.JSONEq(t, `{"outcome":"applied","code":"applied","detail":"","generation":1,"snapshot":1}`,
		string(status["lastReload"]), "the status carries the reload, and no event count")

	reported, err := json.Marshal(round)
	require.NoError(t, err)

	assert.JSONEq(t,
		`{"outcome":"applied","code":"applied","detail":"","generation":1,"snapshot":1,"events":1}`, string(reported))
}
