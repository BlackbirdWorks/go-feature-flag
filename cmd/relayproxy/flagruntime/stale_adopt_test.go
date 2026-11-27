package flagruntime_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/cache"
)

// stale marks an object stale, keeping its bytes (the reader serves the last good bytes of a stale name).
func stale(in *flagruntime.Object) flagruntime.Object {
	out := *in
	out.State, out.Reason = flagruntime.ObjectStale, "error"

	return out
}

// A stale object that carries bytes is the last good content of the reader (restored from its state directory
// after a restart). A runtime that never loaded the name serves it, and reports the object stale; a runtime that
// holds content of the name keeps what it has. Without this a restart would lose the last-known-good content.
func TestStaleObject_AdoptedOnlyWhenNeverLoaded(t *testing.T) {
	t.Parallel()

	good := flagsObj(7, "f")
	other := flagsObj(8, "f")
	broken := obj(flagruntime.KindFlags, []byte("{{ not yaml"))
	doc := setsObj(t, fset("s", src(flagsName), keyA))

	tests := []struct {
		wantValue        any
		first            objs
		second           objs
		name             string
		wantState        flagruntime.ObjectState
		wantSetState     flagruntime.SetState
		wantFlags        []string
		wantFirst        inputClaim
		wantSecond       inputClaim
		wantParsed       int
		wantDecodes      bool
		wantSameChecksum bool
	}{
		{
			name:      "stale with bytes, never loaded",
			wantFirst: inputClaim{flagruntime.ObjectStale, true}, wantDecodes: true,
			first:     objs{flagsName: stale(&good), setsName: doc},
			wantValue: 7, wantState: flagruntime.ObjectStale, wantSetState: flagruntime.StateDegraded,
			wantParsed: 2, wantFlags: []string{"f"},
		},
		{
			name:      "stale without bytes, never loaded",
			wantFirst: inputClaim{flagruntime.ObjectStale, false},
			first: objs{
				flagsName: {Kind: flagruntime.KindFlags, Format: "yaml", Checksum: "x", State: flagruntime.ObjectStale},
				setsName:  doc,
			},
			wantValue:    nil,
			wantState:    flagruntime.ObjectStale,
			wantSetState: flagruntime.StatePending,
			wantParsed:   1,
			wantFlags:    []string{},
		},
		{
			name:      "stale with bytes that do not decode",
			wantFirst: inputClaim{flagruntime.ObjectStale, true}, wantDecodes: false,
			first:     objs{flagsName: stale(&broken), setsName: doc},
			wantValue: nil, wantState: flagruntime.ObjectInvalid, wantSetState: flagruntime.StatePending,
			wantParsed: 2, wantFlags: []string{},
		},
		{
			name:      "stale with other bytes after a good load keeps the loaded content",
			wantFirst: inputClaim{flagruntime.ObjectOK, true}, wantSecond: inputClaim{flagruntime.ObjectStale, true},
			wantDecodes: true, wantSameChecksum: false,
			first:     objs{flagsName: good, setsName: doc},
			second:    objs{flagsName: stale(&other), setsName: doc},
			wantValue: 7, wantState: flagruntime.ObjectStale, wantSetState: flagruntime.StateDegraded,
			wantParsed: 2, wantFlags: []string{"f"},
		},
		{
			name:      "recovery to the same checksum is not parsed again",
			wantFirst: inputClaim{flagruntime.ObjectStale, true}, wantSecond: inputClaim{flagruntime.ObjectOK, true},
			wantDecodes: true, wantSameChecksum: true,
			first:     objs{flagsName: stale(&good), setsName: doc},
			second:    objs{flagsName: good, setsName: doc},
			wantValue: 7, wantState: flagruntime.ObjectOK, wantSetState: flagruntime.StateServing,
			wantParsed: 2, wantFlags: []string{"f"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			first := tt.first[flagsName]
			assertInput(t, "first", &first, tt.wantFirst)

			if tt.wantFirst.bytes {
				_, decodeErr := cache.ConvertToFlagStruct(tt.first[flagsName].Bytes, tt.first[flagsName].Format)
				assert.Equal(t, tt.wantDecodes, decodeErr == nil, "the bytes of the first input decode")
			}

			if tt.second != nil {
				second := tt.second[flagsName]
				assertInput(t, "second", &second, tt.wantSecond)
				assert.Equal(
					t,
					tt.wantSameChecksum,
					tt.first[flagsName].Checksum == tt.second[flagsName].Checksum,
					"the two inputs have the same checksum",
				)
			}

			parsed := 0
			harness := newRig(t, flagruntime.Hooks{OnParse: func(flagruntime.ParseEvent) { parsed++ }})
			harness.start()

			res := harness.feed(content(1, tt.first))
			require.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)

			if tt.second != nil {
				res = harness.feed(content(1, tt.second))
				require.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)
			}

			run := harness.holder.Load()
			set := run.FlagSets["s"]
			require.NotNil(t, set)

			assert.Equal(t, tt.wantValue, value(set, "f"))
			assert.ElementsMatch(t, tt.wantFlags, flagNames(set))
			assert.Equal(t, tt.wantSetState, set.State)
			assert.Equal(t, tt.wantState, run.Status.Objects["flags"].State)
			assert.Equal(t, tt.wantParsed, parsed)
		})
	}
}

