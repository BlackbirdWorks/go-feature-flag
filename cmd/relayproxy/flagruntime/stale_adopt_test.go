package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
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
		name          string
		first         objs
		second        objs // nil: only the first content is fed
		wantValue     any
		wantState     flagruntime.ObjectState
		wantSetState  flagruntime.SetState
		wantParsed    int
		wantFlagCount int
	}{
		{
			name:      "stale with bytes, never loaded",
			first:     objs{flagsName: stale(&good), setsName: doc},
			wantValue: 7, wantState: flagruntime.ObjectStale, wantSetState: flagruntime.StateDegraded,
			wantParsed: 2, wantFlagCount: 1,
		},
		{
			name: "stale without bytes, never loaded",
			first: objs{
				flagsName: {Kind: flagruntime.KindFlags, Format: "yaml", Checksum: "x", State: flagruntime.ObjectStale},
				setsName:  doc,
			},
			wantValue:     nil,
			wantState:     flagruntime.ObjectStale,
			wantSetState:  flagruntime.StatePending,
			wantParsed:    1,
			wantFlagCount: 0,
		},
		{
			name:      "stale with bytes that do not decode",
			first:     objs{flagsName: stale(&broken), setsName: doc},
			wantValue: nil, wantState: flagruntime.ObjectInvalid, wantSetState: flagruntime.StatePending,
			wantParsed: 2, wantFlagCount: 0,
		},
		{
			name:      "stale with other bytes after a good load keeps the loaded content",
			first:     objs{flagsName: good, setsName: doc},
			second:    objs{flagsName: stale(&other), setsName: doc},
			wantValue: 7, wantState: flagruntime.ObjectStale, wantSetState: flagruntime.StateDegraded,
			wantParsed: 2, wantFlagCount: 1,
		},
		{
			name:      "recovery to the same checksum is not parsed again",
			first:     objs{flagsName: stale(&good), setsName: doc},
			second:    objs{flagsName: good, setsName: doc},
			wantValue: 7, wantState: flagruntime.ObjectOK, wantSetState: flagruntime.StateServing,
			wantParsed: 2, wantFlagCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

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
			assert.Len(t, set.Flags, tt.wantFlagCount)
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
		first        objs
		second       objs
		name         string
		wantSet      string
		wantGone     string
		wantDoc      flagruntime.ObjectState
		wantParsed   int
		wantSetCount int
	}{
		{
			name: "never loaded, readable", first: objs{flagsName: flags, setsName: stale(&doc)},
			wantSet: "s", wantDoc: flagruntime.ObjectStale, wantParsed: 2, wantSetCount: 1,
		},
		{
			name: "never loaded, not decodable", first: objs{flagsName: flags, setsName: stale(&broken)},
			wantDoc: flagruntime.ObjectInvalid, wantParsed: 2, wantSetCount: 0,
		},
		{
			name: "loaded: the other bytes are not read", first: objs{flagsName: flags, setsName: doc},
			second:  objs{flagsName: flags, setsName: stale(&other)},
			wantSet: "s", wantGone: "other", wantDoc: flagruntime.ObjectStale, wantParsed: 2, wantSetCount: 1,
		},
		{
			name: "back to ok with the same bytes", first: objs{flagsName: flags, setsName: stale(&doc)},
			second:  objs{flagsName: flags, setsName: doc},
			wantSet: "s", wantDoc: flagruntime.ObjectOK, wantParsed: 2, wantSetCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

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

			assert.Len(t, run.FlagSets, tt.wantSetCount)
			assert.Equal(t, tt.wantDoc, run.Status.Objects[setsName].State)
			assert.Equal(t, tt.wantParsed, parsed)

			if tt.wantSet != "" {
				assert.Contains(t, run.FlagSets, tt.wantSet)
			}

			if tt.wantGone != "" {
				assert.NotContains(t, run.FlagSets, tt.wantGone)
			}
		})
	}
}
