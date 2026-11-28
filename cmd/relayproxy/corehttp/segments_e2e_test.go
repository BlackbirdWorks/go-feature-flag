package corehttp_test

import (
	"encoding/json"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/controlplane"
)

const (
	segFlagsObject = "flags-seg"
	segObjectName  = "people"
	segFlagsYAML   = `
beta-flag:
  variations: {on: true, off: false}
  targeting:
    - {name: members, query: 'targetingKey in segment("beta")', variation: on}
  defaultRule: {name: default, variation: off}
`
	segDocOne = "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: beta\n    members: [alice]\n" +
		"  - name: audience\n    connection: audiences\n    mode: lookup\n"
	segDocTwo = "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: beta\n    members: [alice, bob]\n" +
		"  - name: audience\n    connection: audiences\n    mode: lookup\n"
)

// deadConnection is a connection of the bootstrap file that can serve segments, at an address where nothing listens:
// the listener was open to take the port and is closed at once, so a store that runs on the machine cannot answer.
func deadConnection(tb testing.TB) string {
	tb.Helper()

	var lc net.ListenConfig

	listener, err := lc.Listen(tb.Context(), "tcp", "127.0.0.1:0")
	require.NoError(tb, err)

	address := listener.Addr().String()
	require.NoError(tb, listener.Close())

	return "  audiences: {type: redis, addresses: [\"" + address + "\"]}\n"
}

func ofrepValue(t *testing.T, rig *v2, flag, key string) (int, []byte) {
	t.Helper()

	code, _, body := do(t, http.MethodPost, rig.url+pathOFREPOne+flag,
		`{"context":{"targetingKey":"`+key+`"}}`, keyHeader(tenantKey))

	return code, body
}

func ofrepResult(t *testing.T, rig *v2, key string) bool {
	t.Helper()

	code, body := ofrepValue(t, rig, "beta-flag", key)
	require.Equal(t, http.StatusOK, code, string(body))

	var got struct {
		Value bool `json:"value"`
	}

	require.NoError(t, json.Unmarshal(body, &got))

	return got.Value
}

// Published segments reach the evaluation: static members target through OFREP, a reload that changes the members
// is seen by the next request, a flag without segments answers the same bytes, and /info counts the segments of the
// flag set by kind.
func TestE2E_StaticSegmentsTargetThroughOFREP(t *testing.T) {
	t.Parallel()

	rig := newV2With(t, &v2Options{connections: deadConnection(t)})
	rig.flags[segFlagsObject] = []byte(segFlagsYAML)
	rig.segments = map[string][]byte{segObjectName: []byte(segDocOne)}
	rig.sets = []controlplane.FlagSet{{
		Name: setName, Sources: []string{flagsObject, segFlagsObject}, SegmentSources: []string{segObjectName},
		APIKeys: []string{tenantKey},
	}}

	_, plainBefore := ofrepValue(t, rig, "bool-flag", "user-1")

	rig.publish(t)
	rig.poll(t)

	assert.True(t, ofrepResult(t, rig, "alice"), "a member of the static segment")
	assert.False(t, ofrepResult(t, rig, "bob"), "not a member")

	_, plainAfter := ofrepValue(t, rig, "bool-flag", "user-1")
	assert.Equal(t, string(plainBefore), string(plainAfter), "a flag without segments answers the same bytes")

	code, _, body := do(t, http.MethodGet, rig.url+"/info", "", nil)
	require.Equal(t, http.StatusOK, code)

	var info model.InfoResponse
	require.NoError(t, json.Unmarshal(body, &info))
	require.NotNil(t, info.Config)

	var counts *model.SegmentCountsInfo

	for i := range info.Config.FlagSets {
		if info.Config.FlagSets[i].Name == setName {
			counts = info.Config.FlagSets[i].Segments
		}
	}

	require.NotNil(t, counts)
	assert.Equal(t, model.SegmentCountsInfo{
		Static: 1, Dynamic: 1, DynamicStates: &model.DynamicStatesInfo{Valid: 1, StoreError: 1},
	}, *counts)

	// the members change: the next request sees them
	rig.segments[segObjectName] = []byte(segDocTwo)
	rig.publish(t)
	rig.poll(t)

	assert.True(t, ofrepResult(t, rig, "bob"), "bob joined the segment")
	assert.True(t, ofrepResult(t, rig, "alice"))
}

// A keyless request is served by the public flags with the static segments of their flag set.
func TestE2E_PublicFlagsAreTargetedByTheirStaticSegments(t *testing.T) {
	t.Parallel()

	rig := newV2With(t, &v2Options{connections: deadConnection(t)})
	rig.flags = map[string][]byte{segFlagsObject: []byte(segFlagsYAML)}
	rig.segments = map[string][]byte{segObjectName: []byte(segDocOne)}
	rig.sets = []controlplane.FlagSet{{
		Name: "open", Sources: []string{segFlagsObject}, SegmentSources: []string{segObjectName},
	}}

	rig.publish(t)
	rig.poll(t)

	tests := []struct {
		name      string
		key       string
		wantValue bool
	}{
		{name: "a member", key: "alice", wantValue: true},
		{name: "not a member", key: "bob"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, _, body := do(t, http.MethodPost, rig.url+pathOFREPOne+"beta-flag",
				`{"context":{"targetingKey":"`+tt.key+`"}}`, withJSON(http.Header{}))
			require.Equal(t, http.StatusOK, code, string(body))

			var got struct {
				Value bool `json:"value"`
			}

			require.NoError(t, json.Unmarshal(body, &got))
			assert.Equal(t, tt.wantValue, got.Value)
		})
	}
}