// A stale flag-set document follows the same rule as a stale flags object: read when never loaded, otherwise the
// loaded one stays; bytes that do not decode leave the document invalid; recovery to the same bytes is not parsed
// again.
func TestStaleDocument_AdoptedOnlyWhenNeverLoaded(t *testing.T) {
	t.Parallel()

	doc := setsObj(t, fset("s", src(flagsName), keyA))
	other := setsObj(t, fset("other", src(flagsName), keyB))
	broken := obj(flagruntime.KindFlagSets, []byte("flagSets: [[["))
	flags := flagsObj(1, "f")

	tests := []struct {
		first            objs
		second           objs
		name             string
		wantDoc          flagruntime.ObjectState
		wantSets         []string
		wantFirst        inputClaim
		wantSecond       inputClaim
		wantParsed       int
		wantDecodes      bool
		wantSameChecksum bool
	}{
		{
			name: "never loaded, readable", first: objs{flagsName: flags, setsName: stale(&doc)},
			wantFirst: inputClaim{flagruntime.ObjectStale, true}, wantDecodes: true,
			wantDoc: flagruntime.ObjectStale, wantParsed: 2, wantSets: []string{"s"},
		},
		{
			name: "never loaded, not decodable", first: objs{flagsName: flags, setsName: stale(&broken)},
			wantFirst: inputClaim{flagruntime.ObjectStale, true}, wantDecodes: false,
			wantDoc: flagruntime.ObjectInvalid, wantParsed: 2, wantSets: []string{},
		},
		{
			name: "loaded: the other bytes are not read", first: objs{flagsName: flags, setsName: doc},
			wantFirst: inputClaim{flagruntime.ObjectOK, true}, wantSecond: inputClaim{flagruntime.ObjectStale, true},
			wantDecodes: true, wantSameChecksum: false,
			second:  objs{flagsName: flags, setsName: stale(&other)},
			wantDoc: flagruntime.ObjectStale, wantParsed: 2, wantSets: []string{"s"},
		},
		{
			name: "back to ok with the same bytes", first: objs{flagsName: flags, setsName: stale(&doc)},
			wantFirst: inputClaim{flagruntime.ObjectStale, true}, wantSecond: inputClaim{flagruntime.ObjectOK, true},
			wantDecodes: true, wantSameChecksum: true,
			second:  objs{flagsName: flags, setsName: doc},
			wantDoc: flagruntime.ObjectOK, wantParsed: 2, wantSets: []string{"s"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			first := tt.first[setsName]
			assertInput(t, "first", &first, tt.wantFirst)

			_, _, decodeErr := controlplane.UnmarshalFlagSet(tt.first[setsName].Bytes)
			assert.Equal(t, tt.wantDecodes, decodeErr == nil, "the bytes of the first input decode")

			if tt.second != nil {
				second := tt.second[setsName]
				assertInput(t, "second", &second, tt.wantSecond)
				assert.Equal(
					t,
					tt.wantSameChecksum,
					tt.first[setsName].Checksum == tt.second[setsName].Checksum,
					"the two inputs have the same checksum",
				)
			}

			parsed := 0
			harness := newRig(t, flagruntime.Hooks{OnParse: func(flagruntime.ParseEvent) { parsed++ }})
			harness.start()

			res := harness.feed(content(1, tt.first))
			require.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)

			if tt.second != nil {
				res = harness.feed(content(1, tt.second))
				require.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)
			}

			run := harness.holder.Load()

			assert.ElementsMatch(t, tt.wantSets, slices.Collect(maps.Keys(run.FlagSets)))
			assert.Equal(t, tt.wantDoc, run.Status.Objects[setsName].State)
			assert.Equal(t, tt.wantParsed, parsed)
		})
	}
}

// The last known good definition of a single flag lives in the memory of the running engine only. A restart that
// adopts stale bytes holding a flag that fails validation has no earlier definition of it: the flag is absent and
// counted as rejected (not kept), the other flags of the object are served. This pins that limit (see the docs).
func TestStaleObject_RejectedFlagHasNoEarlierDefinitionAfterARestart(t *testing.T) {
	t.Parallel()

	harness := newRig(t, flagruntime.Hooks{})
	harness.start()

	bytes := obj(flagruntime.KindFlags, join(flagsYAML(7, "f"), badYAML()))
	doc := setsObj(t, fset("s", src(flagsName), keyA))

	res := harness.feed(content(1, objs{flagsName: stale(&bytes), setsName: doc}))
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)

	run := harness.holder.Load()

	assert.Equal(t, []string{"f"}, flagNames(run.FlagSets["s"]))
	assert.Equal(t, flagruntime.StateDegraded, run.FlagSets["s"].State)

	rejected := run.Status.Objects[flagsName].Rejected
	require.Len(t, rejected, 1)
	assert.Equal(t, "y", rejected[0].Flag)
	assert.False(t, rejected[0].Kept)
}

// inputClaim is what a row says about the input object of one of its steps: the state it is fed in and whether it
// carries bytes, asserted on the fixture so that a row cannot pass with another input than its name says.
type inputClaim struct {
	state flagruntime.ObjectState
	bytes bool
}

// assertInput asserts the claim of a row on an input object. An empty state of an object is ok.
func assertInput(t *testing.T, name string, input *flagruntime.Object, claim inputClaim) {
	t.Helper()

	state := input.State
	if state == "" {
		state = flagruntime.ObjectOK
	}

	assert.Equal(t, claim.state, state, "%s: the state of the input", name)
	assert.Equal(t, claim.bytes, len(input.Bytes) > 0, "%s: the input carries bytes", name)
}
